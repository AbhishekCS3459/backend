-- Restores the legacy order tables. Order data is lost: orders, their history
-- and ledger entries are deleted, and reserved stock is returned first so the
-- legacy inventory constraints still hold.

UPDATE inventory i
SET reserved_quantity = i.reserved_quantity - r.held
FROM (
    SELECT inventory_id, SUM(quantity) AS held FROM inventory_reservation
    WHERE status = 'ACTIVE' GROUP BY inventory_id
) r
WHERE r.inventory_id = i.id;

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_order;
DELETE FROM inventory_transaction WHERE type IN ('ORDER_RESERVED', 'ORDER_RELEASED', 'ORDER_PICKUP');

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_unit_price;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_unit_price CHECK (
    unit_price IS NULL OR (type = 'OFFLINE_SALE' AND unit_price > 0)
);

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_quantity;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_quantity CHECK (
    (type = 'STOCK_RECEIVED' AND quantity > 0)
    OR (type = 'OPENING_BALANCE' AND quantity >= 0)
    OR (type = 'ADJUSTMENT' AND (quantity <> 0 OR reason = 'STOCK_COUNT'))
    OR (type = 'OFFLINE_SALE' AND quantity < 0)
);
ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_type_check;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_type_check
    CHECK (type IN ('OPENING_BALANCE', 'STOCK_RECEIVED', 'ADJUSTMENT', 'OFFLINE_SALE'));
DROP INDEX IF EXISTS idx_inventory_transaction_order;
ALTER TABLE inventory_transaction DROP COLUMN IF EXISTS order_id;

DROP TABLE IF EXISTS order_idempotency;
DROP TABLE IF EXISTS payment_webhook_event;
DROP TABLE IF EXISTS payment;
DROP TABLE IF EXISTS inventory_reservation;

UPDATE support_ticket SET order_id = NULL WHERE order_id IS NOT NULL;
DELETE FROM order_status_history;
DELETE FROM order_item;
DELETE FROM orders;

DROP INDEX IF EXISTS idx_order_status_history_order;
ALTER TABLE order_status_history
    DROP CONSTRAINT IF EXISTS order_status_history_actor_check,
    DROP COLUMN reason,
    DROP COLUMN actor,
    DROP COLUMN from_status,
    DROP COLUMN seq,
    ALTER COLUMN changed_at TYPE TIMESTAMP USING changed_at AT TIME ZONE 'UTC',
    ALTER COLUMN changed_at SET DEFAULT CURRENT_TIMESTAMP,
    ALTER COLUMN changed_by SET NOT NULL,
    ALTER COLUMN to_status TYPE VARCHAR(255);
ALTER TABLE order_status_history RENAME COLUMN to_status TO status;
CREATE INDEX IF NOT EXISTS idx_order_status_history_order_id ON order_status_history (order_id);
CREATE INDEX IF NOT EXISTS idx_order_status_history_status ON order_status_history (status);
CREATE INDEX IF NOT EXISTS idx_order_status_history_changed_by ON order_status_history (changed_by);
CREATE INDEX IF NOT EXISTS idx_order_status_history_changed_at ON order_status_history (changed_at);

ALTER TABLE order_item
    DROP CONSTRAINT IF EXISTS order_item_order_inventory_key,
    DROP CONSTRAINT IF EXISTS order_item_price_check,
    DROP COLUMN line_total_paise,
    DROP COLUMN unit_price_paise,
    DROP COLUMN unit,
    DROP COLUMN name,
    DROP COLUMN catalog_key,
    DROP COLUMN inventory_id;
ALTER TABLE order_item
    ADD COLUMN price_at_order_time DECIMAL(12, 2) NOT NULL,
    ADD COLUMN substitution_status VARCHAR(255) NOT NULL,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_order_item_product_variant_id ON order_item (product_variant_id);

DROP INDEX IF EXISTS idx_orders_due;
DROP INDEX IF EXISTS idx_orders_customer;
DROP INDEX IF EXISTS idx_orders_store_queue;
ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_expiry_check,
    DROP CONSTRAINT IF EXISTS orders_pickup_attempts_check,
    DROP CONSTRAINT IF EXISTS orders_total_check,
    DROP CONSTRAINT IF EXISTS orders_payment_status_check,
    DROP CONSTRAINT IF EXISTS orders_payment_mode_check,
    DROP CONSTRAINT IF EXISTS orders_status_check,
    DROP CONSTRAINT IF EXISTS orders_code_key,
    DROP COLUMN version,
    DROP COLUMN closed_reason,
    DROP COLUMN completed_at,
    DROP COLUMN ready_at,
    DROP COLUMN accepted_at,
    DROP COLUMN pickup_attempts,
    DROP COLUMN expires_at,
    DROP COLUMN total_paise,
    DROP COLUMN payment_status,
    DROP COLUMN payment_mode,
    DROP COLUMN code,
    ALTER COLUMN status TYPE VARCHAR(255);
ALTER TABLE orders
    ADD COLUMN delivery_mode VARCHAR(255) NOT NULL,
    ADD COLUMN total_amount DECIMAL(12, 2) NOT NULL,
    ADD COLUMN payment_status VARCHAR(255) NOT NULL;
ALTER TABLE orders RENAME COLUMN customer_id TO consumer_id;
CREATE INDEX IF NOT EXISTS idx_order_consumer_id ON orders (consumer_id);
CREATE INDEX IF NOT EXISTS idx_order_store_id ON orders (store_id);
CREATE INDEX IF NOT EXISTS idx_order_status ON orders (status);
CREATE INDEX IF NOT EXISTS idx_order_payment_status ON orders (payment_status);
