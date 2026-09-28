-- Per-store onboarding. Until now onboarding lived in user_progress (one row per
-- user), so a finished store's draft blocked starting the next store.
--
-- Additive only. The column default stays COMPLETED so stores created by code
-- that predates this migration (it only created stores once setup was done)
-- keep their meaning; new code inserts DRAFT explicitly.
ALTER TABLE store
    ADD COLUMN IF NOT EXISTS onboarding_status VARCHAR(20) NOT NULL DEFAULT 'COMPLETED'
        CONSTRAINT store_onboarding_status_check CHECK (onboarding_status IN ('DRAFT', 'COMPLETED')),
    ADD COLUMN IF NOT EXISTS onboarding JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS onboarding_completed_at TIMESTAMPTZ;

UPDATE store
SET onboarding_completed_at = created_at
WHERE onboarding_status = 'COMPLETED' AND onboarding_completed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_store_retailer_onboarding ON store (retailer_id, onboarding_status);
