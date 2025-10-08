#!/usr/bin/env python3
"""
Perfetto Trace Converter for PostgreSQL Transaction Profiles

Converts binary transaction profile files and pg_tracing spans to Perfetto protobuf format.
Reimplementation of the Go converter using native Perfetto protobufs.
"""

import argparse
import struct
import sys
from dataclasses import dataclass
from enum import IntEnum
from pathlib import Path
from typing import Dict, List, Optional, Tuple
import psycopg2
import psycopg2.extras
from datetime import datetime

# Perfetto imports
from perfetto.trace_builder.proto_builder import TraceProtoBuilder
from perfetto.protos.perfetto.trace.perfetto_trace_pb2 import TrackEvent, TrackDescriptor


class EventType(IntEnum):
    """Event types matching TxnProfileEventType from C"""
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


@dataclass
class Header:
    """Binary file header matching C struct"""
    version: int
    pg_version: int
    backend_id: int
    pid: int
    padding: int
    event_count: int


@dataclass
class Event:
    """Binary event structure matching C struct (56 bytes)"""
    timestamp_ns: int
    backend_id: int
    pid: int
    xid: int
    padding1: int
    query_id: int
    reloid: int
    blocknum: int
    offnum: int
    padding2: int
    event_type: EventType
    lock_mode: int
    padding3: bytes


@dataclass
class ProfileFile:
    """Container for parsed binary profile"""
    header: Header
    events: List[Event]
    filename: str


@dataclass
class PgTracingSpan:
    """pg_tracing span data"""
    trace_id: str
    parent_id: Optional[str]
    span_id: str
    query_id: Optional[int]
    span_type: str
    span_operation: str
    span_start: datetime
    span_end: datetime
    sql_error_code: Optional[str]
    pid: int
    user_id: int
    db_id: int


@dataclass
class LockEvent:
    """Lock event for timeline tracking"""
    timestamp: int
    pid: int
    xid: int
    event_type: EventType


def parse_binary_file(filename: str) -> ProfileFile:
    """Parse binary profile file"""
    with open(filename, 'rb') as f:
        # Read header (28 bytes)
        header_data = f.read(28)
        if len(header_data) < 28:
            raise ValueError(f"Invalid header size: {len(header_data)}")

        version, pg_version, backend_id, pid, padding, event_count = struct.unpack('<IIIIQI', header_data)

        header = Header(
            version=version,
            pg_version=pg_version,
            backend_id=backend_id,
            pid=pid,
            padding=padding,
            event_count=event_count
        )

        print(f"Parsing {filename}: version={version}, pid={pid}, events={event_count}")

        # Read events (56 bytes each)
        events = []
        for i in range(event_count):
            event_data = f.read(56)
            if len(event_data) < 56:
                print(f"Warning: truncated event at index {i}", file=sys.stderr)
                break

            # Unpack event struct (56 bytes total)
            # Q=8, I=4, I=4, I=4, I=4, Q=8, I=4, I=4, H=2, H=2, I=4, H=2, 6x = 56 bytes
            timestamp_ns, backend_id, pid, xid, padding1, query_id, reloid, blocknum, offnum, padding2, event_type, lock_mode = struct.unpack(
                '<QIIIIQIIHHIH6x', event_data
            )
            padding3 = b''  # Already consumed by 6x in format string

            event = Event(
                timestamp_ns=timestamp_ns,
                backend_id=backend_id,
                pid=pid,
                xid=xid,
                padding1=padding1,
                query_id=query_id,
                reloid=reloid,
                blocknum=blocknum,
                offnum=offnum,
                padding2=padding2,
                event_type=EventType(event_type),
                lock_mode=lock_mode,
                padding3=padding3
            )
            events.append(event)

        return ProfileFile(header=header, events=events, filename=filename)


