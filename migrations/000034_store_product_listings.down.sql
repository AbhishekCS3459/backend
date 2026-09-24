ALTER TABLE product_image
    ALTER COLUMN image_url TYPE VARCHAR(255);

ALTER TABLE inventory
    DROP COLUMN IF EXISTS is_available;

-- Recreate NOT NULL on store_id only when every row still has a store.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM product WHERE store_id IS NULL) THEN
        ALTER TABLE product ALTER COLUMN store_id SET NOT NULL;
    END IF;
END $$;

ALTER TABLE product
    DROP COLUMN IF EXISTS retailer_id;
