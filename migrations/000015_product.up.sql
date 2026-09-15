CREATE TABLE IF NOT EXISTS product (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    store_id UUID NOT NULL REFERENCES store(id),
    category_id UUID NOT NULL REFERENCES category(id),
    brand_id UUID NOT NULL REFERENCES brand(id),
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    attributes JSONB NOT NULL,
    status VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_product_store_id ON product (store_id);
CREATE INDEX IF NOT EXISTS idx_product_category_id ON product (category_id);
CREATE INDEX IF NOT EXISTS idx_product_brand_id ON product (brand_id);
CREATE INDEX IF NOT EXISTS idx_product_name ON product (name);
CREATE INDEX IF NOT EXISTS idx_product_attributes ON product USING GIN (attributes);
CREATE INDEX IF NOT EXISTS idx_product_status ON product (status);
