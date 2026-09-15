CREATE TABLE IF NOT EXISTS product_variant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES product(id),
    variant_label VARCHAR(255) NOT NULL,
    sku VARCHAR(255) NOT NULL UNIQUE,
    price DECIMAL(12, 2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_product_variant_product_id ON product_variant (product_id);
