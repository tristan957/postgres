#!/bin/bash
set -e

PATH=/workspaces/hackathon/pg_install/v17/bin:$PATH

rm -rf /tmp/pgdata_test
initdb -D /tmp/pgdata_test > /dev/null

cat >> /tmp/pgdata_test/postgresql.conf << 'EOF'
shared_preload_libraries = 'txn_profiler'
txn_profile.enabled = on
txn_profile.buffer_size = 128
EOF

pg_ctl -D /tmp/pgdata_test -l /tmp/pg_test.log start
sleep 2

psql -h /tmp -d postgres -c "CREATE EXTENSION txn_profiler;"
psql -h /tmp -d postgres -c "CREATE TABLE t (key TEXT PRIMARY KEY, value INT);"
psql -h /tmp -d postgres -c "INSERT INTO t VALUES ('x', 1), ('y', 2);"

echo "Running UPDATE that should generate lock events..."
psql -h /tmp -d postgres -c "BEGIN; UPDATE t SET value = 3 WHERE key = 'x'; COMMIT;"

echo "Stopping server..."
pg_ctl -D /tmp/pgdata_test stop
sleep 1

echo "Checking for profile files..."
ls -lh /tmp/pgdata_test/txn_profiles/ 2>&1 || echo "No directory found"

echo "Checking server log..."
grep -E "(txn_prof|profiling)" /tmp/pg_test.log | tail -10
