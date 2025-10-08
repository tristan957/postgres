DROP TABLE IF EXISTS lock_test;

CREATE TABLE lock_test (
    id SERIAL PRIMARY KEY,
    value TEXT
);

INSERT INTO lock_test (value)
SELECT 'value-' || gs
FROM generate_series(1, 10000) AS gs;
