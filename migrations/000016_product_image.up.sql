
CREATE TABLE IF NOT EXISTS product_image (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES product(id),
    image_url VARCHAR(255) NOT NULL,
    sort_order INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_product_image_product_id ON product_image (product_id);
CREATE INDEX IF NOT EXISTS idx_product_image_image_url ON product_image (image_url);
CREATE INDEX IF NOT EXISTS idx_product_image_sort_order ON product_image (sort_order);