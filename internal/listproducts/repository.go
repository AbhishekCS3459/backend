package listproducts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrNotFound      = errors.New("listing not found")
	ErrAlreadyListed = errors.New("product is already listed in this store")
	ErrForbidden     = errors.New("store not found")
)

type Repository interface {
	AssertStoreOwned(ctx context.Context, storeID, retailerID uuid.UUID) error
	ListStoreProducts(ctx context.Context, storeID uuid.UUID, query, status string) ([]Listing, error)
	StoreStats(ctx context.Context, storeID uuid.UUID) (*StoreSummary, error)
	ListCatalog(ctx context.Context, retailerID, storeID uuid.UUID, query string) ([]CatalogItem, error)
	AddListing(ctx context.Context, storeID, variantID uuid.UUID, qty, threshold int, available bool) (*Listing, error)
	CreateAndList(ctx context.Context, retailerID, storeID uuid.UUID, req *CreateProductRequest) (*Listing, error)
	UpdateListing(ctx context.Context, storeID, variantID uuid.UUID, req *UpdateRequest) (*Listing, error)
	BulkUpdateListings(ctx context.Context, storeID uuid.UUID, items []BulkUpdateItem) (int, error)
	RemoveListing(ctx context.Context, storeID, variantID uuid.UUID) error
	FindListing(ctx context.Context, storeID, variantID uuid.UUID) (*Listing, error)
}

type repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

func (r *repository) AssertStoreOwned(ctx context.Context, storeID, retailerID uuid.UUID) error {
	var count int64
	err := r.db.WithContext(ctx).Table("store").Where("id = ? AND retailer_id = ?", storeID, retailerID).Count(&count).Error
	if err != nil {
		return fmt.Errorf("assert store: %w", err)
	}
	if count == 0 {
		return ErrForbidden
	}
	return nil
}

