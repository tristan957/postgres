#!/bin/bash
set -e

PATH=/workspaces/hackathon/pg_install/v17/bin:$PATH

echo "=== Setting up test database ==="
rm -rf /tmp/pgdata_deadlock
initdb -D /tmp/pgdata_deadlock > /dev/null

cat >> /tmp/pgdata_deadlock/postgresql.conf << 'EOF'
shared_preload_libraries = 'txn_profiler'
txn_profile.enabled = on
txn_profile.buffer_size = 128
lock_timeout = 0
compute_query_id = on
EOF

pg_ctl -D /tmp/pgdata_deadlock -l /tmp/pg_deadlock.log start
sleep 2

echo "=== Creating schema ==="
psql -h /tmp -d postgres -c "CREATE EXTENSION txn_profiler;"
psql -h /tmp -d postgres -c "CREATE TABLE t (key TEXT PRIMARY KEY, value INT);"
psql -h /tmp -d postgres -c "INSERT INTO t VALUES ('x', 1), ('y', 2);"

echo "=== Starting Transaction A (will timeout) ==="
psql -h /tmp -d postgres > /tmp/txn_a.log 2>&1 << 'EOF' &
SET lock_timeout = '5s';
BEGIN;
SELECT pg_backend_pid() as backend_a;
UPDATE t SET value = 3 WHERE key = 'x';  -- Acquire lock on (0,1)
SELECT pg_sleep(1);
UPDATE t SET value = 5 WHERE key = 'y';  -- Try to lock (0,2), will wait then timeout
COMMIT;
EOF
PID_A=$!

sleep 0.5

echo "=== Starting Transaction B (will succeed) ==="
psql -h /tmp -d postgres > /tmp/txn_b.log 2>&1 << 'EOF' &
SET lock_timeout = '10s';
BEGIN;
SELECT pg_backend_pid() as backend_b;
UPDATE t SET value = 4 WHERE key = 'y';  -- Acquire lock on (0,2)
SELECT pg_sleep(1);
UPDATE t SET value = 6 WHERE key = 'x';  -- Try to lock (0,1), will wait then succeed
COMMIT;
EOF
PID_B=$!

echo "=== Waiting for transactions to complete ==="
wait $PID_A || echo "Transaction A failed (expected - timeout)"
wait $PID_B || echo "Transaction B status: $?"

sleep 1

echo "=== Stopping PostgreSQL ==="
pg_ctl -D /tmp/pgdata_deadlock stop
sleep 1

echo ""
echo "=== Transaction A log ==="
cat /tmp/txn_a.log

echo ""
echo "=== Transaction B log ==="
cat /tmp/txn_b.log

echo ""
echo "=== Profile files generated ==="
ls -lh /tmp/pgdata_deadlock/txn_profiles/

echo ""
echo "=== Converting to Perfetto ==="
/workspaces/hackathon/vendor/postgres-v17/tools/perfetto-converter/perfetto-converter \
  -input /tmp/pgdata_deadlock/txn_profiles \
  -output /tmp/deadlock_trace.json

echo ""
echo "=== Analyzing trace events ==="
python3 << 'PYTHON'
import json
import sys

with open('/tmp/deadlock_trace.json', 'r') as f:
    trace = json.load(f)

# Group events by PID
events_by_pid = {}
for evt in trace['traceEvents']:
    if 'pid' in evt:
        pid = evt['pid']
        if pid not in events_by_pid:
            events_by_pid[pid] = []
        events_by_pid[pid].append(evt)

print("=== TRACE ANALYSIS ===\n")

for pid, events in sorted(events_by_pid.items()):
    # Filter out metadata events
    actual_events = [e for e in events if e.get('ph') not in ['M']]
    if not actual_events:
        continue

    print(f"Backend PID {pid}:")
    print("-" * 60)

    for evt in actual_events:
        ts = evt.get('ts', 0)
        ph = evt.get('ph', '?')
        cat = evt.get('cat', 'unknown')
        name = evt.get('name', 'unnamed')
        args = evt.get('args', {})

        if ph == 'B':  # Begin
            phase_str = "START"
        elif ph == 'E':  # End
            phase_str = "END  "
        elif ph == 'i':  # Instant
            phase_str = "EVENT"
        else:
            phase_str = ph

        # Show relevant info
        info = []
        if 'xid' in args:
            info.append(f"xid={args['xid']}")
        if 'ctid' in args:
            info.append(f"ctid={args['ctid']}")
        if 'mode' in args:
            info.append(f"mode={args['mode']}")
        if 'query_id' in args:
            info.append(f"qid={args['query_id']}")

        info_str = " ".join(info) if info else ""
        print(f"  {phase_str} [{cat:12s}] {name:30s} {info_str}")

    print()

print("\n=== What to look for: ===")
print("1. Two backend tracks with different PIDs")
print("2. Each backend should show:")
print("   - Transaction BEGIN/COMMIT or ABORT")
print("   - Lock ATTEMPT and ACQUIRED for first UPDATE")
print("   - Lock ATTEMPT for second UPDATE (contention)")
print("   - Lock WAIT events showing blocking")
print("   - One transaction should timeout/abort")
print("   - Other should succeed after lock is released")
print("3. CTIDs should show (0,1) for key='x' and (0,2) for key='y'")
PYTHON

echo ""
echo "Trace file: /tmp/deadlock_trace.json"
echo "View at: chrome://tracing or https://ui.perfetto.dev/"
