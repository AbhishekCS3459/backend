-- The store's price when units were sold, so sales reports don't change when
-- the price does. Sales recorded before this column existed have NULL; reports
-- fall back to the current price for them and say the total is estimated.
ALTER TABLE inventory_transaction ADD COLUMN IF NOT EXISTS unit_price DECIMAL(12, 2);

ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_unit_price;
ALTER TABLE inventory_transaction ADD CONSTRAINT inventory_transaction_unit_price CHECK (
    unit_price IS NULL OR (type = 'OFFLINE_SALE' AND unit_price > 0)
);

-- Store reports read a period of one store's history.
CREATE INDEX IF NOT EXISTS idx_inventory_transaction_store_created
    ON inventory_transaction (store_id, created_at);
