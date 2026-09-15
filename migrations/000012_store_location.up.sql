CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS store_location (
    store_id UUID PRIMARY KEY REFERENCES store(id),
    address_line VARCHAR(255) NOT NULL,
    city VARCHAR(255) NOT NULL,
    pincode VARCHAR(255) NOT NULL,
    lat DECIMAL(10, 8) NOT NULL,
    lng DECIMAL(11, 8) NOT NULL,
    geog geography(Point, 4326)
        GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(lng, lat), 4326)::geography) STORED,
    service_area_radius_km INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_store_location_pincode ON store_location (pincode);
CREATE INDEX IF NOT EXISTS idx_store_location_city ON store_location (city);
CREATE INDEX IF NOT EXISTS idx_store_location_geog ON store_location USING GIST (geog);