func (r *repository) ListStoreProducts(ctx context.Context, storeID uuid.UUID, query, status string) ([]Listing, error) {
	sql := `
		SELECT
			i.id AS inventory_id,
			p.id AS product_id,
			v.id AS variant_id,
			p.name,
			COALESCE(b.name, '') AS brand,
			v.sku,
			COALESCE(c.name, '') AS category,
			COALESCE((
				SELECT pi.image_url FROM product_image pi
				WHERE pi.product_id = p.id
				ORDER BY pi.sort_order ASC
				LIMIT 1
			), '') AS image_url,
			v.price::float8 AS price,
			i.quantity_available,
			i.quantity_reserved,
			i.low_stock_threshold,
			i.is_available
		FROM inventory i
		JOIN product_variant v ON v.id = i.product_variant_id
		JOIN product p ON p.id = v.product_id
		LEFT JOIN brand b ON b.id = p.brand_id
		LEFT JOIN category c ON c.id = p.category_id
		WHERE i.store_id = ?
	`
	args := []interface{}{storeID}
	if q := strings.TrimSpace(query); q != "" {
		sql += ` AND (p.name ILIKE ? OR v.sku ILIKE ? OR COALESCE(b.name, '') ILIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "low":
		sql += ` AND i.is_available = TRUE AND i.quantity_available > 0 AND i.quantity_available <= i.low_stock_threshold`
	case "out":
		sql += ` AND i.quantity_available = 0`
	case "unavailable":
		sql += ` AND i.is_available = FALSE`
	case "available":
		sql += ` AND i.is_available = TRUE AND i.quantity_available > 0`
	}
	sql += ` ORDER BY p.name ASC`

	var rows []Listing
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list store products: %w", err)
	}
	for i := range rows {
		rows[i].StockStatus = stockStatus(rows[i])
	}
	return rows, nil
}

func (r *repository) StoreStats(ctx context.Context, storeID uuid.UUID) (*StoreSummary, error) {
	var summary StoreSummary
	err := r.db.WithContext(ctx).Raw(`
		SELECT
			COUNT(*)::int AS product_count,
			COALESCE(SUM(quantity_available), 0)::int AS total_inventory,
			COALESCE(SUM(CASE WHEN is_available AND quantity_available > 0 AND quantity_available <= low_stock_threshold THEN 1 ELSE 0 END), 0)::int AS low_stock_count,
			COALESCE(SUM(CASE WHEN quantity_available = 0 THEN 1 ELSE 0 END), 0)::int AS out_of_stock_count
		FROM inventory
		WHERE store_id = ?
	`, storeID).Scan(&summary).Error
	if err != nil {
		return nil, fmt.Errorf("store stats: %w", err)
	}
	return &summary, nil
}

func (r *repository) ListCatalog(ctx context.Context, retailerID, storeID uuid.UUID, query string) ([]CatalogItem, error) {
	sql := `
		SELECT
			p.id AS product_id,
			v.id AS variant_id,
			p.name,
			COALESCE(b.name, '') AS brand,
			v.sku,
			COALESCE(c.name, '') AS category,
			COALESCE((
				SELECT pi.image_url FROM product_image pi
				WHERE pi.product_id = p.id
				ORDER BY pi.sort_order ASC
				LIMIT 1
			), '') AS image_url,
			v.price::float8 AS price,
			EXISTS (
				SELECT 1 FROM inventory i
				WHERE i.store_id = ? AND i.product_variant_id = v.id
			) AS listed
		FROM product p
		JOIN product_variant v ON v.product_id = p.id
		LEFT JOIN brand b ON b.id = p.brand_id
		LEFT JOIN category c ON c.id = p.category_id
		WHERE p.retailer_id = ?
	`
	args := []interface{}{storeID, retailerID}
	if q := strings.TrimSpace(query); q != "" {
		sql += ` AND (p.name ILIKE ? OR v.sku ILIKE ? OR COALESCE(b.name, '') ILIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	sql += ` ORDER BY p.name ASC LIMIT 100`

	var rows []CatalogItem
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list catalog: %w", err)
	}
	return rows, nil
}

func (r *repository) AddListing(ctx context.Context, storeID, variantID uuid.UUID, qty, threshold int, available bool) (*Listing, error) {
	var existing Inventory
	err := r.db.WithContext(ctx).Where("store_id = ? AND product_variant_id = ?", storeID, variantID).First(&existing).Error
	if err == nil {
		return nil, ErrAlreadyListed
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check listing: %w", err)
	}

	row := Inventory{
		ID:                uuid.New(),
		ProductVariantID:  variantID,
		StoreID:           storeID,
		QuantityAvailable: qty,
		LowStockThreshold: threshold,
		IsAvailable:       available,
		UpdatedAt:         time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, fmt.Errorf("add listing: %w", err)
	}
	return r.FindListing(ctx, storeID, variantID)
}

func (r *repository) CreateAndList(ctx context.Context, retailerID, storeID uuid.UUID, req *CreateProductRequest) (*Listing, error) {
	available := true
	if req.IsAvailable != nil {
		available = *req.IsAvailable
	}
	var listing *Listing
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		brandID, err := ensureNamed(tx, "brand", req.Brand)
		if err != nil {
			return err
		}
		categoryID, err := ensureNamed(tx, "category", req.Category)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		product := Product{
			ID:          uuid.New(),
			RetailerID:  &retailerID,
			CategoryID:  categoryID,
			BrandID:     brandID,
			Name:        strings.TrimSpace(req.Name),
			Description: strings.TrimSpace(req.Name),
			Attributes:  AttributesJSON([]byte("{}")),
			Status:      "ACTIVE",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(&product).Error; err != nil {
			return err
		}
		if url := strings.TrimSpace(req.ImageURL); url != "" {
			if err := tx.Create(&Image{
				ID:        uuid.New(),
				ProductID: product.ID,
				ImageURL:  url,
				SortOrder: 0,
			}).Error; err != nil {
				return err
			}
		}
		variant := Variant{
			ID:           uuid.New(),
			ProductID:    product.ID,
			VariantLabel: "Default",
			SKU:          strings.TrimSpace(req.SKU),
			Price:        req.Price,
		}
		if err := tx.Create(&variant).Error; err != nil {
			return err
		}
		inv := Inventory{
			ID:                uuid.New(),
			ProductVariantID:  variant.ID,
			StoreID:           storeID,
			QuantityAvailable: req.QuantityAvailable,
			LowStockThreshold: req.LowStockThreshold,
			IsAvailable:       available,
			UpdatedAt:         now,
		}
		if err := tx.Create(&inv).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create product listing: %w", err)
	}

	// find by sku in store
	var variantID uuid.UUID
	if err := r.db.WithContext(ctx).Raw(`
		SELECT v.id FROM product_variant v
		JOIN product p ON p.id = v.product_id
		WHERE p.retailer_id = ? AND v.sku = ?
		ORDER BY v.created_at DESC LIMIT 1
	`, retailerID, strings.TrimSpace(req.SKU)).Scan(&variantID).Error; err != nil {
		return nil, err
	}
	listing, err = r.FindListing(ctx, storeID, variantID)
	return listing, err
}

func ensureNamed(tx *gorm.DB, table, name string) (uuid.UUID, error) {
	name = strings.TrimSpace(name)
	var idStr string
	err := tx.Raw(fmt.Sprintf(`SELECT id::text FROM %s WHERE lower(name) = lower(?) LIMIT 1`, table), name).Scan(&idStr).Error
	if err != nil {
		return uuid.Nil, err
	}
	if strings.TrimSpace(idStr) != "" {
		return uuid.Parse(idStr)
	}
	id := uuid.New()
	if table == "brand" {
		err = tx.Exec(`INSERT INTO brand (id, name, is_active) VALUES (?, ?, TRUE)`, id, name).Error
	} else {
		err = tx.Exec(`INSERT INTO category (id, name, is_active) VALUES (?, ?, TRUE)`, id, name).Error
	}
	return id, err
}

func (r *repository) UpdateListing(ctx context.Context, storeID, variantID uuid.UUID, req *UpdateRequest) (*Listing, error) {
	var row Inventory
	err := r.db.WithContext(ctx).Where("store_id = ? AND product_variant_id = ?", storeID, variantID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find inventory: %w", err)
	}
	updates := map[string]interface{}{"updated_at": time.Now().UTC()}
	if req.QuantityAvailable != nil {
		if *req.QuantityAvailable < 0 {
			return nil, fmt.Errorf("quantity cannot be negative")
		}
		updates["quantity_available"] = *req.QuantityAvailable
	}
	if req.LowStockThreshold != nil {
		if *req.LowStockThreshold < 0 {
			return nil, fmt.Errorf("threshold cannot be negative")
		}
		updates["low_stock_threshold"] = *req.LowStockThreshold
	}
	if req.IsAvailable != nil {
		updates["is_available"] = *req.IsAvailable
	}
	if err := r.db.WithContext(ctx).Model(&row).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("update inventory: %w", err)
	}
	return r.FindListing(ctx, storeID, variantID)
}

