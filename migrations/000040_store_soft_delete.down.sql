DROP INDEX IF EXISTS idx_store_retailer_live;
ALTER TABLE store DROP COLUMN IF EXISTS deleted_at;
