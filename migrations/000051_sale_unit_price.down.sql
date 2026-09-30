DROP INDEX IF EXISTS idx_inventory_transaction_store_created;
ALTER TABLE inventory_transaction DROP CONSTRAINT IF EXISTS inventory_transaction_unit_price;
ALTER TABLE inventory_transaction DROP COLUMN IF EXISTS unit_price;
