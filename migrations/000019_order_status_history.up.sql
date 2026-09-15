CREATE TABLE IF NOT EXISTS order_status_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id),
    status VARCHAR(255) NOT NULL,
    changed_by UUID NOT NULL REFERENCES users(id),
    changed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_order_status_history_order_id ON order_status_history (order_id);
CREATE INDEX IF NOT EXISTS idx_order_status_history_status ON order_status_history (status);
CREATE INDEX IF NOT EXISTS idx_order_status_history_changed_by ON order_status_history (changed_by);
CREATE INDEX IF NOT EXISTS idx_order_status_history_changed_at ON order_status_history (changed_at);