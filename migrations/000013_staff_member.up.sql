CREATE TABLE IF NOT EXISTS staff_member (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    store_id UUID NOT NULL REFERENCES store(id),
    role VARCHAR(255) NOT NULL,
    invited_by UUID REFERENCES users(id),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT staff_member_user_store_key UNIQUE (user_id, store_id)
);
CREATE INDEX IF NOT EXISTS idx_staff_member_store_id ON staff_member (store_id);
CREATE INDEX IF NOT EXISTS idx_staff_member_role ON staff_member (role);
