CREATE TABLE IF NOT EXISTS review (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    store_id UUID NOT NULL REFERENCES store(id),
    product_id UUID REFERENCES product(id),
    consumer_id UUID NOT NULL REFERENCES users(id),
    rating INTEGER NOT NULL CHECK (rating >= 1 AND rating <= 5),
    comment TEXT NOT NULL,
    retailer_response TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_review_store_id ON review (store_id);
CREATE INDEX IF NOT EXISTS idx_review_product_id ON review (product_id);
CREATE INDEX IF NOT EXISTS idx_review_consumer_id ON review (consumer_id);
