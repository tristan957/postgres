\set id random(1, 10000)
BEGIN;
SELECT value FROM lock_test WHERE id < :id FOR UPDATE;
END;
