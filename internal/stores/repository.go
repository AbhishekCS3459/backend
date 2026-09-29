package stores

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrNotFound = errors.New("store not found")

type Repository interface {
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]Summary, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Store, error)
	Create(ctx context.Context, store *Store, images []string) (*Summary, error)
	// UpdateLocked loads the store with FOR UPDATE, lets apply change it, and
	// saves the listed columns in the same transaction. An error from apply
	// aborts without writing.
	UpdateLocked(ctx context.Context, id uuid.UUID, apply func(*Store) error, columns ...string) (*Store, error)
	// Update changes the given details; a non-nil images slice replaces all photos.
	Update(ctx context.Context, id uuid.UUID, name, description *string, images *[]string) (*Summary, error)
	// FindBank returns nil, nil when the store has no payout account yet.
	FindBank(ctx context.Context, storeID uuid.UUID) (*Bank, error)
	SaveBank(ctx context.Context, bank *Bank) (*Bank, error)
	// Delete closes and soft-deletes the store; its history stays in the database.
	Delete(ctx context.Context, id uuid.UUID) error
	// FindLocation returns nil, nil when the store has no location yet.
	FindLocation(ctx context.Context, storeID uuid.UUID) (*Location, error)
	// SaveLocation creates or replaces the location and refreshes the store's
	// search rows in the same transaction.
	SaveLocation(ctx context.Context, loc *Location) (*Location, error)
	DefaultCategoryID(ctx context.Context) (uuid.UUID, error)
}

type repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

func (r *repository) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]Summary, error) {
	if len(ids) == 0 {
		return []Summary{}, nil
	}
	var rows []Store
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Order("created_at desc").Find(&rows).Error; err != nil {
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
	if store.OnboardingStatus == "" {
		store.OnboardingStatus = OnboardingDraft
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(store).Error; err != nil {
			return err
		}
		return insertMedia(tx, store.ID, images)
	})
	if err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}
	return r.attachSummary(ctx, *store)
}

func insertMedia(tx *gorm.DB, storeID uuid.UUID, images []string) error {
	for i, url := range images {
		media := Media{
			ID:        uuid.New(),
			StoreID:   storeID,
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
}

func (r *repository) FindByID(ctx context.Context, id uuid.UUID) (*Store, error) {
	var store Store
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&store).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find store: %w", err)
	}
	return &store, nil
}

func (r *repository) UpdateLocked(
	ctx context.Context, id uuid.UUID, apply func(*Store) error, columns ...string,
) (*Store, error) {
	var store Store
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// NO KEY UPDATE still serialises edits to the store, but unlike UPDATE it
		// doesn't block stock changes, whose ledger rows reference the store. A
		// visibility change below locks those stock rows, so UPDATE could deadlock.
		err := tx.Clauses(clause.Locking{Strength: "NO KEY UPDATE"}).Where("id = ?", id).First(&store).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		wasSearchable := store.searchable()
		if err := apply(&store); err != nil {
			return err
		}
		store.UpdatedAt = time.Now().UTC()
		if err := tx.Model(&store).Select(append(columns, "updated_at")).Updates(&store).Error; err != nil {
			return err
		}
		if store.searchable() != wasSearchable {
			return availability.RefreshStore(tx, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &store, nil
}

func (r *repository) Update(
	ctx context.Context, id uuid.UUID, name, description *string, images *[]string,
) (*Summary, error) {
	var store Store
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&store).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		columns := []string{"updated_at"}
		if name != nil {
			store.Name = *name
			columns = append(columns, "name")
		}
		if description != nil {
			store.Description = *description
			columns = append(columns, "description")
		}
		store.UpdatedAt = time.Now().UTC()
		if err := tx.Model(&store).Select(columns).Updates(&store).Error; err != nil {
			return err
		}
		if images == nil {
			return nil
		}
		if err := tx.Where("store_id = ?", id).Delete(&Media{}).Error; err != nil {
			return err
		}
		return insertMedia(tx, id, *images)
	})
	if errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("update store: %w", err)
	}
	return r.attachSummary(ctx, store)
}

func (r *repository) FindBank(ctx context.Context, storeID uuid.UUID) (*Bank, error) {
	// Find rather than First: a store without an account yet is normal, not an error to log.
	var rows []Bank
	if err := r.db.WithContext(ctx).Where("store_id = ?", storeID).Limit(1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("find store bank: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		closed := tx.Model(&Store{}).Where("id = ?", id).
			Updates(map[string]any{"status": "INACTIVE", "is_open": false, "updated_at": time.Now().UTC()})
		if closed.Error != nil {
			return closed.Error
		}
		if closed.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Where("id = ?", id).Delete(&Store{}).Error; err != nil {
			return err
		}
		return availability.RefreshStore(tx, id)
	})
	if errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil {
		return fmt.Errorf("delete store: %w", err)
	}
	return nil
}

func (r *repository) SaveBank(ctx context.Context, bank *Bank) (*Bank, error) {
	now := time.Now().UTC()
	bank.CreatedAt, bank.UpdatedAt = now, now
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "store_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"method", "account_holder_name", "account_number", "ifsc", "bank_name", "qr_url", "updated_at",
		}),
	}).Create(bank).Error
	if err != nil {
		return nil, fmt.Errorf("save store bank: %w", err)
	}
	return bank, nil
}

func (r *repository) FindLocation(ctx context.Context, storeID uuid.UUID) (*Location, error) {
	var rows []Location
	if err := r.db.WithContext(ctx).Where("store_id = ?", storeID).Limit(1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("find store location: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (r *repository) SaveLocation(ctx context.Context, loc *Location) (*Location, error) {
	now := time.Now().UTC()
	loc.CreatedAt, loc.UpdatedAt = now, now
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "store_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"address_line", "city", "pincode", "lat", "lng", "service_area_radius_km", "updated_at",
			}),
		}).Create(loc).Error
		if err != nil {
			return err
		}
		return availability.RefreshStore(tx, loc.StoreID)
	})
	if err != nil {
		return nil, fmt.Errorf("save store location: %w", err)
	}
	return loc, nil
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
			COALESCE(SUM(on_hand_quantity), 0)::int,
			COALESCE(SUM(CASE WHEN on_hand_quantity - reserved_quantity <= low_stock_threshold THEN 1 ELSE 0 END), 0)::int
		FROM inventory
		WHERE store_id = ? AND unlisted_at IS NULL
	`, store.ID).Row().Scan(&productCount, &totalInventory, &lowStock)

	bank, err := r.FindBank(ctx, store.ID)
	if err != nil {
		return nil, err
	}
	loc, err := r.FindLocation(ctx, store.ID)
	if err != nil {
		return nil, err
	}
	return &Summary{
		Store:          store,
		Images:         images,
		ProductCount:   productCount,
		TotalInventory: totalInventory,
		LowStockCount:  lowStock,
		Onboarding:     onboardingSummary(store, bank, loc),
	}, nil
}
