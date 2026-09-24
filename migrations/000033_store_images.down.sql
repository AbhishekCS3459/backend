DROP INDEX IF EXISTS idx_store_media_sort;

ALTER TABLE store_media
    DROP COLUMN IF EXISTS is_cover,
    DROP COLUMN IF EXISTS sort_order;

ALTER TABLE store_media
    ALTER COLUMN media_url TYPE VARCHAR(255);
