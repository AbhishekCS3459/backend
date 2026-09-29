-- When the store last set or confirmed its price, so search can flag prices
-- nobody has looked at in a while.
ALTER TABLE inventory ADD COLUMN IF NOT EXISTS price_updated_at TIMESTAMPTZ;

-- Nobody knows when existing prices were set. listed_at is the oldest time the
-- store can have chosen its price, so old prices look old rather than fresh.
UPDATE inventory SET price_updated_at = listed_at WHERE price_updated_at IS NULL;

-- The default covers rows inserted by a backend that doesn't know the column.
ALTER TABLE inventory
    ALTER COLUMN price_updated_at SET DEFAULT NOW(),
    ALTER COLUMN price_updated_at SET NOT NULL;

ALTER TABLE store_product_availability ADD COLUMN IF NOT EXISTS price_updated_at TIMESTAMPTZ;
UPDATE store_product_availability a
SET price_updated_at = i.price_updated_at
FROM inventory i
WHERE i.id = a.inventory_id AND a.price_updated_at IS NULL;
ALTER TABLE store_product_availability ALTER COLUMN price_updated_at SET NOT NULL;

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
    price_updated_at
FROM store_product_availability
WHERE searchable;