func (r *repository) BulkUpdateListings(ctx context.Context, storeID uuid.UUID, items []BulkUpdateItem) (int, error) {
	updated := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for _, item := range items {
			updates := map[string]interface{}{"updated_at": now}
			if item.QuantityAvailable != nil {
				updates["quantity_available"] = *item.QuantityAvailable
			}
			if item.LowStockThreshold != nil {
				updates["low_stock_threshold"] = *item.LowStockThreshold
			}
			if item.IsAvailable != nil {
				updates["is_available"] = *item.IsAvailable
			}
			if len(updates) == 1 {
				continue
			}
			res := tx.Model(&Inventory{}).
				Where("store_id = ? AND product_variant_id = ?", storeID, item.VariantID).
				Updates(updates)
			if res.Error != nil {
				return fmt.Errorf("update inventory %s: %w", item.VariantID, res.Error)
			}
			if res.RowsAffected == 0 {
				return ErrNotFound
			}
			updated++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return updated, nil
}

func (r *repository) RemoveListing(ctx context.Context, storeID, variantID uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("store_id = ? AND product_variant_id = ?", storeID, variantID).Delete(&Inventory{})
	if res.Error != nil {
		return fmt.Errorf("remove listing: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repository) FindListing(ctx context.Context, storeID, variantID uuid.UUID) (*Listing, error) {
	rows, err := r.ListStoreProducts(ctx, storeID, "", "")
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].VariantID == variantID {
			return &rows[i], nil
		}
	}
	return nil, ErrNotFound
}

func stockStatus(row Listing) string {
	if !row.IsAvailable {
		return "unavailable"
	}
	if row.QuantityAvailable == 0 {
		return "out"
	}
	if row.QuantityAvailable <= row.LowStockThreshold {
		return "low"
	}
	return "in_stock"
}
