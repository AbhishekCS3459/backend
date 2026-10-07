-- Online orders with stock reservations. Reshapes the unused tables from
-- migrations 000018-000020 (support_ticket.order_id keeps pointing at orders)
-- and adds reservations, payments and their idempotency records.
--
-- Money is integer paise. A reservation holds units in inventory.reserved_quantity
-- from placing the order until pickup (consumed) or until the order ends (released).

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM orders) OR EXISTS (SELECT 1 FROM order_item)
        OR EXISTS (SELECT 1 FROM order_status_history) THEN
        RAISE EXCEPTION 'orders tables hold rows; this migration only reshapes empty legacy tables';
    END IF;
END $$;

-- orders
DROP INDEX IF EXISTS idx_order_consumer_id;
DROP INDEX IF EXISTS idx_order_status;
DROP INDEX IF EXISTS idx_order_payment_status;
DROP INDEX IF EXISTS idx_order_store_id;

ALTER TABLE orders RENAME COLUMN consumer_id TO customer_id;
ALTER TABLE orders
    DROP COLUMN delivery_mode,
    DROP COLUMN total_amount,
    DROP COLUMN payment_status;

ALTER TABLE orders
    ALTER COLUMN status TYPE VARCHAR(24),
    -- Short code customers and retailers read out, e.g. "TZ-4K7Q9M".
    ADD COLUMN code VARCHAR(16) NOT NULL,
    ADD COLUMN payment_mode VARCHAR(16) NOT NULL,
    ADD COLUMN payment_status VARCHAR(16) NOT NULL,
    ADD COLUMN total_paise BIGINT NOT NULL,
    -- Deadline of the current status: paying, the store accepting, or pickup.
    ADD COLUMN expires_at TIMESTAMPTZ,
    ADD COLUMN pickup_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN accepted_at TIMESTAMPTZ,
    ADD COLUMN ready_at TIMESTAMPTZ,
    ADD COLUMN completed_at TIMESTAMPTZ,
    ADD COLUMN closed_reason VARCHAR(500),
    ADD COLUMN version INTEGER NOT NULL DEFAULT 1,
    ADD CONSTRAINT orders_code_key UNIQUE (code),
    ADD CONSTRAINT orders_status_check CHECK (status IN (
        'PENDING_PAYMENT', 'PLACED', 'ACCEPTED', 'READY',
        'COMPLETED', 'CANCELLED', 'REJECTED', 'EXPIRED', 'NO_SHOW')),
    ADD CONSTRAINT orders_payment_mode_check CHECK (payment_mode IN ('ONLINE', 'PAY_AT_STORE')),
    ADD CONSTRAINT orders_payment_status_check CHECK (payment_status IN (
        'UNPAID', 'PAID', 'REFUND_PENDING', 'REFUNDED')),
    ADD CONSTRAINT orders_total_check CHECK (total_paise > 0),
    ADD CONSTRAINT orders_pickup_attempts_check CHECK (pickup_attempts >= 0),
    ADD CONSTRAINT orders_expiry_check CHECK (
        (status IN ('PENDING_PAYMENT', 'PLACED', 'READY')) = (expires_at IS NOT NULL));

