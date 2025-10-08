\set id random(1, 10000)
SET log_duration = on;
BEGIN;
/*traceparent='00-00000000000000000000000000000123-0000000000000123-01'*/ UPDATE lock_test SET value = value || '-updated' WHERE id = :id;
END;
