-- Groups one catalogue product across retailers. Variants are per retailer, so
-- two retailers stocking the same item have two variants; search groups them
-- by this key. Format <source>:<source_product_id>, e.g. todayz:776963. NULL
-- for products a retailer created by hand.
ALTER TABLE product_variant ADD COLUMN IF NOT EXISTS catalog_key TEXT;

-- Products added from the catalogue carry its Todayz SKU, TDZ-<productId>.
-- SKUs are unique per retailer but case-sensitive, so should a retailer have
-- both TDZ-1 and tdz-1, only the older variant gets the key.
-- cmd/catalog-key-check clears keys whose product isn't in the catalogue.
UPDATE product_variant v
SET catalog_key = k.catalog_key
FROM (
    SELECT DISTINCT ON (retailer_id, lower(sku)) id, 'todayz:' || substring(sku FROM 5) AS catalog_key
    FROM product_variant
    WHERE catalog_key IS NULL AND sku ~* '^TDZ-[0-9]{1,20}$'
    ORDER BY retailer_id, lower(sku), created_at, id
) k
WHERE v.id = k.id;

-- "variant:" is reserved for the search table's key of uncatalogued products.
ALTER TABLE product_variant
    ADD CONSTRAINT product_variant_catalog_key_format
        CHECK (catalog_key ~ '^[a-z][a-z0-9]*:[A-Za-z0-9._-]+$' AND catalog_key NOT LIKE 'variant:%'),
    ADD CONSTRAINT product_variant_retailer_catalog_key_key UNIQUE (retailer_id, catalog_key);

-- Every search row has a key: the catalogue key, or variant:<id> so a
-- hand-made product forms a group of its own.
ALTER TABLE store_product_availability ADD COLUMN IF NOT EXISTS catalog_key TEXT;

WITH keyed AS (
    UPDATE store_product_availability a
    SET catalog_key = COALESCE(v.catalog_key, 'variant:' || v.id::text),
        version = a.version + 1,
        updated_at = NOW()
    FROM product_variant v
    WHERE v.id = a.product_variant_id AND a.catalog_key IS NULL
    RETURNING a.*
)
INSERT INTO outbox_event (aggregate_type, aggregate_id, event_type, payload)
SELECT 'store_product', inventory_id, 'InventoryChanged', jsonb_build_object(
    'schema_version', 2,
    'occurred_at', updated_at,
    'inventory_id', inventory_id,
    'store_id', store_id,
    'product_variant_id', product_variant_id,
    'catalog_key', catalog_key,
    'price', price::text,
    'price_updated_at', price_updated_at,
    'available_qty', available_qty,
    'availability_bucket', availability_bucket,
    'searchable', searchable,
    'last_stock_update_at', last_stock_update_at,
    'version', version,
    'updated_at', updated_at
)
FROM keyed;

-- A backend that doesn't know the column yet (mid-deploy) inserts rows without it.
CREATE OR REPLACE FUNCTION store_product_availability_default_catalog_key() RETURNS trigger AS $$
BEGIN
    IF NEW.catalog_key IS NULL THEN
        SELECT COALESCE(catalog_key, 'variant:' || id::text) INTO NEW.catalog_key
        FROM product_variant WHERE id = NEW.product_variant_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS store_product_availability_default_catalog_key ON store_product_availability;
CREATE TRIGGER store_product_availability_default_catalog_key
    BEFORE INSERT ON store_product_availability
    FOR EACH ROW EXECUTE FUNCTION store_product_availability_default_catalog_key();

ALTER TABLE store_product_availability ALTER COLUMN catalog_key SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_spa_catalog_key ON store_product_availability (catalog_key) WHERE searchable;

CREATE OR REPLACE VIEW store_product_search AS
SELECT
    inventory_id,
    store_id,
    product_variant_id,
    location,
    price,
    CASE WHEN last_stock_update_at < NOW() - INTERVAL '14 days'
        THEN 'CONFIRM_WITH_STORE' ELSE availability_bucket END AS availability_bucket,
    last_stock_update_at,
    price_updated_at,
    catalog_key
FROM store_product_availability
WHERE searchable;
