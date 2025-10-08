/*-------------------------------------------------------------------------
 *
 * txn_profile_hook.c
 *	  Hook variable for transaction profiling
 *
 * Copyright (c) 2024, PostgreSQL Global Development Group
 *
 * IDENTIFICATION
 *	  src/backend/utils/misc/txn_profile_hook.c
 *
 *-------------------------------------------------------------------------
 */
#include "postgres.h"

#include "utils/txn_profile.h"

/* Hook for transaction profiling - set by extension */
txn_profile_emit_hook_type txn_profile_emit_hook = NULL;
