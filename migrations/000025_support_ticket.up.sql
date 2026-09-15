CREATE TABLE IF NOT EXISTS support_ticket (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    store_id UUID NOT NULL REFERENCES store(id),
    order_id UUID REFERENCES orders(id),
    raised_by UUID NOT NULL REFERENCES users(id),
    category VARCHAR(255) NOT NULL,
    status VARCHAR(255) NOT NULL,
    resolution TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_support_ticket_store_id ON support_ticket (store_id);
CREATE INDEX IF NOT EXISTS idx_support_ticket_order_id ON support_ticket (order_id);
