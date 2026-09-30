package marketplace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

const (
	// wordSimilarityThreshold is how close a misspelt query must be to a word
	// run in the product text (pg_trgm's default is 0.6, too strict for typos).
	wordSimilarityThreshold = "0.45"
	// queryTimeout bounds each marketplace query.
	queryTimeout = 3 * time.Second
)

type Repository interface {
	MatchProducts(ctx context.Context, q textQuery, limit int) ([]Product, error)
	StoreOffers(ctx context.Context, keys []string, p SearchParams, staleBefore time.Time) ([]offerRow, error)
	NearbyProducts(ctx context.Context, p NearbyParams, staleBefore time.Time) ([]nearbyRow, error)
	NearbyPage(ctx context.Context, p NearbyPageParams, staleBefore time.Time) ([]nearbyRow, error)
	NearbyCategories(ctx context.Context, p NearbyPageParams, staleBefore time.Time) ([]CategoryCount, error)
	FindProduct(ctx context.Context, catalogKey string) (*Product, error)
	FindStore(ctx context.Context, storeID uuid.UUID) (*storeRow, error)
	StoreProducts(ctx context.Context, p StoreProductsParams, q *textQuery, staleBefore time.Time) ([]storeProductRow, error)
	StoreProduct(ctx context.Context, storeID uuid.UUID, catalogKey string, staleBefore time.Time) (*storeProductRow, error)
}

type repository struct {
	db *gorm.DB
}

// NewRepository reads from db, the primary database, so customers see stock
// as of the last committed inventory change.
func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

// effectiveBucket is the bucket customers see: stock nobody has confirmed
// since the cutoff (the first parameter) is CONFIRM_WITH_STORE.
const effectiveBucket = `CASE WHEN a.last_stock_update_at < ? THEN 'CONFIRM_WITH_STORE' ELSE a.availability_bucket END`

const productColumns = `c.catalog_key, c.name, c.brand, c.unit, c.category_path, c.image_url`

// MatchProducts returns up to limit products matching q, best first: the
// exact name, then every word (name above brand above category), then the
// words run together ("cocacola" in "Coca-Cola"), then near misses (typos).
func (r *repository) MatchProducts(ctx context.Context, q textQuery, limit int) ([]Product, error) {
	var rows []Product
	err := r.readOnly(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec(`SET LOCAL pg_trgm.word_similarity_threshold = ` + wordSimilarityThreshold).Error; err != nil {
			return err
		}
		return tx.Raw(`
			SELECT catalog_key, name, brand, unit, category_path, image_url FROM (
				SELECT `+productColumns+`,
					CASE
						WHEN regexp_replace(lower(c.name), '[^[:alnum:]]+', '', 'g') = ? THEN 0
						WHEN c.search_vector @@ to_tsquery('simple', ?) THEN 1
						WHEN c.search_compact LIKE ? THEN 2
						ELSE 3
					END AS tier,
					ts_rank(c.search_vector, to_tsquery('simple', ?)) AS rank,
					GREATEST(word_similarity(?, c.search_text), word_similarity(?, c.search_compact)) AS score
				FROM catalog_item c
				WHERE c.search_vector @@ to_tsquery('simple', ?)
					OR c.search_compact LIKE ?
					OR ? <% c.search_text
					OR ? <% c.search_compact
			) m
			ORDER BY tier, rank DESC, score DESC, lower(name), catalog_key
			LIMIT ?`,
			q.compact, q.tsquery, q.like(), q.tsquery, q.text, q.compact,
			q.tsquery, q.like(), q.text, q.compact, limit).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("match products: %w", err)
	}
	return rows, nil
}

// offerRow is a store offer before grouping, with internal fields.
type offerRow struct {
	CatalogKey         string
	StoreID            uuid.UUID
	StoreName          string
	DistanceM          float64
	Lat                float64
	Lng                float64
	Price              float64
	AvailabilityBucket string
	LastStockUpdateAt  time.Time
	AvailableQty       int
	Version            int64
}

