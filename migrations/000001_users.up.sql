CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    phone VARCHAR(255) NOT NULL UNIQUE,
    is_phone_verified BOOLEAN NOT NULL DEFAULT FALSE,

    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,

    user_type VARCHAR(50) NOT NULL DEFAULT 'USER'
        CHECK (user_type IN ('USER', 'RETAILER', 'STAFF', 'ADMIN')),

    status VARCHAR(50) NOT NULL DEFAULT 'ACTIVE'
        CHECK (status IN ('ACTIVE', 'INACTIVE')),

    password_reset_token VARCHAR(255),
    password_reset_expires_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_user_type
ON users(user_type);

CREATE INDEX idx_users_password_reset_token
ON users(password_reset_token)
WHERE password_reset_token IS NOT NULL;