def query_pg_tracing_spans(conn_str: str) -> List[PgTracingSpan]:
    """Query pg_tracing spans from PostgreSQL"""
    conn = psycopg2.connect(conn_str)
    cursor = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)

    query = """
        SELECT
            trace_id,
            parent_id,
            span_id,
            query_id,
            span_type,
            span_operation,
            span_start,
            span_end,
            sql_error_code,
            pid,
            userid,
            dbid
        FROM pg_tracing_peek_spans
        ORDER BY span_start
    """

    print("\n=== Querying pg_tracing spans ===")
    cursor.execute(query)
    rows = cursor.fetchall()

    spans = []
    for row in rows:
        span = PgTracingSpan(
            trace_id=row['trace_id'],
            parent_id=row['parent_id'],
            span_id=row['span_id'],
            query_id=row['query_id'],
            span_type=row['span_type'],
            span_operation=row['span_operation'],
            span_start=row['span_start'],
            span_end=row['span_end'],
            sql_error_code=row['sql_error_code'],
            pid=row['pid'],
            user_id=row['userid'],
            db_id=row['dbid']
        )
        spans.append(span)

    cursor.close()
    conn.close()

    print(f"Query returned {len(spans)} spans")
    return spans


def build_lock_timeline(profile_files: List[ProfileFile]) -> Dict[str, List[LockEvent]]:
    """Build lock timeline for flow event generation"""
    timeline = {}

    for pf in profile_files:
        for evt in pf.events:
            if evt.event_type in (EventType.LOCK_ACQUIRED, EventType.LOCK_RELEASED):
                lock_key = f"{evt.reloid}:{evt.blocknum}:{evt.offnum}"

                lock_evt = LockEvent(
                    timestamp=evt.timestamp_ns,
                    pid=evt.pid,
                    xid=evt.xid,
                    event_type=evt.event_type
                )

                if lock_key not in timeline:
                    timeline[lock_key] = []
                timeline[lock_key].append(lock_evt)

    # Sort events by timestamp
    for lock_key in timeline:
        timeline[lock_key].sort(key=lambda e: e.timestamp)

    return timeline


def find_contending_hold(lock_key: str, wait_start: int, wait_pid: int,
                        timeline: Dict[str, List[LockEvent]]) -> Optional[Tuple[int, int]]:
    """Find the hold event that overlaps with a wait event"""
    if lock_key not in timeline:
        return None

    events = timeline[lock_key]

    # Find most recent LOCK_ACQUIRED before or at wait_start from different backend
    hold_start = None
    for evt in reversed(events):
        if evt.event_type == EventType.LOCK_ACQUIRED and evt.timestamp <= wait_start and evt.pid != wait_pid:
            hold_start = evt
            break

    if hold_start is None:
        return None

    # Verify hold was still active when wait started
    hold_active = True
    for evt in events:
        if evt.event_type == EventType.LOCK_RELEASED and evt.pid == hold_start.pid and evt.timestamp > hold_start.timestamp:
            if evt.timestamp < wait_start:
                hold_active = False
            break

    if not hold_active:
        return None

    return (hold_start.pid, hold_start.timestamp)


