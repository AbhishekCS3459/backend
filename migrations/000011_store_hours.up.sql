
CREATE TABLE IF NOT EXISTS store_hours (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    store_id UUID NOT NULL REFERENCES store(id),
    day_of_week VARCHAR(255) NOT NULL,
    open_time VARCHAR(255) NOT NULL,
    close_time VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_store_hours_store_id ON store_hours (store_id);
CREATE INDEX IF NOT EXISTS idx_store_hours_day_of_week ON store_hours (day_of_week);
CREATE INDEX IF NOT EXISTS idx_store_hours_open_time ON store_hours (open_time);
CREATE INDEX IF NOT EXISTS idx_store_hours_close_time ON store_hours (close_time);