CREATE TABLE IF NOT EXISTS store (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    retailer_id UUID NOT NULL REFERENCES retailers(id),
    category_id UUID NOT NULL REFERENCES category(id),
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'ACTIVE'
        CHECK (status IN ('ACTIVE', 'INACTIVE', 'VACATION')),
    is_open BOOLEAN NOT NULL DEFAULT TRUE,
    vacation_until DATE,
    kyb_status VARCHAR(50) NOT NULL DEFAULT 'PENDING'
        CHECK (kyb_status IN ('PENDING', 'APPROVED', 'REJECTED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