def process_backend_events(pf: ProfileFile, builder: TraceProtoBuilder,
                           lock_timeline: Dict[str, List[LockEvent]],
                           pid_to_backend: Dict[int, int],
                           track_uuids: Dict[str, int]):
    """Process events for a single backend"""
    pid = pf.header.pid

    # Create unique track UUIDs for this backend
    txn_track_key = f"backend_{pid}_txn"
    query_track_key = f"backend_{pid}_query"
    lock_track_key = f"backend_{pid}_lock"

    # Generate UUIDs if not already created (use smaller values for TID compatibility)
    if txn_track_key not in track_uuids:
        track_uuids[txn_track_key] = pid * 10 + 0
    if query_track_key not in track_uuids:
        track_uuids[query_track_key] = pid * 10 + 1
    if lock_track_key not in track_uuids:
        track_uuids[lock_track_key] = pid * 10 + 2

    txn_track_uuid = track_uuids[txn_track_key]
    query_track_uuid = track_uuids[query_track_key]
    lock_track_uuid = track_uuids[lock_track_key]

    # Add track descriptors
    # Transaction track
    packet = builder.add_packet()
    packet.trusted_packet_sequence_id = 1000
    packet.track_descriptor.uuid = txn_track_uuid
    packet.track_descriptor.name = f"Backend {pid} - Transactions"
    packet.track_descriptor.thread.pid = pid
    packet.track_descriptor.thread.tid = txn_track_uuid

    # Query track
    packet = builder.add_packet()
    packet.trusted_packet_sequence_id = 1000
    packet.track_descriptor.uuid = query_track_uuid
    packet.track_descriptor.name = f"Backend {pid} - Queries"
    packet.track_descriptor.thread.pid = pid
    packet.track_descriptor.thread.tid = query_track_uuid

    # Lock track
    packet = builder.add_packet()
    packet.trusted_packet_sequence_id = 1000
    packet.track_descriptor.uuid = lock_track_uuid
    packet.track_descriptor.name = f"Backend {pid} - Locks"
    packet.track_descriptor.thread.pid = pid
    packet.track_descriptor.thread.tid = lock_track_uuid

    # Track state
    txn_starts = {}  # xid -> timestamp
    query_starts = {}  # query_id -> timestamp
    lock_attempt_starts = {}  # lock_key -> timestamp
    lock_hold_starts = {}  # lock_key -> timestamp
    wait_starts = {}  # lock_key -> timestamp
    txn_locks = {}  # xid -> [lock_keys]
    txn_queries = {}  # xid -> [query_ids]

    for evt in pf.events:
        ts = evt.timestamp_ns
        lock_key = f"{evt.reloid}:{evt.blocknum}:{evt.offnum}"

        if evt.event_type == EventType.TXN_BEGIN:
            if evt.xid == 0:
                continue
            packet = builder.add_packet()
            packet.trusted_packet_sequence_id = 1000
            packet.timestamp = ts
            packet.track_event.type = TrackEvent.TYPE_SLICE_BEGIN
            packet.track_event.track_uuid = txn_track_uuid
            packet.track_event.name = f"Txn {evt.xid}"
            packet.track_event.categories.append("transaction")
            txn_starts[evt.xid] = ts

        elif evt.event_type == EventType.TXN_COMMIT:
            if evt.xid == 0:
                continue

            # Close all queries
            if evt.xid in txn_queries:
                for qid in txn_queries[evt.xid]:
                    if qid in query_starts:
                        packet = builder.add_packet()
                        packet.trusted_packet_sequence_id = 1000
                        packet.timestamp = ts
                        packet.track_event.type = TrackEvent.TYPE_SLICE_END
                        packet.track_event.track_uuid = query_track_uuid
                        del query_starts[qid]
                del txn_queries[evt.xid]

            # Close all locks
            if evt.xid in txn_locks:
                for lk in txn_locks[evt.xid]:
                    if lk in wait_starts:
                        packet = builder.add_packet()
                        packet.trusted_packet_sequence_id = 1000
                        packet.timestamp = ts
                        packet.track_event.type = TrackEvent.TYPE_SLICE_END
                        packet.track_event.track_uuid = lock_track_uuid
                        del wait_starts[lk]
                    if lk in lock_attempt_starts:
                        packet = builder.add_packet()
                        packet.trusted_packet_sequence_id = 1000
                        packet.timestamp = ts
                        packet.track_event.type = TrackEvent.TYPE_SLICE_END
                        packet.track_event.track_uuid = lock_track_uuid
                        del lock_attempt_starts[lk]
                    if lk in lock_hold_starts:
                        packet = builder.add_packet()
                        packet.trusted_packet_sequence_id = 1000
                        packet.timestamp = ts
                        packet.track_event.type = TrackEvent.TYPE_SLICE_END
                        packet.track_event.track_uuid = lock_track_uuid
                        del lock_hold_starts[lk]
                del txn_locks[evt.xid]

            # End transaction
            if evt.xid in txn_starts:
                packet = builder.add_packet()
                packet.trusted_packet_sequence_id = 1000
                packet.timestamp = ts
                packet.track_event.type = TrackEvent.TYPE_SLICE_END
                packet.track_event.track_uuid = txn_track_uuid

                # Add instant for commit
                packet = builder.add_packet()
                packet.trusted_packet_sequence_id = 1000
                packet.timestamp = ts
                packet.track_event.type = TrackEvent.TYPE_INSTANT
                packet.track_event.track_uuid = txn_track_uuid
                packet.track_event.name = "COMMIT"
                packet.track_event.categories.append("transaction")

                del txn_starts[evt.xid]

        elif evt.event_type == EventType.TXN_ABORT:
            if evt.xid == 0:
                continue

            # Close all queries
            if evt.xid in txn_queries:
                for qid in txn_queries[evt.xid]:
                    if qid in query_starts:
                        packet = builder.add_packet()
                        packet.trusted_packet_sequence_id = 1000
                        packet.timestamp = ts
                        packet.track_event.type = TrackEvent.TYPE_SLICE_END
                        packet.track_event.track_uuid = query_track_uuid
                        del query_starts[qid]
                del txn_queries[evt.xid]

            # Close all locks
            if evt.xid in txn_locks:
                for lk in txn_locks[evt.xid]:
                    if lk in wait_starts:
                        packet = builder.add_packet()
                        packet.trusted_packet_sequence_id = 1000
                        packet.timestamp = ts
                        packet.track_event.type = TrackEvent.TYPE_SLICE_END
                        packet.track_event.track_uuid = lock_track_uuid
                        del wait_starts[lk]
                    if lk in lock_attempt_starts:
                        packet = builder.add_packet()
                        packet.trusted_packet_sequence_id = 1000
                        packet.timestamp = ts
                        packet.track_event.type = TrackEvent.TYPE_SLICE_END
                        packet.track_event.track_uuid = lock_track_uuid
                        del lock_attempt_starts[lk]
                    if lk in lock_hold_starts:
                        packet = builder.add_packet()
                        packet.trusted_packet_sequence_id = 1000
                        packet.timestamp = ts
                        packet.track_event.type = TrackEvent.TYPE_SLICE_END
                        packet.track_event.track_uuid = lock_track_uuid
                        del lock_hold_starts[lk]
                del txn_locks[evt.xid]

            # End transaction
            if evt.xid in txn_starts:
                packet = builder.add_packet()
                packet.trusted_packet_sequence_id = 1000
                packet.timestamp = ts
                packet.track_event.type = TrackEvent.TYPE_SLICE_END
                packet.track_event.track_uuid = txn_track_uuid

                # Add instant for abort
                packet = builder.add_packet()
                packet.trusted_packet_sequence_id = 1000
                packet.timestamp = ts
                packet.track_event.type = TrackEvent.TYPE_INSTANT
                packet.track_event.track_uuid = txn_track_uuid
                packet.track_event.name = "ABORT"
                packet.track_event.categories.append("transaction")

                del txn_starts[evt.xid]

        elif evt.event_type == EventType.QUERY_START:
            packet = builder.add_packet()
            packet.trusted_packet_sequence_id = 1000
            packet.timestamp = ts
            packet.track_event.type = TrackEvent.TYPE_SLICE_BEGIN
            packet.track_event.track_uuid = query_track_uuid
            packet.track_event.name = f"Query {evt.query_id}"
            packet.track_event.categories.append("query")
            query_starts[evt.query_id] = ts
            if evt.xid not in txn_queries:
                txn_queries[evt.xid] = []
            txn_queries[evt.xid].append(evt.query_id)

        elif evt.event_type == EventType.QUERY_END:
            if evt.query_id in query_starts:
                packet = builder.add_packet()
                packet.trusted_packet_sequence_id = 1000
                packet.timestamp = ts
                packet.track_event.type = TrackEvent.TYPE_SLICE_END
                packet.track_event.track_uuid = query_track_uuid
                del query_starts[evt.query_id]

        elif evt.event_type == EventType.LOCK_ATTEMPT:
            packet = builder.add_packet()
            packet.trusted_packet_sequence_id = 1000
            packet.timestamp = ts
            packet.track_event.type = TrackEvent.TYPE_SLICE_BEGIN
            packet.track_event.track_uuid = lock_track_uuid
            packet.track_event.name = f"Acquiring {evt.reloid}:{evt.blocknum}:{evt.offnum}"
            packet.track_event.categories.append("lock_attempt")
            lock_attempt_starts[lock_key] = ts
            if evt.xid not in txn_locks:
                txn_locks[evt.xid] = []
            txn_locks[evt.xid].append(lock_key)

        elif evt.event_type == EventType.LOCK_ACQUIRED:
            # End attempt
            if lock_key in lock_attempt_starts:
                packet = builder.add_packet()
                packet.trusted_packet_sequence_id = 1000
                packet.timestamp = ts
                packet.track_event.type = TrackEvent.TYPE_SLICE_END
                packet.track_event.track_uuid = lock_track_uuid
                del lock_attempt_starts[lock_key]

            # Start hold
            packet = builder.add_packet()
            packet.trusted_packet_sequence_id = 1000
            packet.timestamp = ts
            packet.track_event.type = TrackEvent.TYPE_SLICE_BEGIN
            packet.track_event.track_uuid = lock_track_uuid
            packet.track_event.name = f"Hold {evt.reloid}:{evt.blocknum}:{evt.offnum}"
            packet.track_event.categories.append("lock_hold")
            lock_hold_starts[lock_key] = ts

        elif evt.event_type == EventType.LOCK_WAIT_START:
            # Start wait slice
            packet = builder.add_packet()
            packet.trusted_packet_sequence_id = 1000
            packet.timestamp = ts
            packet.track_event.type = TrackEvent.TYPE_SLICE_BEGIN
            packet.track_event.track_uuid = lock_track_uuid
            packet.track_event.name = f"Waiting on {evt.reloid}:{evt.blocknum}:{evt.offnum}"
            packet.track_event.categories.append("lock_wait")
            wait_starts[lock_key] = ts

            # Flow events would be added here, but protobuf format is different from JSON

        elif evt.event_type == EventType.LOCK_WAIT_END:
            if lock_key in wait_starts:
                packet = builder.add_packet()
                packet.trusted_packet_sequence_id = 1000
                packet.timestamp = ts
                packet.track_event.type = TrackEvent.TYPE_SLICE_END
                packet.track_event.track_uuid = lock_track_uuid
                del wait_starts[lock_key]

        elif evt.event_type == EventType.LOCK_TIMEOUT:
            if lock_key in lock_attempt_starts:
                packet = builder.add_packet()
                packet.trusted_packet_sequence_id = 1000
                packet.timestamp = ts
                packet.track_event.type = TrackEvent.TYPE_SLICE_END
                packet.track_event.track_uuid = lock_track_uuid
                del lock_attempt_starts[lock_key]

            # Add instant for timeout
            packet = builder.add_packet()
            packet.trusted_packet_sequence_id = 1000
            packet.timestamp = ts
            packet.track_event.type = TrackEvent.TYPE_INSTANT
            packet.track_event.track_uuid = lock_track_uuid
            packet.track_event.name = f"TIMEOUT {evt.reloid}:{evt.blocknum}:{evt.offnum}"
            packet.track_event.categories.append("error")


