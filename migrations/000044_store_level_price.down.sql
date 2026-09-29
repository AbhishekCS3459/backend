DROP TRIGGER IF EXISTS inventory_default_price ON inventory;
DROP FUNCTION IF EXISTS inventory_default_price();
ALTER TABLE inventory DROP CONSTRAINT IF EXISTS inventory_price_positive;
ALTER TABLE inventory DROP COLUMN IF EXISTS price;
