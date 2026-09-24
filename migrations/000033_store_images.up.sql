ALTER TABLE store_media
    ALTER COLUMN media_url TYPE TEXT;

ALTER TABLE store_media
    ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS is_cover BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_store_media_sort ON store_media (store_id, sort_order);
