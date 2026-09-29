-- Deleted stores are kept so orders, ledger entries and stock history stay intact.
ALTER TABLE store ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_store_retailer_live ON store (retailer_id) WHERE deleted_at IS NULL;
