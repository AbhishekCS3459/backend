-- Search is by distance, so a row without a point would be "searchable" yet
-- never returned. Hide such rows (with an event, like any other change), then
-- make the rule impossible to break.
WITH hidden AS (
    UPDATE store_product_availability
    SET searchable = FALSE, version = version + 1, updated_at = NOW()
    WHERE searchable AND location IS NULL
    RETURNING *
)
INSERT INTO outbox_event (aggregate_type, aggregate_id, event_type, payload)
SELECT 'store_product', inventory_id, 'InventoryChanged', jsonb_build_object(
    'schema_version', 1,
    'occurred_at', updated_at,
    'inventory_id', inventory_id,
    'store_id', store_id,
    'product_variant_id', product_variant_id,
    'price', price::text,
    'available_qty', available_qty,
    'availability_bucket', availability_bucket,
    'searchable', searchable,
    'last_stock_update_at', last_stock_update_at,
    'version', version,
    'updated_at', updated_at
)
FROM hidden;

ALTER TABLE store_product_availability
    ADD CONSTRAINT store_product_availability_searchable_location
    CHECK (NOT searchable OR location IS NOT NULL);

-- What customers may see; customer search reads this, never the table.
-- Stock nobody has confirmed for 14 days shows as CONFIRM_WITH_STORE, computed
-- here at read time so no job has to downgrade rows and nothing can drift.
-- The exact count stays internal: customers get the bucket only.
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

-- A failed delivery is retried after next_attempt_at, waiting longer each time.
-- After the publisher's attempt limit the event is parked with failed_at, so
-- it can't block the events behind it. To retry parked events:
--   UPDATE outbox_event SET failed_at = NULL, attempts = 0, next_attempt_at = NULL WHERE ...;
ALTER TABLE outbox_event
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS failed_at TIMESTAMPTZ,
    ADD CONSTRAINT outbox_event_settled_once CHECK (published_at IS NULL OR failed_at IS NULL);

DROP INDEX IF EXISTS idx_outbox_event_pending;
CREATE INDEX IF NOT EXISTS idx_outbox_event_pending ON outbox_event (seq)
    WHERE published_at IS NULL AND failed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_outbox_event_failed ON outbox_event (failed_at) WHERE failed_at IS NOT NULL;
