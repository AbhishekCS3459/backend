-- CREATE OR REPLACE VIEW can't drop a column, so rebuild the view from 000043.
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
    last_stock_update_at
FROM store_product_availability
WHERE searchable;

ALTER TABLE store_product_availability DROP COLUMN IF EXISTS price_updated_at;
ALTER TABLE inventory DROP COLUMN IF EXISTS price_updated_at;
