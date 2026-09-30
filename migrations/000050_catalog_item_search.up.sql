CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Customer search matches catalog_item in tiers: whole words (full-text, name
-- weighted above brand above category), then the words run together
-- ("cocacola" finds "Coca-Cola"), then trigram similarity for typos.
ALTER TABLE catalog_item
    ADD COLUMN IF NOT EXISTS search_text TEXT
        GENERATED ALWAYS AS (lower(name || ' ' || brand || ' ' || category_path)) STORED,
    ADD COLUMN IF NOT EXISTS search_compact TEXT
        GENERATED ALWAYS AS (regexp_replace(lower(name || brand), '[^[:alnum:]]+', '', 'g')) STORED,
    ADD COLUMN IF NOT EXISTS search_vector TSVECTOR
        GENERATED ALWAYS AS (
            setweight(to_tsvector('simple', name), 'A')
            || setweight(to_tsvector('simple', brand), 'B')
            || setweight(to_tsvector('simple', category_path), 'C')
        ) STORED;

CREATE INDEX IF NOT EXISTS idx_catalog_item_search_vector ON catalog_item USING GIN (search_vector);
CREATE INDEX IF NOT EXISTS idx_catalog_item_search_text_trgm ON catalog_item USING GIN (search_text gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_catalog_item_search_compact_trgm ON catalog_item USING GIN (search_compact gin_trgm_ops);
