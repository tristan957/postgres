/*-------------------------------------------------------------------------
 *
 * txn_profile.h
 *	  Transaction profiling hooks for extensions
 *
 * Copyright (c) 2024, PostgreSQL Global Development Group
 *
 * src/include/utils/txn_profile.h
 *
 *-------------------------------------------------------------------------
 */
#ifndef TXN_PROFILE_H
#define TXN_PROFILE_H

#include "postgres.h"
#include "access/transam.h"
#include "storage/itemptr.h"
#include "nodes/lockoptions.h"

/* Event types that we track */
typedef enum TxnProfileEventType
{
	TXNPROF_TXN_BEGIN,
	TXNPROF_TXN_COMMIT,
	TXNPROF_TXN_ABORT,
	TXNPROF_QUERY_START,
	TXNPROF_QUERY_END,
	TXNPROF_LOCK_ATTEMPT,
	TXNPROF_LOCK_ACQUIRED,
	TXNPROF_LOCK_WAIT_START,
	TXNPROF_LOCK_WAIT_END,
	TXNPROF_LOCK_RELEASED,
	TXNPROF_LOCK_TIMEOUT
} TxnProfileEventType;

/* Hook type for profiling events */
typedef void (*txn_profile_emit_hook_type) (TxnProfileEventType type,
											 TransactionId xid,
											 uint64 query_id,
											 Oid reloid,
											 ItemPointer tid,
											 LockTupleMode lock_mode);

/* Hook variable - set by extension */
extern PGDLLIMPORT txn_profile_emit_hook_type txn_profile_emit_hook;

/* Inline function to emit events if hook is set */
static inline void
txn_profile_emit_event(TxnProfileEventType type,
					   TransactionId xid,
					   uint64 query_id,
					   Oid reloid,
					   ItemPointer tid,
					   LockTupleMode lock_mode)
{
	if (txn_profile_emit_hook)
		txn_profile_emit_hook(type, xid, query_id, reloid, tid, lock_mode);
}

/* Check if profiling is enabled */
static inline bool
txn_profile_is_enabled(void)
{
	return txn_profile_emit_hook != NULL;
}

#endif							/* TXN_PROFILE_H */
