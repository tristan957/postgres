/*-------------------------------------------------------------------------
 *
 * txn_profile.h
 *	  Local header for transaction profiling extension
 *
 * Copyright (c) 2024, PostgreSQL Global Development Group
 *
 * contrib/txn_profiler/txn_profile.h
 *
 *-------------------------------------------------------------------------
 */
#ifndef TXN_PROFILE_INTERNAL_H
#define TXN_PROFILE_INTERNAL_H

#include "postgres.h"

/* Register the profiling infrastructure (called from extension _PG_init) */
extern void txn_profile_register(void);

#endif							/* TXN_PROFILE_INTERNAL_H */
