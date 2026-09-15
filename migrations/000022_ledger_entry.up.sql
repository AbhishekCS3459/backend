CREATE TABLE IF NOT EXISTS ledger_entry (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    retailer_id UUID NOT NULL REFERENCES retailers(id),
    store_id UUID NOT NULL REFERENCES store(id),
    amount DECIMAL(12, 2) NOT NULL,
    type VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ledger_entry_retailer_id ON ledger_entry (retailer_id);
CREATE INDEX IF NOT EXISTS idx_ledger_entry_store_id ON ledger_entry (store_id);
CREATE INDEX IF NOT EXISTS idx_ledger_entry_type ON ledger_entry (type);
