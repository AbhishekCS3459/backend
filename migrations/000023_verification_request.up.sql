CREATE TABLE IF NOT EXISTS verification_request (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type VARCHAR(255) NOT NULL,
    target_id UUID NOT NULL,
    requested_info VARCHAR(255) NOT NULL,
    status VARCHAR(255) NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_verification_request_target_type ON verification_request (target_type);
CREATE INDEX IF NOT EXISTS idx_verification_request_target_id ON verification_request (target_id);
