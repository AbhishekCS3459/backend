CREATE TABLE IF NOT EXISTS store_bank_details (
    store_id UUID PRIMARY KEY REFERENCES store(id) ON DELETE CASCADE,
    method VARCHAR(10) NOT NULL
        CONSTRAINT store_bank_details_method_check CHECK (method IN ('bank', 'qr')),
    account_holder_name VARCHAR(255) NOT NULL DEFAULT '',
    account_number VARCHAR(255) NOT NULL DEFAULT '',
    ifsc VARCHAR(255) NOT NULL DEFAULT '',
    bank_name VARCHAR(255) NOT NULL DEFAULT '',
    qr_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Keep the account each store already entered during setup.
INSERT INTO store_bank_details (store_id, method, account_holder_name, account_number, ifsc, bank_name, qr_url)
SELECT
    id,
    CASE WHEN onboarding->'bank'->>'method' = 'qr' THEN 'qr' ELSE 'bank' END,
    COALESCE(onboarding->'bank'->>'holder', ''),
    COALESCE(onboarding->'bank'->>'number', ''),
    COALESCE(onboarding->'bank'->>'ifsc', ''),
    COALESCE(onboarding->'bank'->>'bank', ''),
    COALESCE(onboarding->'bank'->>'qr_url', '')
FROM store
WHERE COALESCE(onboarding->'bank'->>'number', '') <> ''
   OR COALESCE(onboarding->'bank'->>'qr_url', '') <> ''
ON CONFLICT (store_id) DO NOTHING;

-- Live stores without one keep being paid into the retailer's account.
INSERT INTO store_bank_details (store_id, method, account_holder_name, account_number, ifsc, qr_url)
SELECT
    s.id,
    CASE WHEN b.qr_url <> '' THEN 'qr' ELSE 'bank' END,
    b.account_holder_name,
    b.account_number,
    b.ifsc,
    b.qr_url
FROM store s
JOIN bank_details b ON b.retailer_id = s.retailer_id
WHERE s.onboarding_status = 'COMPLETED'
ON CONFLICT (store_id) DO NOTHING;
