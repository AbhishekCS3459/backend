CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    consumer_id UUID NOT NULL REFERENCES users(id),
    store_id UUID NOT NULL REFERENCES store(id),
    status VARCHAR(255) NOT NULL,
    delivery_mode VARCHAR(255) NOT NULL,
    total_amount DECIMAL(12, 2) NOT NULL,
    payment_status VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_order_consumer_id ON orders (consumer_id);
CREATE INDEX IF NOT EXISTS idx_order_store_id ON orders (store_id);
CREATE INDEX IF NOT EXISTS idx_order_status ON orders (status);
CREATE INDEX IF NOT EXISTS idx_order_payment_status ON orders (payment_status);
