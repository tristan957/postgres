/*-------------------------------------------------------------------------
 *
 * txn_profiler.c
 *	  Transaction profiler extension - hooks for transaction and query events
 *
 * Copyright (c) 2024, PostgreSQL Global Development Group
 *
 * IDENTIFICATION
 *	  contrib/txn_profiler/txn_profiler.c
 *
 *-------------------------------------------------------------------------
 */
#include "postgres.h"

#include "access/xact.h"
#include "executor/executor.h"
#include "nodes/queryjumble.h"
#include "storage/ipc.h"
#include "utils/guc.h"
#include "utils/txn_profile.h"
#include "txn_profile.h"

PG_MODULE_MAGIC;

/* Import hook variable */
extern PGDLLIMPORT txn_profile_emit_hook_type txn_profile_emit_hook;

/* Hooks */
static ExecutorStart_hook_type prev_ExecutorStart = NULL;
static ExecutorEnd_hook_type prev_ExecutorEnd = NULL;

/* Function prototypes */
void		_PG_init(void);
static void txnprof_xact_callback(XactEvent event, void *arg);
static void txnprof_ExecutorStart(QueryDesc *queryDesc, int eflags);
static void txnprof_ExecutorEnd(QueryDesc *queryDesc);

void
_PG_init(void)
{
	/* Register profiling infrastructure (GUCs and hooks) */
	txn_profile_register();

	/* Register transaction callback */
	RegisterXactCallback(txnprof_xact_callback, NULL);

	/* Hook into executor */
	prev_ExecutorStart = ExecutorStart_hook;
	ExecutorStart_hook = txnprof_ExecutorStart;

	prev_ExecutorEnd = ExecutorEnd_hook;
	ExecutorEnd_hook = txnprof_ExecutorEnd;

	ereport(LOG, (errmsg("txn_profiler extension loaded")));
}

static void
txnprof_xact_callback(XactEvent event, void *arg)
{
	TransactionId xid = GetCurrentTransactionIdIfAny();

	if (!txn_profile_emit_hook)
		return;

	switch (event)
	{
		case XACT_EVENT_COMMIT:
			txn_profile_emit_hook(TXNPROF_TXN_COMMIT, xid, 0,
								  InvalidOid, NULL, 0);
			break;

		case XACT_EVENT_ABORT:
			txn_profile_emit_hook(TXNPROF_TXN_ABORT, xid, 0,
								  InvalidOid, NULL, 0);
			break;

		case XACT_EVENT_PREPARE:
		case XACT_EVENT_PRE_COMMIT:
		case XACT_EVENT_PARALLEL_COMMIT:
		case XACT_EVENT_PARALLEL_ABORT:
		case XACT_EVENT_PRE_PREPARE:
		case XACT_EVENT_PARALLEL_PRE_COMMIT:
			/* Not tracking these for now */
			break;
	}
}

static void
txnprof_ExecutorStart(QueryDesc *queryDesc, int eflags)
{
	TransactionId xid = GetCurrentTransactionIdIfAny();
	uint64		query_id = queryDesc->plannedstmt->queryId;

	if (txn_profile_emit_hook)
		txn_profile_emit_hook(TXNPROF_QUERY_START, xid, query_id,
							  InvalidOid, NULL, 0);

	/* Chain to previous hook */
	if (prev_ExecutorStart)
		prev_ExecutorStart(queryDesc, eflags);
	else
		standard_ExecutorStart(queryDesc, eflags);
}

static void
txnprof_ExecutorEnd(QueryDesc *queryDesc)
{
	TransactionId xid = GetCurrentTransactionIdIfAny();
	uint64		query_id = queryDesc->plannedstmt->queryId;

	/* Chain to previous hook first */
	if (prev_ExecutorEnd)
		prev_ExecutorEnd(queryDesc);
	else
		standard_ExecutorEnd(queryDesc);

	if (txn_profile_emit_hook)
		txn_profile_emit_hook(TXNPROF_QUERY_END, xid, query_id,
							  InvalidOid, NULL, 0);
}
