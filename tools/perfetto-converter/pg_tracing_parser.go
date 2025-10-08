package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type PgTracingSpan struct {
	TraceID             string
	ParentID            *string
	SpanID              string
	QueryID             *int64
	SpanType            string
	SpanOperation       string
	SpanStart           time.Time
	SpanEnd             time.Time
	SQLErrorCode        *string
	PID                 int32
	UserID              uint32 // OID type in PostgreSQL
	DbID                uint32 // OID type in PostgreSQL
	SubxactCount        *int16 // smallint in PostgreSQL
	PlanStartupCost     *float64
	PlanTotalCost       *float64
	PlanRows            *float64
	PlanWidth           *int32
	Rows                *int64
	Nloops              *int64
	SharedBlksHit       *int64
	SharedBlksRead      *int64
	SharedBlksDirtied   *int64
	SharedBlksWritten   *int64
	LocalBlksHit        *int64
	LocalBlksRead       *int64
	LocalBlksDirtied    *int64
	LocalBlksWritten    *int64
	BlkReadTime         *float64
	BlkWriteTime        *float64
	TempBlksRead        *int64
	TempBlksWritten     *int64
	TempBlkReadTime     *float64
	TempBlkWriteTime    *float64
	WalRecords          *int64
	WalFpi              *int64
	WalBytes            *string // numeric in PostgreSQL (can be larger than int64)
	JitFunctions        *int64
	JitGenerationTime   *float64
	JitInliningTime     *float64
	JitOptimizationTime *float64
	JitEmissionTime     *float64
	Startup             *int64 // bigint in PostgreSQL
	Parameters          any    // ARRAY in PostgreSQL
	DeparseInfo         *string
}

type FilterOptions struct {
	StartTime *time.Time
	EndTime   *time.Time
	TraceID   string
	SpanType  string
	PeekOnly  bool
}

func queryPgTracingSpans(ctx context.Context, conn *pgx.Conn, filters FilterOptions) ([]PgTracingSpan, error) {
	// Build WHERE clause
	var whereConditions []string
	var args []interface{}
	argIdx := 1

	if filters.StartTime != nil {
		whereConditions = append(whereConditions, fmt.Sprintf("span_start >= $%d", argIdx))
		args = append(args, *filters.StartTime)
		argIdx++
	}

	if filters.EndTime != nil {
		whereConditions = append(whereConditions, fmt.Sprintf("span_end <= $%d", argIdx))
		args = append(args, *filters.EndTime)
		argIdx++
	}

	if filters.TraceID != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("trace_id = $%d", argIdx))
		args = append(args, filters.TraceID)
		argIdx++
	}

	query := `
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
			dbid,
			subxact_count,
			plan_startup_cost,
			plan_total_cost,
			plan_rows,
			plan_width,
			rows,
			nloops,
			shared_blks_hit,
			shared_blks_read,
			shared_blks_dirtied,
			shared_blks_written,
			local_blks_hit,
			local_blks_read,
			local_blks_dirtied,
			local_blks_written,
			blk_read_time,
			blk_write_time,
			temp_blks_read,
			temp_blks_written,
			temp_blk_read_time,
			temp_blk_write_time,
			wal_records,
			wal_fpi,
			wal_bytes,
			jit_functions,
			jit_generation_time,
			jit_inlining_time,
			jit_optimization_time,
			jit_emission_time,
			startup,
			parameters,
			deparse_info
		FROM pg_tracing_peek_spans
		ORDER BY span_start
	`

	fmt.Printf("\n=== Querying pg_tracing spans ===\n")
	fmt.Printf("Query: %s\n", query)
	fmt.Printf("Args: %v\n", args)

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// Collect all spans in a simple list
	var spans []PgTracingSpan
	for rows.Next() {
		var span PgTracingSpan
		err := rows.Scan(
			&span.TraceID,
			&span.ParentID,
			&span.SpanID,
			&span.QueryID,
			&span.SpanType,
			&span.SpanOperation,
			&span.SpanStart,
			&span.SpanEnd,
			&span.SQLErrorCode,
			&span.PID,
			&span.UserID,
			&span.DbID,
			&span.SubxactCount,
			&span.PlanStartupCost,
			&span.PlanTotalCost,
			&span.PlanRows,
			&span.PlanWidth,
			&span.Rows,
			&span.Nloops,
			&span.SharedBlksHit,
			&span.SharedBlksRead,
			&span.SharedBlksDirtied,
			&span.SharedBlksWritten,
			&span.LocalBlksHit,
			&span.LocalBlksRead,
			&span.LocalBlksDirtied,
			&span.LocalBlksWritten,
			&span.BlkReadTime,
			&span.BlkWriteTime,
			&span.TempBlksRead,
			&span.TempBlksWritten,
			&span.TempBlkReadTime,
			&span.TempBlkWriteTime,
			&span.WalRecords,
			&span.WalFpi,
			&span.WalBytes,
			&span.JitFunctions,
			&span.JitGenerationTime,
			&span.JitInliningTime,
			&span.JitOptimizationTime,
			&span.JitEmissionTime,
			&span.Startup,
			&span.Parameters,
			&span.DeparseInfo,
		)
		if err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}

		spans = append(spans, span)

		// Debug: Log first few spans
		if len(spans) <= 3 {
			queryIDStr := "nil"
			if span.QueryID != nil {
				queryIDStr = fmt.Sprintf("%d", *span.QueryID)
			}
			fmt.Printf("  Row %d: PID=%d, QueryID=%s, Type=%s, Operation=%s, TraceID=%s\n",
				len(spans), span.PID, queryIDStr, span.SpanType, span.SpanOperation, span.TraceID)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration failed: %w", err)
	}

	fmt.Printf("Query returned %d spans\n", len(spans))

	return spans, nil
}
