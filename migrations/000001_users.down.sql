-- Drop indexes if they exist before dropping the users table
DROP INDEX IF EXISTS idx_users_password_reset_token;
DROP INDEX IF EXISTS idx_users_user_type;

-- Drop the users table if it exists
DROP TABLE IF EXISTS users;