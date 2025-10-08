#!/bin/bash
#
# Test script for PostgreSQL transaction profiler
# Demonstrates deadlock visualization with Perfetto
#

set -e

PGDATA="/tmp/pgdata_profile_test"
LOGFILE="/tmp/postgres_profile.log"
CONVERTER="./perfetto-converter"
OUTPUT_TRACE="/tmp/deadlock_trace.json"

echo "========================================="
echo "PostgreSQL Transaction Profiler Test"
echo "========================================="
echo

# Clean up any existing test database
if [ -d "$PGDATA" ]; then
    echo "Cleaning up existing test database..."
    pg_ctl -D "$PGDATA" stop -m immediate 2>/dev/null || true
    rm -rf "$PGDATA"
fi

# Initialize new database
echo "Initializing PostgreSQL..."
initdb -D "$PGDATA" > /dev/null

# Configure for profiling
echo "Configuring profiling..."
cat >> "$PGDATA/postgresql.conf" << EOF
shared_preload_libraries = 'txn_profiler'
txn_profile.enabled = on
txn_profile.buffer_size = 128
EOF

# Start PostgreSQL
echo "Starting PostgreSQL..."
pg_ctl -D "$PGDATA" -l "$LOGFILE" start
sleep 2

# Create extension and test table
echo "Setting up test database..."
psql -d postgres -c "CREATE EXTENSION txn_profiler;" > /dev/null
psql -d postgres -c "CREATE TABLE t (key TEXT PRIMARY KEY, value INT);" > /dev/null
psql -d postgres -c "INSERT INTO t VALUES ('x', 1), ('y', 2);" > /dev/null

echo
echo "Running deadlock scenario..."
echo "  - Transaction A: locks 'x', then tries 'y'"
echo "  - Transaction B: locks 'y', then tries 'x'"
echo

# Transaction A (will timeout and rollback)
psql -d postgres << 'EOF' 2>&1 | sed 's/^/  [Txn A] /' &
BEGIN;
SELECT 'Transaction A started' as status;
SELECT pg_sleep(1);
UPDATE t SET value = 3 WHERE key = 'x';
SELECT 'Transaction A: locked x' as status;
SELECT pg_sleep(2);
UPDATE t SET value = 5 WHERE key = 'y';
SELECT 'Transaction A: locked y (should not reach here)' as status;
ROLLBACK;
EOF
TXN_A_PID=$!

# Wait a bit before starting B
sleep 0.5

# Transaction B (will wait then succeed)
psql -d postgres << 'EOF' 2>&1 | sed 's/^/  [Txn B] /' &
BEGIN;
SELECT 'Transaction B started' as status;
SELECT pg_sleep(1.5);
UPDATE t SET value = 4 WHERE key = 'y';
SELECT 'Transaction B: locked y' as status;
SELECT pg_sleep(2);
UPDATE t SET value = 6 WHERE key = 'x';
SELECT 'Transaction B: acquired x after wait' as status;
COMMIT;
SELECT 'Transaction B: committed' as status;
EOF
TXN_B_PID=$!

# Wait for both transactions to complete
wait $TXN_A_PID 2>/dev/null || echo "  [Txn A] Transaction rolled back (expected)"
wait $TXN_B_PID

echo
echo "Transactions complete. Stopping PostgreSQL to flush profile data..."
pg_ctl -D "$PGDATA" stop
sleep 1

# Check for profile files
PROFILE_COUNT=$(ls -1 "$PGDATA/txn_profiles/"*.bin 2>/dev/null | wc -l)
echo
echo "Profile files generated: $PROFILE_COUNT"

if [ $PROFILE_COUNT -eq 0 ]; then
    echo "ERROR: No profile files found!"
    exit 1
fi

# List profile files
echo "Profile files:"
ls -lh "$PGDATA/txn_profiles/"*.bin | awk '{print "  " $9 " (" $5 ")"}'

# Convert to Perfetto
echo
echo "Converting to Perfetto trace..."
if [ ! -f "$CONVERTER" ]; then
    echo "ERROR: Converter not found. Building..."
    go build -o perfetto-converter
fi

$CONVERTER -input "$PGDATA/txn_profiles" -output "$OUTPUT_TRACE"

echo
echo "========================================="
echo "SUCCESS!"
echo "========================================="
echo
echo "Trace file: $OUTPUT_TRACE"
echo
echo "To view the trace:"
echo "  1. Open Chrome/Chromium browser"
echo "  2. Navigate to: chrome://tracing"
echo "  3. Click 'Load' and select: $OUTPUT_TRACE"
echo
echo "Or visit: https://ui.perfetto.dev/"
echo
echo "What to look for:"
echo "  - Two backend tracks (one per transaction)"
echo "  - Lock attempt/acquired/wait/release events"
echo "  - Transaction A waits ~10s, then aborts"
echo "  - Transaction B waits, then succeeds"
echo "  - Clear visualization of deadlock pattern"
echo
