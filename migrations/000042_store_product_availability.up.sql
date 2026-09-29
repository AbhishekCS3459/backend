-- Read model for customer search: one row per store product (inventory row),
-- written only by internal/availability in the same transaction as the change
-- that caused it. Rows are never deleted while the inventory row exists;
-- searchable = false hides unlisted products and closed or deleted stores.
CREATE TABLE IF NOT EXISTS store_product_availability (
    inventory_id UUID PRIMARY KEY REFERENCES inventory(id) ON DELETE CASCADE,
    store_id UUID NOT NULL,
    product_variant_id UUID NOT NULL,
    -- Copied from store_location; NULL until the store saves its location.
    location geography(Point, 4326),
    price DECIMAL(12, 2) NOT NULL,
    available_qty INTEGER NOT NULL CHECK (available_qty >= 0),
    availability_bucket VARCHAR(24) NOT NULL
        CHECK (availability_bucket IN ('IN_STOCK', 'LOW', 'OUT', 'CONFIRM_WITH_STORE')),
    searchable BOOLEAN NOT NULL,
    -- Latest inventory_transaction for the product (a count confirms stock even
    -- when nothing changed), or when it was listed if it has none.
    last_stock_update_at TIMESTAMPTZ NOT NULL,
    -- Increases by one on every change so consumers can drop out-of-order events.
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_spa_location ON store_product_availability USING GIST (location) WHERE searchable;
CREATE INDEX IF NOT EXISTS idx_spa_variant ON store_product_availability (product_variant_id) WHERE searchable;
CREATE INDEX IF NOT EXISTS idx_spa_store ON store_product_availability (store_id);

-- Transactional outbox: events are inserted in the same transaction as the
-- change and published afterwards. seq follows insert order, not commit order,
-- so consumers must order by the version in the payload.
CREATE TABLE IF NOT EXISTS outbox_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id UUID NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT
);

CREATE INDEX IF NOT EXISTS idx_outbox_event_pending ON outbox_event (seq) WHERE published_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_outbox_event_published ON outbox_event (published_at) WHERE published_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_outbox_event_aggregate ON outbox_event (aggregate_type, aggregate_id, seq);
