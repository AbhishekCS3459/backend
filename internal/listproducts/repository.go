package listproducts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/catalogitem"
	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

var (
	ErrNotFound = errors.New("listing not found")
	// ErrVariantNotFound also covers variants of another retailer's products.
	ErrVariantNotFound = errors.New("product not found")
	// ErrSKUTaken: SKUs are unique per retailer (product_variant_retailer_sku_key).
	ErrSKUTaken = errors.New("you already have a product with this SKU")
	// ErrCatalogProductTaken: a retailer owns at most one variant of a catalogue
	// product (product_variant_retailer_catalog_key_key).
	ErrCatalogProductTaken = errors.New("you already have this catalogue product")
)

// openingStockNote labels the history entry for stock entered while adding a product.
const openingStockNote = "Opening stock when added to the store"

type Repository interface {
	ListStoreProducts(ctx context.Context, storeID uuid.UUID, filter ListFilter) ([]Listing, error)
	StoreStats(ctx context.Context, storeID uuid.UUID) (*StoreSummary, error)
	ListCatalog(ctx context.Context, retailerID, storeID uuid.UUID, query string) ([]CatalogItem, error)
	AddListing(ctx context.Context, actorID, retailerID, storeID uuid.UUID, req *AddRequest, available bool) (*Listing, error)
	// CreateAndList creates a product and lists it. source is set only for a
	// product added from the catalogue.
	CreateAndList(
		ctx context.Context, actorID, retailerID, storeID uuid.UUID, req *CreateProductRequest,
		source *productcatalog.CanonicalProduct,
	) (*Listing, error)
	UpdateListing(ctx context.Context, storeID, variantID uuid.UUID, req *UpdateRequest) (*Listing, error)
	RemoveListing(ctx context.Context, storeID, variantID uuid.UUID) error
	FindListing(ctx context.Context, storeID, variantID uuid.UUID) (*Listing, error)
}

// repository reads listings directly but makes every inventory write through
// the inventory ledger, which owns stock and its history.
type repository struct {
	db     *gorm.DB
	ledger *inventory.Ledger
}

func NewRepository(db *gorm.DB, ledger *inventory.Ledger) Repository {
	return &repository{db: db, ledger: ledger}
}

const listingSelect = `
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
		i.price::float8 AS price,
		i.price_updated_at,
		i.on_hand_quantity AS on_hand,
		i.reserved_quantity AS reserved,
		i.on_hand_quantity - i.reserved_quantity AS available,
		i.low_stock_threshold,
		i.is_available
	FROM inventory i
	JOIN product_variant v ON v.id = i.product_variant_id
	JOIN product p ON p.id = v.product_id
	LEFT JOIN brand b ON b.id = p.brand_id
	LEFT JOIN category c ON c.id = p.category_id
	WHERE i.store_id = ? AND i.unlisted_at IS NULL
`

