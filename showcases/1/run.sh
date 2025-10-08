#!/bin/sh

export PGDATA="$1"

rm -rf "$PGDATA"

initdb "$PGDATA" >/dev/null 2>/dev/null
pg_ctl -D "$PGDATA" -l "$PGDATA/pg.log" start >/dev/null
psql -d postgres -f showcases/1/setup.sql >/dev/null 2>/dev/null
pgbench -d postgres -n -f showcases/1/scan.sql -c 5 -T 10 >/dev/null 2>/dev/null &
printf 'Contention run\b'
pgbench -d postgres -n -f showcases/1/update.sql -c 10 -j 2 -T 10
pg_ctl -D "$PGDATA" stop >/dev/null
grep 'LOG:  duration:' "$PGDATA/pg.log" | python3 showcases/1/histogram.py
echo
rm "$PGDATA/pg.log"
pg_ctl -D "$PGDATA" -l "$PGDATA/pg.log" start >/dev/null
printf 'Regular run\n'
pgbench -d postgres -n -f showcases/1/update.sql -c 10 -j 2 -T 10
pg_ctl -D "$PGDATA" stop >/dev/null
grep 'LOG:  duration:' "$PGDATA/pg.log" | python3 showcases/1/histogram.py
