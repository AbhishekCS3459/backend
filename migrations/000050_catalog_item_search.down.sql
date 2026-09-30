-- pg_trgm is left installed: product.name has used it since before 000050.
DROP INDEX IF EXISTS idx_catalog_item_search_compact_trgm;
DROP INDEX IF EXISTS idx_catalog_item_search_text_trgm;
DROP INDEX IF EXISTS idx_catalog_item_search_vector;
ALTER TABLE catalog_item
    DROP COLUMN IF EXISTS search_vector,
    DROP COLUMN IF EXISTS search_compact,
    DROP COLUMN IF EXISTS search_text;
