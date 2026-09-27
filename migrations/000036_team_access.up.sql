ALTER TABLE users
    ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE staff_member
    ADD COLUMN IF NOT EXISTS permissions TEXT[] NOT NULL DEFAULT '{}';

UPDATE staff_member SET role = 'STORE_STAFF' WHERE role NOT IN ('STORE_ADMIN', 'STORE_STAFF');

ALTER TABLE staff_member DROP CONSTRAINT IF EXISTS staff_member_role_check;
ALTER TABLE staff_member
    ADD CONSTRAINT staff_member_role_check CHECK (role IN ('STORE_ADMIN', 'STORE_STAFF'));

CREATE INDEX IF NOT EXISTS idx_staff_member_user_active ON staff_member (user_id) WHERE is_active;