func (r *repository) ListStoreProducts(ctx context.Context, storeID uuid.UUID, filter ListFilter) ([]Listing, error) {
	sql := listingSelect
	args := []interface{}{storeID}
	if len(filter.VariantIDs) > 0 {
		sql += ` AND i.product_variant_id IN ?`
		args = append(args, filter.VariantIDs)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		sql += ` AND (p.name ILIKE ? OR v.sku ILIKE ? OR COALESCE(b.name, '') ILIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	const available = `(i.on_hand_quantity - i.reserved_quantity)`
	switch strings.ToLower(strings.TrimSpace(filter.Status)) {
	case "low":
		sql += ` AND i.is_available = TRUE AND ` + available + ` > 0 AND ` + available + ` <= i.low_stock_threshold`
	case "out":
		sql += ` AND i.is_available = TRUE AND ` + available + ` <= 0`
	case "unavailable":
		sql += ` AND i.is_available = FALSE`
	case "available":
		sql += ` AND i.is_available = TRUE AND ` + available + ` > 0`
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
			COALESCE(SUM(on_hand_quantity), 0)::int AS total_inventory,
			COALESCE(SUM(CASE WHEN is_available
				AND on_hand_quantity - reserved_quantity > 0
				AND on_hand_quantity - reserved_quantity <= low_stock_threshold THEN 1 ELSE 0 END), 0)::int AS low_stock_count,
			COALESCE(SUM(CASE WHEN is_available
				AND on_hand_quantity - reserved_quantity <= 0 THEN 1 ELSE 0 END), 0)::int AS out_of_stock_count
		FROM inventory
		WHERE store_id = ? AND unlisted_at IS NULL
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
			i.price::float8 AS store_price,
			i.id IS NOT NULL AND i.unlisted_at IS NULL AS listed
		FROM product p
		JOIN product_variant v ON v.product_id = p.id
		LEFT JOIN inventory i ON i.store_id = ? AND i.product_variant_id = v.id
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

func (r *repository) AddListing(
	ctx context.Context, actorID, retailerID, storeID uuid.UUID, req *AddRequest, available bool,
) (*Listing, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owned int64
		err := tx.Table("product_variant v").
			Joins("JOIN product p ON p.id = v.product_id").
			Where("v.id = ? AND p.retailer_id = ?", req.VariantID, retailerID).
			Count(&owned).Error
		if err != nil {
			return fmt.Errorf("check variant: %w", err)
		}
		if owned == 0 {
			return ErrVariantNotFound
		}
		return r.listWithOpeningStock(tx, actorID, inventory.ListInput{
			StoreID: storeID, VariantID: req.VariantID, LowStockThreshold: req.LowStockThreshold,
			IsAvailable: available, Price: req.Price,
		}, req.OpeningQuantity)
	})
	if err != nil {
		return nil, err
	}
	return r.FindListing(ctx, storeID, req.VariantID)
}

func (r *repository) CreateAndList(
	ctx context.Context, actorID, retailerID, storeID uuid.UUID, req *CreateProductRequest,
	source *productcatalog.CanonicalProduct,
) (*Listing, error) {
	available := true
	if req.IsAvailable != nil {
		available = *req.IsAvailable
	}
	var catalogKey *string
	attributes := AttributesJSON([]byte("{}"))
	if source != nil {
		key := source.CatalogKey()
		catalogKey = &key
		if source.Unit != "" {
			unit, err := json.Marshal(map[string]string{"unit": source.Unit})
			if err != nil {
				return nil, err
			}
			attributes = AttributesJSON(unit)
		}
	}
	var variantID uuid.UUID
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
			Attributes:  attributes,
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
			RetailerID:   retailerID,
			VariantLabel: "Default",
			SKU:          strings.TrimSpace(req.SKU),
			Price:        req.Price,
			CatalogKey:   catalogKey,
		}
		if err := tx.Create(&variant).Error; err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.ConstraintName {
				case "product_variant_retailer_sku_key":
					return ErrSKUTaken
				case "product_variant_retailer_catalog_key_key":
					return ErrCatalogProductTaken
				case "product_variant_reserved_sku":
					return ErrReservedSKU
				}
			}
			return err
		}
		variantID = variant.ID
		item := catalogitem.Manual(variant.ID, product.Name, strings.TrimSpace(req.Brand),
			strings.TrimSpace(req.Category), strings.TrimSpace(req.ImageURL))
		if source != nil {
			item = catalogitem.FromCatalogue(*source)
		}
		if _, err := catalogitem.Upsert(tx, item); err != nil {
			return err
		}
		return r.listWithOpeningStock(tx, actorID, inventory.ListInput{
			StoreID: storeID, VariantID: variant.ID, LowStockThreshold: req.LowStockThreshold,
			IsAvailable: available, Price: &req.Price,
		}, req.OpeningQuantity)
	})
	if err != nil {
		return nil, fmt.Errorf("create product listing: %w", err)
	}
	return r.FindListing(ctx, storeID, variantID)
}

// listWithOpeningStock lists (or relists) a product and records any opening
// quantity as received stock, inside the caller's transaction.
func (r *repository) listWithOpeningStock(tx *gorm.DB, actorID uuid.UUID, in inventory.ListInput, opening int) error {
	if _, err := r.ledger.List(tx, in); err != nil || opening == 0 {
		return err
	}
	actor, err := inventory.ResolveActor(tx, actorID)
	if err != nil {
		return err
	}
	_, err = r.ledger.Receive(tx, inventory.ReceiveInput{
		StoreID: in.StoreID, VariantID: in.VariantID, Quantity: opening, Note: openingStockNote, Actor: actor,
	})
	return err
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
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := r.ledger.UpdateSettings(tx, storeID, variantID, inventory.Settings{
			LowStockThreshold: req.LowStockThreshold,
			IsAvailable:       req.IsAvailable,
			Price:             req.Price,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return r.FindListing(ctx, storeID, variantID)
}

// RemoveListing unlists the product; its stock and history are kept so adding
// it back later restores both.
func (r *repository) RemoveListing(ctx context.Context, storeID, variantID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.ledger.Unlist(tx, storeID, variantID)
	})
}

func (r *repository) FindListing(ctx context.Context, storeID, variantID uuid.UUID) (*Listing, error) {
	var rows []Listing
	err := r.db.WithContext(ctx).Raw(listingSelect+` AND v.id = ?`, storeID, variantID).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("find listing: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	rows[0].StockStatus = stockStatus(rows[0])
	return &rows[0], nil
}

func stockStatus(row Listing) string {
	return inventory.StockStatus(inventory.Stock{OnHand: row.OnHand, Reserved: row.Reserved}, row.LowStockThreshold, row.IsAvailable, true)
}
