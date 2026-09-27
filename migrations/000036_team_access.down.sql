DROP INDEX IF EXISTS idx_staff_member_user_active;
ALTER TABLE staff_member DROP CONSTRAINT IF EXISTS staff_member_role_check;
ALTER TABLE staff_member DROP COLUMN IF EXISTS permissions;
ALTER TABLE users DROP COLUMN IF EXISTS must_change_password;
