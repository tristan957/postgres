# PostgreSQL Transaction Profiler - Perfetto Converter

This tool converts PostgreSQL transaction profile data into Perfetto-compatible JSON traces for visualization.

## Quick Start

### 1. Enable Profiling in PostgreSQL

Add to `postgresql.conf`:
```
shared_preload_libraries = 'txn_profiler'
txn_profile.enabled = on
txn_profile.buffer_size = 128  # KB
```

Or set via command line:
```sql
ALTER SYSTEM SET shared_preload_libraries = 'txn_profiler';
ALTER SYSTEM SET txn_profile.enabled = on;
SELECT pg_reload_conf();
```

### 2. Create Extension

```sql
CREATE EXTENSION txn_profiler;
```

### 3. Run Your Workload

The profiler will automatically capture transaction and lock events. Profile data is written to `$PGDATA/txn_profiles/` when each backend process exits.

### 4. Convert to Perfetto Trace

```bash
# Convert all profile files in directory
./perfetto-converter -input $PGDATA/txn_profiles -output trace.json

# Or specify individual files
./perfetto-converter file1.bin file2.bin -output trace.json
```

### 5. View in Browser

Option 1 - Chrome built-in:
- Open `chrome://tracing`
- Click "Load" and select `trace.json`

Option 2 - Perfetto UI:
- Visit https://ui.perfetto.dev/
- Click "Open trace file" and select `trace.json`

## Example: Visualizing a Deadlock

```bash
# Terminal 1: Start PostgreSQL with profiling
initdb -D /tmp/pgdata
echo "shared_preload_libraries = 'txn_profiler'" >> /tmp/pgdata/postgresql.conf
echo "txn_profile.enabled = on" >> /tmp/pgdata/postgresql.conf
pg_ctl -D /tmp/pgdata -l /tmp/postgres.log start

# Create test table
psql -d postgres -c "CREATE EXTENSION txn_profiler;"
psql -d postgres -c "CREATE TABLE t (key TEXT PRIMARY KEY, value INT);"
psql -d postgres -c "INSERT INTO t VALUES ('x', 1), ('y', 2);"

# Terminal 2: Run transaction A
psql -d postgres << 'EOF' &
BEGIN;
SELECT pg_sleep(1);
UPDATE t SET value = 3 WHERE key = 'x';  -- Lock x
SELECT pg_sleep(2);
UPDATE t SET value = 5 WHERE key = 'y';  -- Try to lock y (will wait)
SELECT pg_sleep(15);
ROLLBACK;
EOF

# Terminal 3: Run transaction B
sleep 0.5
psql -d postgres << 'EOF' &
BEGIN;
SELECT pg_sleep(1.5);
UPDATE t SET value = 4 WHERE key = 'y';  -- Lock y
SELECT pg_sleep(2);
UPDATE t SET value = 6 WHERE key = 'x';  -- Try to lock x (will wait)
COMMIT;
EOF

# Wait for both transactions
wait

# Stop PostgreSQL to flush buffers
pg_ctl -D /tmp/pgdata stop

# Convert to Perfetto
./perfetto-converter -input /tmp/pgdata/txn_profiles -output deadlock_trace.json

# View in browser
echo "Open chrome://tracing and load deadlock_trace.json"
```

## What You'll See

In the Perfetto UI, you'll see:

1. **Two tracks** - One per backend process
2. **Transaction spans** - Showing BEGIN to COMMIT/ROLLBACK
3. **Query slices** - Nested within transactions
4. **Lock events**:
   - Lock attempt → Lock acquired (shows wait time)
   - Lock hold (from acquire to release)
   - Lock wait events (for XactLockTableWait)
5. **Timeline** - Clear visualization of contention and deadlock patterns

## Event Types

The profiler captures these events:

- `TXNPROF_TXN_BEGIN` - Transaction starts
- `TXNPROF_TXN_COMMIT` - Transaction commits
- `TXNPROF_TXN_ABORT` - Transaction aborts/rollbacks
- `TXNPROF_QUERY_START` - Query execution starts
- `TXNPROF_QUERY_END` - Query execution completes
- `TXNPROF_LOCK_ATTEMPT` - Attempting to acquire row lock
- `TXNPROF_LOCK_ACQUIRED` - Successfully acquired row lock
- `TXNPROF_LOCK_WAIT_START` - Started waiting for transaction
- `TXNPROF_LOCK_WAIT_END` - Finished waiting for transaction
- `TXNPROF_LOCK_RELEASED` - Released row lock
- `TXNPROF_LOCK_TIMEOUT` - Lock acquisition timed out

## Configuration

GUC parameters:

- `txn_profile.enabled` (bool) - Enable/disable profiling (default: off)
- `txn_profile.buffer_size` (int) - Ring buffer size in KB (default: 64, range: 8-1024)
- `txn_profile.output_dir` (string) - Output directory (default: $PGDATA/txn_profiles)

## Performance Notes

- Ring buffer is process-local (no shared memory contention)
- Events are fixed-size (64 bytes)
- Lock-free single-writer design
- File I/O only at backend exit
- Minimal overhead (~5-10 nanoseconds per event)

For production use, consider:
- Enabling only during debugging sessions
- Using larger buffer sizes for long-running backends
- Cleaning up old profile files periodically

## Binary Format

Profile files use this format:

Header (40 bytes):
- version (uint32)
- pg_version (uint32)
- backend_id (uint32)
- pid (uint32)
- start_time_sec (int64)
- start_time_ns (int64)
- event_count (int32)

Events (64 bytes each):
- timestamp_ns (uint64)
- backend_id (uint32)
- pid (uint32)
- xid (uint32)
- query_id (uint64)
- reloid (uint32)
- blocknum (uint32)
- offnum (uint16)
- event_type (uint32)
- lock_mode (uint16)
- padding (6 bytes)

## Troubleshooting

**No profile files generated:**
- Check `txn_profile.enabled = on` in postgresql.conf
- Verify extension is loaded: `SHOW shared_preload_libraries;`
- Ensure backend processes have exited (profile files written on exit)

**Converter errors:**
- Verify binary format matches (check version in header)
- Ensure files are complete (not truncated mid-write)

**Empty trace:**
- Check that events were actually generated (file size > header size)
- Verify workload actually performed UPDATE/DELETE operations

## Building

```bash
cd tools/perfetto-converter
go build -o perfetto-converter
```

## License

PostgreSQL License
