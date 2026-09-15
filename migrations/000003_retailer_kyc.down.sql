-- Drop indexes if they exist before dropping the retailer_kyc table
DROP INDEX IF EXISTS idx_retailer_kyc_retailer_id;
DROP INDEX IF EXISTS idx_retailer_kyc_status;

-- Drop the retailer_kyc table if it exists
DROP TABLE IF EXISTS retailer_kyc;