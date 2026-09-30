-- TDZ-<productId> SKUs name catalogue products, so a variant without a
-- catalogue key (a product made by hand) can't use one: in any case, after
-- leading whitespace, with invisible formatting characters (zero-width spaces,
-- BOMs, bidi marks) ignored. The API rejects these first; this is the backstop.
-- NOT VALID checks only new and updated rows. Existing rows are checked by
-- 000048 once cmd/catalog-key-check reports none left.
ALTER TABLE product_variant
    ADD CONSTRAINT product_variant_reserved_sku CHECK (
        catalog_key IS NOT NULL
        OR regexp_replace(sku, '[\u00AD\u200B-\u200F\u202A-\u202E\u2060-\u2064\uFEFF]', '', 'g') !~* '^\s*TDZ-'
    ) NOT VALID;
