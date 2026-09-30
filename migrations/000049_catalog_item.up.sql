-- One canonical product per catalog_key, read by customer search instead of
-- each retailer's own copy of the product. It holds only what describes the
-- product; price, stock and stores stay in store_product_availability.
--   todayz:<productId>  a catalogue product, from the MongoDB catalogue
--   variant:<id>        a product a retailer made by hand, from their product
CREATE TABLE IF NOT EXISTS catalog_item (
    catalog_key       TEXT PRIMARY KEY,
    source            TEXT NOT NULL,
    source_product_id TEXT NOT NULL,
    name              TEXT NOT NULL,
    brand             TEXT NOT NULL DEFAULT '',
    unit              TEXT NOT NULL DEFAULT '',
    category_path     TEXT NOT NULL DEFAULT '',
    image_url         TEXT NOT NULL DEFAULT '',
    -- Not populated or enforced yet.
    mrp               NUMERIC(12, 2),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT catalog_item_key_matches_source CHECK (catalog_key = source || ':' || source_product_id),
    CONSTRAINT catalog_item_source_format CHECK (source ~ '^[a-z][a-z0-9]*$'),
    CONSTRAINT catalog_item_mrp_positive CHECK (mrp IS NULL OR mrp > 0)
);

-- Provisional rows from the retailers' copies, so search works as soon as this
-- is deployed. cmd/catalog-item-sync then replaces catalogue products with the
-- catalogue's values. Where several retailers stock a catalogue product, the
-- earliest-created variant's copy is used.
INSERT INTO catalog_item (catalog_key, source, source_product_id, name, brand, unit, category_path, image_url)
SELECT DISTINCT ON (key.catalog_key)
    key.catalog_key,
    split_part(key.catalog_key, ':', 1),
    substring(key.catalog_key FROM position(':' IN key.catalog_key) + 1),
    p.name,
    COALESCE(b.name, ''),
    COALESCE(p.attributes->>'unit', ''),
    COALESCE(c.name, ''),
    COALESCE((
        SELECT pi.image_url FROM product_image pi
        WHERE pi.product_id = p.id
        ORDER BY pi.sort_order
        LIMIT 1
    ), '')
FROM product_variant v
CROSS JOIN LATERAL (SELECT COALESCE(v.catalog_key, 'variant:' || v.id::text) AS catalog_key) key
JOIN product p ON p.id = v.product_id
LEFT JOIN brand b ON b.id = p.brand_id
LEFT JOIN category c ON c.id = p.category_id
ORDER BY key.catalog_key, v.created_at, v.id
ON CONFLICT (catalog_key) DO NOTHING;
