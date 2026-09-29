DROP INDEX IF EXISTS idx_outbox_event_failed;
DROP INDEX IF EXISTS idx_outbox_event_pending;
ALTER TABLE outbox_event
    DROP CONSTRAINT IF EXISTS outbox_event_settled_once,
    DROP COLUMN IF EXISTS failed_at,
    DROP COLUMN IF EXISTS next_attempt_at;
CREATE INDEX IF NOT EXISTS idx_outbox_event_pending ON outbox_event (seq) WHERE published_at IS NULL;

DROP VIEW IF EXISTS store_product_search;
ALTER TABLE store_product_availability DROP CONSTRAINT IF EXISTS store_product_availability_searchable_location;
