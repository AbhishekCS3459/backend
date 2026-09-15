    -- STORE_MEDIA {
    --     uuid id PK
    --     uuid store_id FK
    --     string media_url
    --     string type
    -- }

CREATE TABLE IF NOT EXISTS store_media (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    store_id UUID NOT NULL REFERENCES store(id),
    media_url VARCHAR(255) NOT NULL,
    type VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_store_media_store_id ON store_media (store_id);
CREATE INDEX IF NOT EXISTS idx_store_media_type ON store_media (type);