def process_pg_tracing_spans(spans: List[PgTracingSpan], builder: TraceProtoBuilder, track_uuids: Dict[str, int]):
    """Convert pg_tracing spans to Perfetto events"""

    # Group spans by PID
    spans_by_pid = {}
    for span in spans:
        if span.pid not in spans_by_pid:
            spans_by_pid[span.pid] = []
        spans_by_pid[span.pid].append(span)

    # Create tracks for each PID
    for pid, pid_spans in spans_by_pid.items():
        track_key = f"pg_tracing_{pid}"

        # Generate UUID if not already created (use smaller values for TID compatibility)
        if track_key not in track_uuids:
            track_uuids[track_key] = pid * 10 + 3

        track_uuid = track_uuids[track_key]

        # Add track descriptor
        packet = builder.add_packet()
        packet.trusted_packet_sequence_id = 1000
        packet.track_descriptor.uuid = track_uuid
        packet.track_descriptor.name = f"pg_tracing - Backend {pid}"
        packet.track_descriptor.thread.pid = pid
        packet.track_descriptor.thread.tid = track_uuid

        for span in pid_spans:
            start_ns = int(span.span_start.timestamp() * 1_000_000_000)
            end_ns = int(span.span_end.timestamp() * 1_000_000_000)

            # Begin slice
            packet = builder.add_packet()
            packet.trusted_packet_sequence_id = 1000
            packet.timestamp = start_ns
            packet.track_event.type = TrackEvent.TYPE_SLICE_BEGIN
            packet.track_event.track_uuid = track_uuid
            packet.track_event.name = span.span_operation
            packet.track_event.categories.append("PG")

            # End slice
            packet = builder.add_packet()
            packet.trusted_packet_sequence_id = 1000
            packet.timestamp = end_ns
            packet.track_event.type = TrackEvent.TYPE_SLICE_END
            packet.track_event.track_uuid = track_uuid


