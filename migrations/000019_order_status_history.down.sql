
DROP INDEX IF EXISTS idx_order_status_history_order_id;
DROP INDEX IF EXISTS idx_order_status_history_status;
DROP INDEX IF EXISTS idx_order_status_history_changed_by;
DROP INDEX IF EXISTS idx_order_status_history_changed_at;
DROP TABLE IF EXISTS order_status_history;