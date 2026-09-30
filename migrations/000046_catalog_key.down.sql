-- CREATE OR REPLACE VIEW can't drop a column, so rebuild the view from 000045.
DROP VIEW IF EXISTS store_product_search;
CREATE VIEW store_product_search AS
SELECT
    inventory_id,
    store_id,
    product_variant_id,
    location,
    price,
    CASE WHEN last_stock_update_at < NOW() - INTERVAL '14 days'
        THEN 'CONFIRM_WITH_STORE' ELSE availability_bucket END AS availability_bucket,
    last_stock_update_at,
    price_updated_at
FROM store_product_availability
WHERE searchable;

DROP INDEX IF EXISTS idx_spa_catalog_key;
DROP TRIGGER IF EXISTS store_product_availability_default_catalog_key ON store_product_availability;
DROP FUNCTION IF EXISTS store_product_availability_default_catalog_key();
ALTER TABLE store_product_availability DROP COLUMN IF EXISTS catalog_key;

ALTER TABLE product_variant
    DROP CONSTRAINT IF EXISTS product_variant_retailer_catalog_key_key,
    DROP CONSTRAINT IF EXISTS product_variant_catalog_key_format,
    DROP COLUMN IF EXISTS catalog_key;