CREATE INDEX IF NOT EXISTS idx_orders_store_queue ON orders (store_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders (customer_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_orders_due ON orders (expires_at) WHERE expires_at IS NOT NULL;

-- order_item: one line per product, with the name and price the customer agreed to.
DROP INDEX IF EXISTS idx_order_item_product_variant_id;
ALTER TABLE order_item
    DROP COLUMN price_at_order_time,
    DROP COLUMN substitution_status,
    DROP COLUMN updated_at;
ALTER TABLE order_item
    ADD COLUMN inventory_id UUID NOT NULL REFERENCES inventory(id),
    ADD COLUMN catalog_key TEXT NOT NULL,
    ADD COLUMN name VARCHAR(500) NOT NULL,
    ADD COLUMN unit VARCHAR(100) NOT NULL DEFAULT '',
    ADD COLUMN unit_price_paise BIGINT NOT NULL,
    ADD COLUMN line_total_paise BIGINT NOT NULL,
    ADD CONSTRAINT order_item_price_check CHECK (unit_price_paise > 0 AND line_total_paise = unit_price_paise * quantity),
    ADD CONSTRAINT order_item_order_inventory_key UNIQUE (order_id, inventory_id);

-- order_status_history: every status change, who made it and why.
DROP INDEX IF EXISTS idx_order_status_history_status;
DROP INDEX IF EXISTS idx_order_status_history_changed_by;
DROP INDEX IF EXISTS idx_order_status_history_changed_at;
DROP INDEX IF EXISTS idx_order_status_history_order_id;
ALTER TABLE order_status_history RENAME COLUMN status TO to_status;
ALTER TABLE order_status_history
    ALTER COLUMN to_status TYPE VARCHAR(24),
    ALTER COLUMN changed_by DROP NOT NULL,
    ALTER COLUMN changed_at TYPE TIMESTAMPTZ USING changed_at AT TIME ZONE 'UTC',
    ALTER COLUMN changed_at SET DEFAULT NOW(),
    ADD COLUMN seq BIGINT GENERATED ALWAYS AS IDENTITY,
    ADD COLUMN from_status VARCHAR(24),
    -- CUSTOMER, RETAILER, SYSTEM (deadlines) or PAYMENT (provider events).
    ADD COLUMN actor VARCHAR(16) NOT NULL,
    ADD COLUMN reason VARCHAR(500),
    ADD CONSTRAINT order_status_history_actor_check CHECK (actor IN ('CUSTOMER', 'RETAILER', 'SYSTEM', 'PAYMENT'));
CREATE INDEX IF NOT EXISTS idx_order_status_history_order ON order_status_history (order_id, seq);

-- inventory_reservation: who holds which reserved units. The ACTIVE rows of an
-- inventory row always sum to its reserved_quantity.
CREATE TABLE IF NOT EXISTS inventory_reservation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id),
    order_item_id UUID NOT NULL REFERENCES order_item(id),
    inventory_id UUID NOT NULL REFERENCES inventory(id),
    store_id UUID NOT NULL REFERENCES store(id),
    product_variant_id UUID NOT NULL REFERENCES product_variant(id),
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    status VARCHAR(16) NOT NULL CHECK (status IN ('ACTIVE', 'CONSUMED', 'RELEASED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ,
    CONSTRAINT inventory_reservation_closed CHECK ((status = 'ACTIVE') = (closed_at IS NULL))
);
CREATE INDEX IF NOT EXISTS idx_inventory_reservation_order ON inventory_reservation (order_id);
CREATE INDEX IF NOT EXISTS idx_inventory_reservation_active ON inventory_reservation (inventory_id) WHERE status = 'ACTIVE';
-- A line holds at most one live reservation; a late payment may reserve it again after a release.
CREATE UNIQUE INDEX IF NOT EXISTS idx_inventory_reservation_live_item
    ON inventory_reservation (order_item_id) WHERE status = 'ACTIVE';

-- payment: one attempt to collect an order's total through a provider.
CREATE TABLE IF NOT EXISTS payment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id),
    provider VARCHAR(32) NOT NULL,
    provider_payment_id VARCHAR(255) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('CREATED', 'SUCCEEDED', 'FAILED', 'REFUND_PENDING', 'REFUNDED')),
    amount_paise BIGINT NOT NULL CHECK (amount_paise > 0),
    refund_attempts INTEGER NOT NULL DEFAULT 0,
    next_refund_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT payment_provider_id_key UNIQUE (provider, provider_payment_id)
);
CREATE INDEX IF NOT EXISTS idx_payment_order ON payment (order_id, created_at DESC);
-- An order has at most one attempt waiting for the customer.
CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_open ON payment (order_id) WHERE status = 'CREATED';
CREATE INDEX IF NOT EXISTS idx_payment_refund_due ON payment (next_refund_at) WHERE status = 'REFUND_PENDING';

-- Provider webhooks are delivered at least once: the first copy of an event is
-- processed, later copies are acknowledged and ignored.
CREATE TABLE IF NOT EXISTS payment_webhook_event (
    provider VARCHAR(32) NOT NULL,
    event_id VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, event_id)
);

-- The first successful response per customer and key, replayed on retries.
CREATE TABLE IF NOT EXISTS order_idempotency (
    customer_id UUID NOT NULL REFERENCES users(id),
    idempotency_key VARCHAR(100) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (customer_id, idempotency_key)
);

-- Ledger entries for orders. ORDER_RESERVED and ORDER_RELEASED move only
-- reserved_quantity; ORDER_PICKUP takes the reserved units out of stock.
ALTER TABLE inventory_transaction ADD COLUMN IF NOT EXISTS order_id UUID REFERENCES orders(id);
CREATE INDEX IF NOT EXISTS idx_inventory_transaction_order ON inventory_transaction (order_id) WHERE order_id IS NOT NULL;

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_type_check;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_type_check
    CHECK (type IN ('OPENING_BALANCE', 'STOCK_RECEIVED', 'ADJUSTMENT', 'OFFLINE_SALE',
        'ORDER_RESERVED', 'ORDER_RELEASED', 'ORDER_PICKUP'));

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_quantity;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_quantity CHECK (
    (type = 'STOCK_RECEIVED' AND quantity > 0)
    OR (type = 'OPENING_BALANCE' AND quantity >= 0)
    OR (type = 'ADJUSTMENT' AND (quantity <> 0 OR reason = 'STOCK_COUNT'))
    OR (type = 'OFFLINE_SALE' AND quantity < 0)
    OR (type = 'ORDER_RESERVED' AND quantity = 0 AND after_reserved > before_reserved)
    OR (type = 'ORDER_RELEASED' AND quantity = 0 AND after_reserved < before_reserved)
    OR (type = 'ORDER_PICKUP' AND quantity < 0 AND after_reserved - before_reserved = quantity)
);

ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_order
    CHECK ((type IN ('ORDER_RESERVED', 'ORDER_RELEASED', 'ORDER_PICKUP')) = (order_id IS NOT NULL));

-- A pickup is a sale at the order's price, which is always known.
ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_unit_price;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_unit_price CHECK (
    (unit_price IS NULL AND type <> 'ORDER_PICKUP')
    OR (type IN ('OFFLINE_SALE', 'ORDER_PICKUP') AND unit_price > 0)
);
