-- Make product catalog retailer-scoped; store listings live in inventory.
ALTER TABLE product
    ADD COLUMN IF NOT EXISTS retailer_id UUID REFERENCES retailers(id);

UPDATE product p
SET retailer_id = s.retailer_id
FROM store s
WHERE p.store_id = s.id
  AND p.retailer_id IS NULL;

ALTER TABLE product
    ALTER COLUMN store_id DROP NOT NULL;

ALTER TABLE inventory
    ADD COLUMN IF NOT EXISTS is_available BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE product_image
    ALTER COLUMN image_url TYPE TEXT;
