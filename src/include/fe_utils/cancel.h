/*-------------------------------------------------------------------------
 *
 * Query cancellation support for frontend code
 *
 *
 * Portions Copyright (c) 1996-2023, PostgreSQL Global Development Group
 * Portions Copyright (c) 1994, Regents of the University of California
 *
 * src/include/fe_utils/cancel.h
 *
 *-------------------------------------------------------------------------
 */

#ifndef CANCEL_H
#define CANCEL_H

#include <signal.h>

#include "libpq-fe.h"

typedef void cancel_callback(void);

extern PGDLLIMPORT volatile sig_atomic_t CancelRequested;

extern void SetCancelConn(PGconn *conn);
extern void ResetCancelConn(void);

/*
 * A callback can be optionally set up to be called at cancellation
 * time.
 */
extern void setup_cancel_handler(cancel_callback *query_pre_cancel_callback,
								 cancel_callback *query_post_cancel_callback);

#endif							/* CANCEL_H */