def main():
    parser = argparse.ArgumentParser(description='Convert PostgreSQL transaction profiles to Perfetto protobuf format')
    parser.add_argument('-db', '--database', default='postgres://localhost/postgres?host=/tmp',
                       help='PostgreSQL connection string')
    parser.add_argument('-input', '--input-dir', help='Input directory containing *.bin files')
    parser.add_argument('-files', '--input-files', help='Comma-separated list of input files')
    parser.add_argument('-output', '--output', default='trace.pftrace', help='Output Perfetto trace file')
    parser.add_argument('files', nargs='*', help='Input binary files')

    args = parser.parse_args()

    # Collect input files
    input_files = []
    if args.input_dir:
        input_dir = Path(args.input_dir)
        input_files = list(input_dir.glob('txn_profile_*.bin'))
    elif args.input_files:
        input_files = [Path(f.strip()) for f in args.input_files.split(',')]
    elif args.files:
        input_files = [Path(f) for f in args.files]
    else:
        print("Error: No input files specified", file=sys.stderr)
        parser.print_help()
        return 1

    if not input_files:
        print("Error: No input files found", file=sys.stderr)
        return 1

    print(f"Processing {len(input_files)} files...")

    # Parse binary files
    profile_files = []
    for filepath in input_files:
        try:
            pf = parse_binary_file(str(filepath))
            profile_files.append(pf)
        except Exception as e:
            print(f"Error parsing {filepath}: {e}", file=sys.stderr)

    if not profile_files:
        print("Error: No files successfully parsed", file=sys.stderr)
        return 1

    # Build trace
    builder = TraceProtoBuilder()
    track_uuids = {}

    # Build lock timeline
    print("Building lock timeline...")
    lock_timeline = build_lock_timeline(profile_files)

    # Build PID to BackendID mapping
    pid_to_backend = {pf.header.pid: pf.header.backend_id for pf in profile_files}

    # Process lock events
    print("Processing backend events...")
    for pf in profile_files:
        process_backend_events(pf, builder, lock_timeline, pid_to_backend, track_uuids)

    # Query and process pg_tracing spans
    try:
        print("Querying pg_tracing spans...")
        spans = query_pg_tracing_spans(args.database)
        print(f"Processing {len(spans)} pg_tracing spans...")
        process_pg_tracing_spans(spans, builder, track_uuids)
    except Exception as e:
        print(f"Warning: Could not query pg_tracing spans: {e}", file=sys.stderr)
        print("Continuing with lock events only...", file=sys.stderr)

    # Write output
    print(f"Writing trace to {args.output}...")
    with open(args.output, 'wb') as f:
        f.write(builder.serialize())

    print(f"\nTrace written to {args.output}")
    print("View in Perfetto UI: https://ui.perfetto.dev/")
    print(f"Upload the file: {args.output}")

    return 0


if __name__ == '__main__':
    sys.exit(main())
