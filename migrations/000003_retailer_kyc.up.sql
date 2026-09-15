CREATE TABLE IF NOT EXISTS retailer_kyc (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    retailer_id UUID NOT NULL UNIQUE REFERENCES retailers(id),
    id_proof_url VARCHAR(255) NOT NULL,
    business_reg_url VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_retailer_kyc_status ON retailer_kyc(status);
