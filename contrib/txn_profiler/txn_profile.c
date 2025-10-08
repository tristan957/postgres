/*-------------------------------------------------------------------------
 *
 * txn_profile.c
 *	  Transaction profiling ring buffer implementation
 *
 * Copyright (c) 2024, PostgreSQL Global Development Group
 *
 * IDENTIFICATION
 *	  src/backend/utils/misc/txn_profile.c
 *
 *-------------------------------------------------------------------------
 */
#include "postgres.h"

#include <time.h>
#include <unistd.h>
#include <sys/stat.h>

#include "access/xact.h"
#include "miscadmin.h"
#include "port/atomics.h"
#include "portability/instr_time.h"
#include "storage/fd.h"
#include "storage/ipc.h"
#include "storage/procnumber.h"
#include "utils/guc.h"
#include "utils/txn_profile.h"

/* Import hook variable from backend */
extern PGDLLIMPORT txn_profile_emit_hook_type txn_profile_emit_hook;

/* Fixed-size event structure (64 bytes for cache alignment) */
typedef struct TxnProfileEvent
{
	uint64		timestamp_ns;	/* nanosecond timestamp from CLOCK_MONOTONIC */
	uint32		backend_id;		/* Backend ID */
	uint32		pid;			/* Process ID */
	TransactionId xid;			/* Transaction ID */
	uint64		query_id;		/* Query identifier (from pgss) */
	Oid			reloid;			/* Relation OID for lock events */
	BlockNumber blocknum;		/* Block number from ctid */
	OffsetNumber offnum;		/* Offset number from ctid */
	TxnProfileEventType event_type;	/* Type of event */
	uint16		lock_mode;		/* LockTupleMode for lock events */
	uint16		padding[3];		/* Padding to 64 bytes */
} TxnProfileEvent;

/* GUC variables */
bool		txn_profile_enabled = false;
int			txn_profile_buffer_size = 64;	/* KB */
char	   *txn_profile_output_dir = NULL;

/* Ring buffer state */
static TxnProfileEvent *event_buffer = NULL;
static int	max_events = 0;
static int	current_event = 0;
static bool buffer_initialized = false;
static bool shutdown_registered = false;
static struct timespec start_time;

/* Function declarations */
static uint64 get_timestamp_ns(void);
static void txn_profile_write_header(FILE *fp);
static bool txn_profile_is_enabled_internal(void);
static void txn_profile_emit_event_internal(TxnProfileEventType type,
											TransactionId xid,
											uint64 query_id,
											Oid reloid,
											ItemPointer tid,
											LockTupleMode lock_mode);
static void txn_profile_flush_to_file(void);
void txn_profile_init(void);
void txn_profile_shutdown(int code, Datum arg);

/*
 * Get current timestamp in nanoseconds
 */
static uint64
get_timestamp_ns(void)
{
	struct timespec ts;

	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ((uint64) ts.tv_sec * 1000000000ULL) + ts.tv_nsec;
}

/*
 * Initialize profiling system
 */
void
txn_profile_init(void)
{
	if (!txn_profile_enabled || buffer_initialized)
		return;

	/* Calculate max events from buffer size */
	max_events = (txn_profile_buffer_size * 1024) / sizeof(TxnProfileEvent);

	/* Allocate buffer */
	event_buffer = (TxnProfileEvent *) malloc(max_events * sizeof(TxnProfileEvent));
	if (!event_buffer)
	{
		ereport(WARNING,
				(errmsg("failed to allocate transaction profile buffer")));
		txn_profile_enabled = false;
		return;
	}

	memset(event_buffer, 0, max_events * sizeof(TxnProfileEvent));
	current_event = 0;
	buffer_initialized = true;
	clock_gettime(CLOCK_MONOTONIC, &start_time);

	/* Register shutdown callback for THIS backend */
	if (!shutdown_registered)
	{
		on_proc_exit(txn_profile_shutdown, 0);
		shutdown_registered = true;
	}

	ereport(LOG,
			(errmsg("transaction profiling enabled: buffer_size=%dKB, max_events=%d",
					txn_profile_buffer_size, max_events)));
}

/*
 * Shutdown profiling and flush to disk
 */
void
txn_profile_shutdown(int code, Datum arg)
{
	ereport(LOG, (errmsg("txn_profile_shutdown called: buffer_init=%d current_event=%d",
						 buffer_initialized, current_event)));

	if (!buffer_initialized)
		return;

	txn_profile_flush_to_file();

	if (event_buffer)
	{
		free(event_buffer);
		event_buffer = NULL;
	}

	buffer_initialized = false;
}

/*
 * Check if profiling is enabled and initialized (internal version)
 * Note: There's also an inline version in the header for backend code
 */
static bool
txn_profile_is_enabled_internal(void)
{
	/* Auto-initialize on first use if enabled but not yet initialized */
	if (txn_profile_enabled && !buffer_initialized)
		txn_profile_init();

	return txn_profile_enabled && buffer_initialized;
}

/*
 * Internal function to emit a profiling event
 * (called via hook from backend code)
 */
