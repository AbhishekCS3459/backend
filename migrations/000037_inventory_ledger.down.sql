DROP TABLE IF EXISTS inventory_idempotency;
DROP TABLE IF EXISTS inventory_transaction;

DROP TRIGGER IF EXISTS inventory_sync_legacy_stock ON inventory;
DROP FUNCTION IF EXISTS inventory_sync_legacy_stock();

-- The previous schema has no unlisted state; unlisted rows would reappear as listed.
DELETE FROM inventory WHERE unlisted_at IS NOT NULL;

DROP INDEX IF EXISTS idx_inventory_store_listed;
ALTER TABLE inventory DROP CONSTRAINT IF EXISTS inventory_stock_check;
ALTER TABLE inventory
    DROP COLUMN IF EXISTS unlisted_at,
    DROP COLUMN IF EXISTS listed_at,
    DROP COLUMN IF EXISTS reserved_quantity,
    DROP COLUMN IF EXISTS on_hand_quantity;
