DROP INDEX IF EXISTS idx_store_retailer_onboarding;

ALTER TABLE store
    DROP COLUMN IF EXISTS onboarding_completed_at,
    DROP COLUMN IF EXISTS onboarding,
    DROP COLUMN IF EXISTS onboarding_status;
