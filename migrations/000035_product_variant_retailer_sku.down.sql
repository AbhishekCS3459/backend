-- Fails while two retailers share a SKU; resolve those rows before rolling back.
ALTER TABLE product_variant
    DROP CONSTRAINT IF EXISTS product_variant_retailer_sku_key,
    DROP CONSTRAINT IF EXISTS product_variant_product_retailer_fkey,
    ADD CONSTRAINT product_variant_sku_key UNIQUE (sku),
    DROP COLUMN IF EXISTS retailer_id;

ALTER TABLE product
    DROP CONSTRAINT IF EXISTS product_id_retailer_id_key;
