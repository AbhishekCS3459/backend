-- SKUs are a retailer's own labels (catalogue SKUs such as TDZ-776963 are shared
-- by every retailer that stocks the product), so they only need to be unique per
-- retailer. retailer_id is copied from product; the composite foreign key keeps
-- the copy in step with the product's owner.
ALTER TABLE product
    ADD CONSTRAINT product_id_retailer_id_key UNIQUE (id, retailer_id);

ALTER TABLE product_variant
    ADD COLUMN retailer_id UUID;

UPDATE product_variant v
SET retailer_id = p.retailer_id
FROM product p
WHERE p.id = v.product_id;

ALTER TABLE product_variant
    ALTER COLUMN retailer_id SET NOT NULL,
    ADD CONSTRAINT product_variant_product_retailer_fkey
        FOREIGN KEY (product_id, retailer_id) REFERENCES product (id, retailer_id) ON UPDATE CASCADE,
    DROP CONSTRAINT product_variant_sku_key,
    ADD CONSTRAINT product_variant_retailer_sku_key UNIQUE (retailer_id, sku);
