CREATE TABLE IF NOT EXISTS inventory (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_variant_id UUID NOT NULL REFERENCES product_variant(id),
    store_id UUID NOT NULL REFERENCES store(id),
    quantity_available INTEGER NOT NULL DEFAULT 0,
    quantity_reserved INTEGER NOT NULL DEFAULT 0,
    low_stock_threshold INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT inventory_store_variant_key UNIQUE (store_id, product_variant_id),
    CONSTRAINT inventory_qty_non_negative CHECK (
        quantity_available >= 0
        AND quantity_reserved >= 0
        AND low_stock_threshold >= 0
    )
);
CREATE INDEX IF NOT EXISTS idx_inventory_product_variant_id ON inventory (product_variant_id);
CREATE INDEX IF NOT EXISTS idx_inventory_store_id ON inventory (store_id);
