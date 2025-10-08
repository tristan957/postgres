\set id random(1, 10000)
SET log_duration = on;
BEGIN;
UPDATE lock_test SET value = value || '-updated' WHERE id = :id;
END;
