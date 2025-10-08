#!/bin/sh

export PGDATA="$1"

rm -rf "$PGDATA"

printf 'Scan query\n\n'
cat showcases/1/scan.sql
echo

printf 'Update query\n\n'
cat showcases/1/update.sql
echo

SCRIPT_DIR=/Users/tristan.partin/Projects/work/postgres/tools/perfetto-converter
DB_CONN="host=127.0.0.1 port=5432 user=tristan.partin dbname=postgres"
PROFILE_DIR="$PGDATA/txn_profiles"
OUTPUT_TRACE="$HOME/Desktop/deadlock_trace.json"
CONVERTER="$SCRIPT_DIR/perfetto-converter"
# PYTHON_CONVERTER="$SCRIPT_DIR/perfetto_converter.py"

initdb "$PGDATA" >/dev/null 2>/dev/null
echo "shared_preload_libraries = 'pg_tracing,txn_profiler'" >> "$PGDATA/postgresql.conf"
echo 'pg_tracing.sample_rate = 1.0' >> "$PGDATA/postgresql.conf"
echo 'txn_profile.buffer_size = 128' >> "$PGDATA/postgresql.conf"
pg_ctl -D "$PGDATA" -l "$PGDATA/pg.log" start >/dev/null
psql -d postgres -c 'CREATE EXTENSION pg_tracing' >/dev/null
psql -d postgres -f showcases/1/setup.sql >/dev/null 2>/dev/null
psql -d postgres -c 'ALTER SYSTEM SET txn_profile.enabled = on'
psql -d postgres -c 'SELECT pg_reload_conf()'
pgbench -d postgres -n -f showcases/1/scan.sql -T 10 >/dev/null 2>/dev/null &
pgbench -d postgres -n -f showcases/1/update.sql -T 10
printf 'Contention run\n\n'
$CONVERTER -input "$PROFILE_DIR" -db "$DB_CONN" -output "$OUTPUT_TRACE"
pg_ctl -D "$PGDATA" stop >/dev/null
grep 'LOG:  duration:' "$PGDATA/pg.log" | python3 showcases/1/histogram.py
echo
rm "$PGDATA/pg.log"
pg_ctl -D "$PGDATA" -l "$PGDATA/pg.log" start >/dev/null
printf 'Regular run\n\n'
pgbench -d postgres -n -f showcases/1/update.sql -T 10
pg_ctl -D "$PGDATA" stop >/dev/null
grep 'LOG:  duration:' "$PGDATA/pg.log" | python3 showcases/1/histogram.py
