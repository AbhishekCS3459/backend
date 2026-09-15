CREATE EXTENSION IF NOT EXISTS pg_trgm;

ALTER TABLE product ADD COLUMN IF NOT EXISTS search_vector tsvector
    GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(name, '') || ' ' || coalesce(description, ''))
    ) STORED;

CREATE INDEX IF NOT EXISTS idx_product_search ON product USING GIN (search_vector);
CREATE INDEX IF NOT EXISTS idx_product_name_trgm ON product USING GIN (name gin_trgm_ops);

-- Remove unused / wrong-type indexes from older 015 applies
DROP INDEX IF EXISTS idx_product_description;
DROP INDEX IF EXISTS idx_product_attributes;
CREATE INDEX IF NOT EXISTS idx_product_attributes ON product USING GIN (attributes);
