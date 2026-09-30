-- Fails while a hand-made variant still has a TDZ- SKU. Run
-- `make catalog-key-check` first; `make catalog-key-repair` renames them.
ALTER TABLE product_variant VALIDATE CONSTRAINT product_variant_reserved_sku;
