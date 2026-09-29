-- Each store sets its own selling price. product_variant.price stays as the
-- retailer's default: the price a product starts at when a store lists it.
ALTER TABLE inventory ADD COLUMN IF NOT EXISTS price DECIMAL(12, 2);

UPDATE inventory i
SET price = v.price
FROM product_variant v
WHERE v.id = i.product_variant_id AND i.price IS NULL;

-- A row inserted without a price starts at the default. This also keeps a
-- backend that doesn't know the column yet (mid-deploy) and seed.sql working.
CREATE OR REPLACE FUNCTION inventory_default_price() RETURNS trigger AS $$
BEGIN
    IF NEW.price IS NULL THEN
        SELECT price INTO NEW.price FROM product_variant WHERE id = NEW.product_variant_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS inventory_default_price ON inventory;
CREATE TRIGGER inventory_default_price
    BEFORE INSERT ON inventory
    FOR EACH ROW EXECUTE FUNCTION inventory_default_price();

ALTER TABLE inventory ALTER COLUMN price SET NOT NULL;
ALTER TABLE inventory DROP CONSTRAINT IF EXISTS inventory_price_positive;
ALTER TABLE inventory ADD CONSTRAINT inventory_price_positive CHECK (price > 0);
