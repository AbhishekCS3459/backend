CREATE TABLE IF NOT EXISTS bank_details (
    retailer_id UUID PRIMARY KEY REFERENCES retailers(id),
    account_number VARCHAR(255) NOT NULL,
    ifsc VARCHAR(255) NOT NULL,
    account_holder_name VARCHAR(255) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