static void
txn_profile_emit_event_internal(TxnProfileEventType type,
								TransactionId xid,
								uint64 query_id,
								Oid reloid,
								ItemPointer tid,
								LockTupleMode lock_mode)
{
	TxnProfileEvent *event;

	if (!txn_profile_is_enabled_internal())
		return;

	/* Debug logging for first few events */
	if (current_event < 5)
		ereport(LOG, (errmsg("txn_profile: emitting event type=%d xid=%u", type, xid)));

	/* Handle buffer overflow by dropping oldest events (ring behavior) */
	if (current_event >= max_events)
		current_event = 0;

	event = &event_buffer[current_event++];

	/* Fill in event data */
	event->timestamp_ns = get_timestamp_ns();
	event->backend_id = (uint32) MyProcNumber;
	event->pid = MyProcPid;
	event->xid = xid;
	event->query_id = query_id;
	event->event_type = type;
	event->lock_mode = (uint16) lock_mode;

	/* Fill in relation and tuple info if provided */
	if (reloid != InvalidOid)
	{
		event->reloid = reloid;
		if (tid)
		{
			event->blocknum = ItemPointerGetBlockNumber(tid);
			event->offnum = ItemPointerGetOffsetNumber(tid);
		}
		else
		{
			event->blocknum = InvalidBlockNumber;
			event->offnum = InvalidOffsetNumber;
		}
	}
	else
	{
		event->reloid = InvalidOid;
		event->blocknum = InvalidBlockNumber;
		event->offnum = InvalidOffsetNumber;
	}
}

/*
 * Write file header with metadata
 */
static void
txn_profile_write_header(FILE *fp)
{
	uint32		version = 1;
	uint32		pg_version = PG_VERSION_NUM;

	uint32 backend_id = (uint32) MyProcNumber;

	fwrite(&version, sizeof(uint32), 1, fp);
	fwrite(&pg_version, sizeof(uint32), 1, fp);
	fwrite(&backend_id, sizeof(uint32), 1, fp);
	fwrite(&MyProcPid, sizeof(uint32), 1, fp);
	fwrite(&start_time.tv_sec, sizeof(time_t), 1, fp);
	fwrite(&start_time.tv_nsec, sizeof(long), 1, fp);
	fwrite(&current_event, sizeof(int), 1, fp);
}

/*
 * Flush events to disk
 */
void
txn_profile_flush_to_file(void)
{
	FILE	   *fp;
	char		filename[MAXPGPATH];
	char		output_dir[MAXPGPATH];
	time_t		now = time(NULL);

	if (!buffer_initialized)
	{
		ereport(LOG, (errmsg("txn_profile: buffer not initialized, cannot flush")));
		return;
	}

	if (current_event == 0)
	{
		ereport(LOG, (errmsg("txn_profile: no events to flush")));
		return;
	}

	/* Determine output directory */
	if (txn_profile_output_dir && txn_profile_output_dir[0])
		snprintf(output_dir, MAXPGPATH, "%s", txn_profile_output_dir);
	else
		snprintf(output_dir, MAXPGPATH, "%s/txn_profiles", DataDir);

	/* Create directory if it doesn't exist */
	mkdir(output_dir, 0700);

	/* Create filename with PID and timestamp */
	snprintf(filename, MAXPGPATH, "%s/txn_profile_%d_%ld.bin",
			 output_dir, MyProcPid, now);

	fp = fopen(filename, "wb");
	if (!fp)
	{
		ereport(WARNING,
				(errmsg("could not open transaction profile output file \"%s\": %m",
						filename)));
		return;
	}

	/* Write header */
	txn_profile_write_header(fp);

	/* Write events */
	fwrite(event_buffer, sizeof(TxnProfileEvent), current_event, fp);

	fclose(fp);

	ereport(LOG,
			(errmsg("transaction profile written to \"%s\" (%d events)",
					filename, current_event)));
}

/*
 * Initialize profiling infrastructure
 * Called from the extension's _PG_init
 */
void
txn_profile_register(void)
{
	/* Define GUC variables */
	DefineCustomBoolVariable("txn_profile.enabled",
							 "Enable transaction profiling",
							 NULL,
							 &txn_profile_enabled,
							 false,
							 PGC_SUSET,
							 0,
							 NULL, NULL, NULL);

	DefineCustomIntVariable("txn_profile.buffer_size",
							"Transaction profile buffer size in KB",
							NULL,
							&txn_profile_buffer_size,
							64,
							8, 1024,
							PGC_SUSET,
							GUC_UNIT_KB,
							NULL, NULL, NULL);

	DefineCustomStringVariable("txn_profile.output_dir",
							   "Directory for transaction profile output files",
							   NULL,
							   &txn_profile_output_dir,
							   NULL,
							   PGC_SUSET,
							   0,
							   NULL, NULL, NULL);

	/* Register our hook to point to internal implementation */
	txn_profile_emit_hook = txn_profile_emit_event_internal;

	ereport(LOG, (errmsg("txn_profile GUCs registered, hook installed")));
}
