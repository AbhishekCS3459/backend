CREATE TABLE IF NOT EXISTS settlement (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    retailer_id UUID NOT NULL REFERENCES retailers(id),
    amount DECIMAL(12, 2) NOT NULL,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT settlement_period_valid CHECK (period_end >= period_start)
);

CREATE INDEX IF NOT EXISTS idx_settlement_retailer_id ON settlement(retailer_id);
CREATE INDEX IF NOT EXISTS idx_settlement_status ON settlement(status);