// storeOrder ranks one product's stores for each sort. Every order ends in
// store_id so equal rows keep a stable order.
var storeOrder = map[Sort]string{
	SortNearest:  `(r.availability_bucket = 'OUT'), r.distance_m, r.store_id`,
	SortCheapest: `(r.availability_bucket = 'OUT'), r.price, r.distance_m, r.store_id`,
	SortAvailability: `CASE r.availability_bucket WHEN 'IN_STOCK' THEN 0 WHEN 'LOW' THEN 1
		WHEN 'CONFIRM_WITH_STORE' THEN 2 ELSE 3 END, r.distance_m, r.store_id`,
}

// StoreOffers returns up to StoresPerProduct searchable stores per key within
// the radius, ranked by p.Sort. The point is inlined (not joined) so the
// partial GiST index on location serves ST_DWithin.
func (r *repository) StoreOffers(
	ctx context.Context, keys []string, p SearchParams, staleBefore time.Time,
) ([]offerRow, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	order, ok := storeOrder[p.Sort]
	if !ok {
		return nil, fmt.Errorf("unknown sort %q", p.Sort)
	}
	const point = `ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography`
	var rows []offerRow
	err := r.readOnly(ctx, func(tx *gorm.DB) error {
		return tx.Raw(`
			SELECT catalog_key, store_id, store_name, distance_m, lat, lng, price, availability_bucket,
				last_stock_update_at, available_qty, version
			FROM (
				SELECT r.*, ROW_NUMBER() OVER (PARTITION BY r.catalog_key ORDER BY `+order+`) AS rank
				FROM (
					SELECT a.catalog_key, a.store_id, s.name AS store_name,
						ST_Distance(a.location, `+point+`) AS distance_m,
						ST_Y(a.location::geometry) AS lat, ST_X(a.location::geometry) AS lng,
						a.price::float8 AS price,
						`+effectiveBucket+` AS availability_bucket,
						a.last_stock_update_at, a.available_qty, a.version
					FROM store_product_availability a
					JOIN store s ON s.id = a.store_id
					WHERE a.searchable
						AND a.catalog_key IN ?
						AND ST_DWithin(a.location, `+point+`, ?)
				) r
			) ranked
			WHERE rank <= ?
			ORDER BY catalog_key, rank`,
			p.Lng, p.Lat, staleBefore, keys, p.Lng, p.Lat, p.RadiusM, StoresPerProduct).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("find store offers: %w", err)
	}
	return rows, nil
}

// nearbyRow is a product sold near the point, with its top-level category and
// how many nearby products that category has.
type nearbyRow struct {
	Product
	SortName     string
	Category     string
	CategorySize int
}

// nearbyProductsCTE is every product sold within the radius with its
// top-level category (category_path's first "›" part), how many nearby
// stores have it and how far the nearest is. Parameters: lng, lat,
// staleBefore, lng, lat, radius, OtherCategory.
const nearbyProductsCTE = `
	WITH offers AS (
		SELECT a.catalog_key,
			ST_Distance(a.location, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography) AS distance_m,
			(` + effectiveBucket + `) <> 'OUT' AS available
		FROM store_product_availability a
		WHERE a.searchable AND ST_DWithin(a.location, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)
	), products AS (
		SELECT catalog_key, COUNT(*) FILTER (WHERE available) AS available_stores, MIN(distance_m) AS nearest_m
		FROM offers GROUP BY catalog_key
	), categorised AS (
		SELECT ` + productColumns + `, lower(c.name) AS sort_name, p.available_stores, p.nearest_m,
			COALESCE(NULLIF(btrim(split_part(c.category_path, '›', 1)), ''), ?) AS category
		FROM products p
		JOIN catalog_item c ON c.catalog_key = p.catalog_key
	)`

func nearbyArgs(lat, lng float64, radiusM int, staleBefore time.Time) []any {
	return []any{lng, lat, staleBefore, lng, lat, radiusM, OtherCategory}
}

// NearbyProducts returns products sold within the radius. Without a category:
// for up to MaxNearbyCategories top-level categories (largest first), up to
// p.PerCategory products, those stocked by more nearby stores first, then the
// nearest. With one: a NearbyPage of that category.
func (r *repository) NearbyProducts(ctx context.Context, p NearbyParams, staleBefore time.Time) ([]nearbyRow, error) {
	if p.Category != "" {
		return r.NearbyPage(ctx, NearbyPageParams{
			Lat: p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Category: p.Category, Limit: p.PerCategory, After: p.After,
		}, staleBefore)
	}
	query := nearbyProductsCTE + `, ranked AS (
			SELECT *,
				ROW_NUMBER() OVER (PARTITION BY category
					ORDER BY available_stores DESC, nearest_m, sort_name, catalog_key) AS rank,
				COUNT(*) OVER (PARTITION BY category) AS category_size
			FROM categorised
		), ordered AS (
			SELECT *, DENSE_RANK() OVER (ORDER BY category_size DESC, category) AS category_rank FROM ranked
		)
		SELECT catalog_key, name, brand, unit, category_path, image_url, sort_name, category, category_size
		FROM ordered
		WHERE rank <= ? AND category_rank <= ?
		ORDER BY category_rank, rank`
	args := append(nearbyArgs(p.Lat, p.Lng, p.RadiusM, staleBefore), p.PerCategory, MaxNearbyCategories)
	return r.nearbyRows(ctx, query, args)
}

// NearbyPage returns products sold within the radius, all of them or those in
// p.Category, by name after p.After: one more than p.Limit so the caller can
// tell whether another page follows. CategorySize counts every product in the
// listing, not just this page.
func (r *repository) NearbyPage(ctx context.Context, p NearbyPageParams, staleBefore time.Time) ([]nearbyRow, error) {
	args := nearbyArgs(p.Lat, p.Lng, p.RadiusM, staleBefore)
	filter := ""
	if p.Category != "" {
		filter = `WHERE category = ?`
		args = append(args, p.Category)
	}
	after := ""
	if p.After != nil {
		after = `WHERE (sort_name, catalog_key) > (?, ?)`
		args = append(args, p.After.Name, p.After.CatalogKey)
	}
	query := nearbyProductsCTE + `, listed AS (
			SELECT *, COUNT(*) OVER () AS category_size FROM categorised ` + filter + `
		)
		SELECT catalog_key, name, brand, unit, category_path, image_url, sort_name, category, category_size
		FROM listed
		` + after + `
		ORDER BY sort_name, catalog_key
		LIMIT ?`
	args = append(args, p.Limit+1)
	return r.nearbyRows(ctx, query, args)
}

// NearbyCategories returns every top-level category sold within the radius
// with its product count, largest first. p.Category is ignored.
func (r *repository) NearbyCategories(ctx context.Context, p NearbyPageParams, staleBefore time.Time) ([]CategoryCount, error) {
	query := nearbyProductsCTE + `
		SELECT category AS name, COUNT(*) AS product_count
		FROM categorised
		GROUP BY category
		ORDER BY product_count DESC, category`
	var rows []CategoryCount
	err := r.readOnly(ctx, func(tx *gorm.DB) error {
		return tx.Raw(query, nearbyArgs(p.Lat, p.Lng, p.RadiusM, staleBefore)...).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("find nearby categories: %w", err)
	}
	return rows, nil
}

func (r *repository) nearbyRows(ctx context.Context, query string, args []any) ([]nearbyRow, error) {
	var rows []nearbyRow
	err := r.readOnly(ctx, func(tx *gorm.DB) error {
		return tx.Raw(query, args...).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("find nearby products: %w", err)
	}
	return rows, nil
}

// FindProduct returns the product if at least one store currently lists it
// for customers; otherwise ErrNotFound, so unlisted products stay private.
func (r *repository) FindProduct(ctx context.Context, catalogKey string) (*Product, error) {
	var rows []Product
	err := r.readOnly(ctx, func(tx *gorm.DB) error {
		return tx.Raw(`SELECT `+productColumns+` FROM catalog_item c
			WHERE c.catalog_key = ?
				AND EXISTS (SELECT 1 FROM store_product_availability a WHERE a.catalog_key = c.catalog_key AND a.searchable)`,
			catalogKey).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("find product: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

type storeRow struct {
	ID            uuid.UUID
	Name          string
	Description   string
	Status        string
	IsOpen        bool
	VacationUntil *string
	AddressLine   string
	City          string
	Pincode       string
	Lat           float64
	Lng           float64
}

// FindStore returns a store customers may see: onboarded, not deleted, not
// deactivated, and with a saved location. A closed or on-vacation store is
// returned; the caller reports it as closed.
func (r *repository) FindStore(ctx context.Context, storeID uuid.UUID) (*storeRow, error) {
	var rows []storeRow
	err := r.readOnly(ctx, func(tx *gorm.DB) error {
		return tx.Raw(`
			SELECT s.id, s.name, s.description, s.status, s.is_open, s.vacation_until::text AS vacation_until,
				l.address_line, l.city, l.pincode, l.lat::float8 AS lat, l.lng::float8 AS lng
			FROM store s
			JOIN store_location l ON l.store_id = s.id
			WHERE s.id = ?
				AND s.deleted_at IS NULL
				AND s.onboarding_status = 'COMPLETED'
				AND s.status IN ('ACTIVE', 'VACATION')`, storeID).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("find store: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

type storeProductRow struct {
	Product
	InventoryID        uuid.UUID
	SortName           string
	StoreID            uuid.UUID
	Price              float64
	AvailabilityBucket string
	LastStockUpdateAt  time.Time
	AvailableQty       int
	Version            int64
}

const storeProductColumns = productColumns + `, a.inventory_id, lower(c.name) AS sort_name, a.store_id,
	a.price::float8 AS price, ` + effectiveBucket + ` AS availability_bucket,
	a.last_stock_update_at, a.available_qty, a.version`

// StoreProducts returns the store's searchable products after p.After, in
// (lower(name), inventory_id) order, one more than p.Limit to detect a next page.
func (r *repository) StoreProducts(
	ctx context.Context, p StoreProductsParams, q *textQuery, staleBefore time.Time,
) ([]storeProductRow, error) {
	var sqlb strings.Builder
	sqlb.WriteString(`SELECT ` + storeProductColumns + `
		FROM store_product_availability a
		JOIN catalog_item c ON c.catalog_key = a.catalog_key
		WHERE a.store_id = ? AND a.searchable`)
	args := []any{staleBefore, p.StoreID}
	if q != nil {
		sqlb.WriteString(` AND (c.search_vector @@ to_tsquery('simple', ?) OR c.search_compact LIKE ?)`)
		args = append(args, q.tsquery, q.like())
	}
	if p.After != nil {
		sqlb.WriteString(` AND (lower(c.name), a.inventory_id) > (?, ?)`)
		args = append(args, p.After.Name, p.After.InventoryID)
	}
	sqlb.WriteString(` ORDER BY lower(c.name), a.inventory_id LIMIT ?`)
	args = append(args, p.Limit+1)

	var rows []storeProductRow
	err := r.readOnly(ctx, func(tx *gorm.DB) error {
		return tx.Raw(sqlb.String(), args...).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("list store products: %w", err)
	}
	return rows, nil
}

// StoreProduct returns the product at the store if customers can find it
// there. It is read from the primary and never cached, so it is as current as
// the last committed stock change.
func (r *repository) StoreProduct(
	ctx context.Context, storeID uuid.UUID, catalogKey string, staleBefore time.Time,
) (*storeProductRow, error) {
	var rows []storeProductRow
	err := r.readOnly(ctx, func(tx *gorm.DB) error {
		return tx.Raw(`SELECT `+storeProductColumns+`
			FROM store_product_availability a
			JOIN catalog_item c ON c.catalog_key = a.catalog_key
			WHERE a.store_id = ? AND a.catalog_key = ? AND a.searchable
			LIMIT 1`, staleBefore, storeID, catalogKey).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("find store product: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

// readOnly runs fn in a read-only transaction with a statement timeout, so a
// slow search can't hold a connection the inventory writers need.
func (r *repository) readOnly(ctx context.Context, fn func(tx *gorm.DB) error) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(fmt.Sprintf(`SET LOCAL statement_timeout = %d`, queryTimeout.Milliseconds())).Error; err != nil {
			return err
		}
		return fn(tx)
	}, &sql.TxOptions{ReadOnly: true})
	var pgErr *pgconn.PgError
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &pgErr) && pgErr.Code == queryCanceled) {
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	}
	return err
}

// queryCanceled is PostgreSQL's code for a statement stopped by statement_timeout.
const queryCanceled = "57014"
