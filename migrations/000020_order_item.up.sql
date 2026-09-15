CREATE TABLE IF NOT EXISTS order_item (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id),
    product_variant_id UUID NOT NULL REFERENCES product_variant(id),
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    price_at_order_time DECIMAL(12, 2) NOT NULL,
    substitution_status VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_order_item_order_id ON order_item (order_id);
CREATE INDEX IF NOT EXISTS idx_order_item_product_variant_id ON order_item (product_variant_id);
