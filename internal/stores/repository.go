package stores

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrNotFound = errors.New("store not found")

type Repository interface {
	ListByRetailer(ctx context.Context, retailerID uuid.UUID) ([]Summary, error)
	FindOwned(ctx context.Context, storeID, retailerID uuid.UUID) (*Store, error)
	Create(ctx context.Context, store *Store, images []string) (*Summary, error)
	DefaultCategoryID(ctx context.Context) (uuid.UUID, error)
}

type repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

func (r *repository) ListByRetailer(ctx context.Context, retailerID uuid.UUID) ([]Summary, error) {
	var rows []Store
	if err := r.db.WithContext(ctx).Where("retailer_id = ?", retailerID).Order("created_at desc").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list stores: %w", err)
	}
	out := make([]Summary, 0, len(rows))
	for _, row := range rows {
		summary, err := r.attachSummary(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, *summary)
	}
	return out, nil
}

func (r *repository) FindOwned(ctx context.Context, storeID, retailerID uuid.UUID) (*Store, error) {
	var row Store
	err := r.db.WithContext(ctx).Where("id = ? AND retailer_id = ?", storeID, retailerID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find store: %w", err)
	}
	return &row, nil
}

func (r *repository) Create(ctx context.Context, store *Store, images []string) (*Summary, error) {
	now := time.Now().UTC()
	store.ID = uuid.New()
	store.CreatedAt = now
	store.UpdatedAt = now
	if store.Status == "" {
		store.Status = "ACTIVE"
	}
	if store.KYBStatus == "" {
		store.KYBStatus = "PENDING"
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(store).Error; err != nil {
			return err
		}
		for i, url := range images {
			media := Media{
				ID:        uuid.New(),
				StoreID:   store.ID,
				MediaURL:  url,
				Type:      "photo",
				SortOrder: i,
				IsCover:   i == 0,
			}
			if err := tx.Create(&media).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}
	return r.attachSummary(ctx, *store)
}

func (r *repository) DefaultCategoryID(ctx context.Context) (uuid.UUID, error) {
	var idStr string
	err := r.db.WithContext(ctx).Raw(`SELECT id::text FROM category WHERE parent_category_id IS NULL ORDER BY created_at LIMIT 1`).Scan(&idStr).Error
	if err != nil {
		return uuid.Nil, fmt.Errorf("default category: %w", err)
	}
	if strings.TrimSpace(idStr) == "" {
		id := uuid.New()
		if err := r.db.WithContext(ctx).Exec(`INSERT INTO category (id, name, is_active) VALUES (?, 'General', TRUE)`, id).Error; err != nil {
			return uuid.Nil, fmt.Errorf("create default category: %w", err)
		}
		return id, nil
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("default category: %w", err)
	}
	return id, nil
}

func (r *repository) attachSummary(ctx context.Context, store Store) (*Summary, error) {
	var media []Media
	if err := r.db.WithContext(ctx).Where("store_id = ?", store.ID).Order("sort_order asc, is_cover desc").Find(&media).Error; err != nil {
		return nil, fmt.Errorf("store media: %w", err)
	}
	images := make([]string, 0, len(media))
	for _, item := range media {
		images = append(images, item.MediaURL)
	}

	var productCount, totalInventory, lowStock int
	_ = r.db.WithContext(ctx).Raw(`
		SELECT
			COUNT(*)::int,
			COALESCE(SUM(quantity_available), 0)::int,
			COALESCE(SUM(CASE WHEN quantity_available = 0 OR quantity_available <= low_stock_threshold THEN 1 ELSE 0 END), 0)::int
		FROM inventory
		WHERE store_id = ?
	`, store.ID).Row().Scan(&productCount, &totalInventory, &lowStock)

	return &Summary{
		Store:          store,
		Images:         images,
		ProductCount:   productCount,
		TotalInventory: totalInventory,
		LowStockCount:  lowStock,
	}, nil
}
