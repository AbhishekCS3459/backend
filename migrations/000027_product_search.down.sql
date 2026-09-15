DROP INDEX IF EXISTS idx_product_name_trgm;
DROP INDEX IF EXISTS idx_product_search;
ALTER TABLE product DROP COLUMN IF EXISTS search_vector;
