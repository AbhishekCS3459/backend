-- OFFLINE_SALE records units sold over the counter. Like STOCK_RECEIVED it has
-- no reason, but it always takes stock out.

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_type_check;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_type_check
    CHECK (type IN ('OPENING_BALANCE', 'STOCK_RECEIVED', 'ADJUSTMENT', 'OFFLINE_SALE'));

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_quantity;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_quantity CHECK (
    (type = 'STOCK_RECEIVED' AND quantity > 0)
    OR (type = 'OPENING_BALANCE' AND quantity >= 0)
    OR (type = 'ADJUSTMENT' AND (quantity <> 0 OR reason = 'STOCK_COUNT'))
    OR (type = 'OFFLINE_SALE' AND quantity < 0)
);
