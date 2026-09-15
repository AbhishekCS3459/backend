CREATE TABLE IF NOT EXISTS retailer_wallet (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    retailer_id UUID NOT NULL UNIQUE REFERENCES retailers(id),
    balance DECIMAL(12, 2) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
