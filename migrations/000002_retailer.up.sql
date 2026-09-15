CREATE TABLE IF NOT EXISTS retailers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id),
    legal_name VARCHAR(255) NOT NULL,
    owner_name VARCHAR(255) NOT NULL,
    kyc_status VARCHAR(50) NOT NULL DEFAULT 'PENDING'
        CHECK (kyc_status IN ('PENDING', 'APPROVED', 'REJECTED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_retailers_user_id ON retailers(user_id);
CREATE INDEX IF NOT EXISTS idx_retailers_legal_name ON retailers(legal_name);
CREATE INDEX IF NOT EXISTS idx_retailers_owner_name ON retailers(owner_name);
CREATE INDEX IF NOT EXISTS idx_retailers_kyc_status ON retailers(kyc_status);