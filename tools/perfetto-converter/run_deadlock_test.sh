#!/bin/bash
set -e

# Get script directory (tools/perfetto-converter)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Configuration
PG_INSTALL="/tmp/pg-install"
PG_DATA="/tmp/pg-test-data"
PG_LOG="/tmp/postgres_test.log"
PG_PORT="5433"
PROFILE_DIR="$PG_DATA/txn_profiles"
OUTPUT_TRACE="/tmp/deadlock_trace.json"
CONVERTER="$SCRIPT_DIR/perfetto-converter"

echo "=== PostgreSQL Deadlock Trace Test ==="
echo "Repository: $REPO_ROOT"
echo ""

# Build converter
echo "0. Building perfetto-converter..."
(cd "$SCRIPT_DIR" && go build -o perfetto-converter)

# Clean up old data
echo "1. Cleaning old profile data..."
rm -f $PROFILE_DIR/*.bin
rm -f $PG_LOG

# Start PostgreSQL
echo "2. Starting PostgreSQL on port $PG_PORT..."
$PG_INSTALL/bin/pg_ctl -D $PG_DATA -o "-p $PG_PORT" -l $PG_LOG start
sleep 1

# Create pg_tracing extension
echo "3. Creating pg_tracing extension..."
$PG_INSTALL/bin/psql -h 127.0.0.1 -p $PG_PORT -U $USER postgres <<EOF
CREATE EXTENSION IF NOT EXISTS pg_tracing;
EOF

# Reset table data to known state
echo "4. Resetting table data..."
$PG_INSTALL/bin/psql -h 127.0.0.1 -p $PG_PORT -U $USER postgres <<EOF
DELETE FROM t;
INSERT INTO t VALUES ('x', 5), ('y', 6);
EOF

# Create transaction scripts
echo "5. Creating transaction scripts..."
cat > /tmp/test_txn_a.sql <<'SQL'
/*traceparent='00-00000000000000000000000000000001-0000000000000001-01'*/ BEGIN;
/*traceparent='00-00000000000000000000000000000001-0000000000000001-01'*/ SET lock_timeout = '20ms';
/*traceparent='00-00000000000000000000000000000001-0000000000000001-01'*/ SELECT pg_sleep(0.01);
/*traceparent='00-00000000000000000000000000000001-0000000000000001-01'*/ UPDATE t SET value = 50 WHERE key = 'x';
/*traceparent='00-00000000000000000000000000000001-0000000000000001-01'*/ SELECT pg_sleep(0.01);
/*traceparent='00-00000000000000000000000000000001-0000000000000001-01'*/ UPDATE t SET value = 60 WHERE key = 'y';
/*traceparent='00-00000000000000000000000000000001-0000000000000001-01'*/ SELECT pg_sleep(0.01);
/*traceparent='00-00000000000000000000000000000001-0000000000000001-01'*/ COMMIT;
SQL

cat > /tmp/test_txn_b.sql <<'SQL'
/*traceparent='00-00000000000000000000000000000002-0000000000000002-01'*/ BEGIN;
/*traceparent='00-00000000000000000000000000000002-0000000000000002-01'*/ SET lock_timeout = '20ms';
/*traceparent='00-00000000000000000000000000000002-0000000000000002-01'*/ SELECT pg_sleep(0.01);
/*traceparent='00-00000000000000000000000000000002-0000000000000002-01'*/ UPDATE t SET value = 100 WHERE key = 'y';
/*traceparent='00-00000000000000000000000000000002-0000000000000002-01'*/ SELECT pg_sleep(0.01);
/*traceparent='00-00000000000000000000000000000002-0000000000000002-01'*/ UPDATE t SET value = 110 WHERE key = 'x';
/*traceparent='00-00000000000000000000000000000002-0000000000000002-01'*/ COMMIT;
SQL

# Run concurrent transactions
echo "6. Running concurrent transactions (expect deadlock)..."
$PG_INSTALL/bin/psql -h 127.0.0.1 -p $PG_PORT -U $USER postgres -f /tmp/test_txn_a.sql > /tmp/test_txn_a_output.txt 2>&1 &
PID_A=$!
$PG_INSTALL/bin/psql -h 127.0.0.1 -p $PG_PORT -U $USER postgres -f /tmp/test_txn_b.sql > /tmp/test_txn_b_output.txt 2>&1 &
PID_B=$!

# Wait for both transactions to complete
wait $PID_A || true
wait $PID_B || true

echo "   Transaction A (PID $PID_A): $(grep -E 'COMMIT|ROLLBACK' /tmp/test_txn_a_output.txt | tail -1)"
echo "   Transaction B (PID $PID_B): $(grep -E 'COMMIT|ROLLBACK' /tmp/test_txn_b_output.txt | tail -1)"

# Query pg_tracing spans and generate trace
echo "7. Querying pg_tracing spans and generating Perfetto trace..."
DB_CONN="host=127.0.0.1 port=$PG_PORT user=$USER dbname=postgres"
$CONVERTER -input $PROFILE_DIR -db "$DB_CONN" -output $OUTPUT_TRACE

# Stop PostgreSQL
echo "8. Stopping PostgreSQL..."
$PG_INSTALL/bin/pg_ctl -D $PG_DATA stop -m fast
sleep 1

# Show results
echo ""
echo "=== Results ==="
EVENT_COUNT=$(jq '.traceEvents | length' $OUTPUT_TRACE)
echo "Trace events: $EVENT_COUNT"
echo ""

# Show span summary
echo "Span summary:"
jq -r '.traceEvents[] | select(.name) | "  \(.name) (pid=\(.pid))"' $OUTPUT_TRACE | head -20

echo ""
echo "Trace written to: $OUTPUT_TRACE"
echo ""
echo "View in:"
echo "  - Chrome: chrome://tracing"
echo "  - Perfetto UI: https://ui.perfetto.dev/"
echo ""
echo "PostgreSQL log: $PG_LOG"
