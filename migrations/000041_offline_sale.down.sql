-- The ledger is append-only history, so sales are never rewritten to fit the
-- older constraints; roll back only before any sale has been recorded.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM inventory_transaction WHERE type = 'OFFLINE_SALE') THEN
        RAISE EXCEPTION 'cannot roll back 000041: offline sales are recorded in inventory_transaction';
    END IF;
END $$;

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_quantity;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_quantity CHECK (
    (type = 'STOCK_RECEIVED' AND quantity > 0)
    OR (type = 'OPENING_BALANCE' AND quantity >= 0)
    OR (type = 'ADJUSTMENT' AND (quantity <> 0 OR reason = 'STOCK_COUNT'))
);

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_type_check;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_type_check
    CHECK (type IN ('OPENING_BALANCE', 'STOCK_RECEIVED', 'ADJUSTMENT'));
