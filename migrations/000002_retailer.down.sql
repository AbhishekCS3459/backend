-- Drop indexes if they exist before dropping the retailers table
DROP INDEX IF EXISTS idx_retailers_user_id;
DROP INDEX IF EXISTS idx_retailers_legal_name;
DROP INDEX IF EXISTS idx_retailers_owner_name;
DROP INDEX IF EXISTS idx_retailers_kyc_status;

-- Drop the retailers table if it exists
DROP TABLE IF EXISTS retailers;