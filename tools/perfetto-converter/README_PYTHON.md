# Perfetto Converter - Python Implementation

Python reimplementation of the Go-based perfetto-converter that outputs native Perfetto protobuf traces instead of JSON.

## Overview

This converter reads:
1. Binary transaction profile files (`.bin`) from PostgreSQL txn_profiler extension
2. pg_tracing spans from a running PostgreSQL database

And outputs a Perfetto protobuf trace file (`.pb`) that can be viewed in the Perfetto UI.

## Installation

```bash
# Install dependencies
pip install -r requirements.txt

# Or install individually:
pip install perfetto psycopg2-binary protobuf
```

## Usage

### Basic usage with binary files

```bash
./perfetto_converter.py -input /tmp/pg-test-data/txn_profiles -output trace.pb
```

### Specify files directly

```bash
./perfetto_converter.py file1.bin file2.bin -output trace.pb
```

### With custom database connection

```bash
./perfetto_converter.py \
  -db "postgres://user:pass@localhost:5432/mydb" \
  -input /path/to/profiles \
  -output trace.pb
```

### Command-line options

- `-db, --database`: PostgreSQL connection string (default: `postgres://localhost/postgres?host=/tmp`)
- `-input, --input-dir`: Directory containing `*.bin` files
- `-files, --input-files`: Comma-separated list of input files
- `-output, --output`: Output file path (default: `trace.pb`)

## Viewing Traces

1. Go to https://ui.perfetto.dev/
2. Click "Open trace file"
3. Select your generated `.pb` file

## Comparison with Go Version

| Feature | Go (JSON) | Python (Protobuf) |
|---------|-----------|-------------------|
| Output format | JSON (Chrome trace format) | Protobuf (native Perfetto) |
| File size | Larger (text) | Smaller (binary) |
| Perfetto UI support | Full | Full |
| chrome://tracing | Yes | No (protobuf not supported) |
| Flow events | Supported | Limited (different protobuf structure) |
| Performance | Fast | Moderate |

## Binary File Format

The converter reads binary profile files with the following structure:

### Header (28 bytes)
```
- version (uint32)
- pg_version (uint32)
- backend_id (uint32)
- pid (uint32)
- padding (uint64)
- event_count (int32)
```

### Event (56 bytes each)
```
- timestamp_ns (uint64)
- backend_id (uint32)
- pid (uint32)
- xid (uint32)
- padding1 (uint32)
- query_id (uint64)
- reloid (uint32)
- blocknum (uint32)
- offnum (uint16)
- padding2 (uint16)
- event_type (uint32)
- lock_mode (uint16)
- padding3 (6 bytes)
```

## Event Types

```python
TXN_BEGIN = 0
TXN_COMMIT = 1
TXN_ABORT = 2
QUERY_START = 3
QUERY_END = 4
LOCK_ATTEMPT = 5
LOCK_ACQUIRED = 6
LOCK_WAIT_START = 7
LOCK_WAIT_END = 8
LOCK_RELEASED = 9
LOCK_TIMEOUT = 10
```

## Tracks

Each backend process gets three tracks:
1. **Transactions** - Transaction lifecycle (BEGIN/COMMIT/ABORT)
2. **Queries** - Query execution spans
3. **Locks** - Lock operations (attempt, hold, wait, timeout)

## Flow Events

Flow events showing lock contention are currently limited due to differences in how Perfetto protobuf represents flows compared to the Chrome JSON format. The basic slice events still show timing relationships.

## Development

### Testing

```bash
# Run the converter on test data
./perfetto_converter.py -input /tmp/pg-test-data/txn_profiles -output test_trace.pb

# Verify output
ls -lh test_trace.pb
```

### Debugging

Add print statements or use Python's built-in debugger:

```python
import pdb; pdb.set_trace()
```

## Troubleshooting

### "perfetto package not found"
```bash
pip install perfetto
```

### "psycopg2 not found"
```bash
pip install psycopg2-binary
```

### Database connection fails
- Verify PostgreSQL is running
- Check connection string format
- Ensure pg_tracing extension is installed:
  ```sql
  CREATE EXTENSION pg_tracing;
  ```

### No spans found
- Make sure queries are run with pg_tracing enabled
- Check that traces are visible:
  ```sql
  SELECT count(*) FROM pg_tracing_peek_spans;
  ```

## Architecture

1. **Binary Parser** (`parse_binary_file`): Reads C struct-packed binary files
2. **pg_tracing Querier** (`query_pg_tracing_spans`): Queries PostgreSQL for span data
3. **Lock Timeline Builder** (`build_lock_timeline`): Builds chronological lock event timeline
4. **Event Processor** (`process_backend_events`): Converts events to Perfetto format
5. **Trace Builder** (`PerfettoTraceBuilder`): Constructs protobuf trace packets

## License

Same as parent PostgreSQL project.
