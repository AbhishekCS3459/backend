-- PostgreSQL can't mark a constraint unvalidated, so it is recreated NOT VALID as in 000047.
ALTER TABLE product_variant DROP CONSTRAINT IF EXISTS product_variant_reserved_sku;
ALTER TABLE product_variant
    ADD CONSTRAINT product_variant_reserved_sku CHECK (
        catalog_key IS NOT NULL
        OR regexp_replace(sku, '[\u00AD\u200B-\u200F\u202A-\u202E\u2060-\u2064\uFEFF]', '', 'g') !~* '^\s*TDZ-'
    ) NOT VALID;
