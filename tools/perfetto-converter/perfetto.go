package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Perfetto JSON trace format structures
type TraceEvent struct {
	Name  string         `json:"name,omitempty"`
	Cat   string         `json:"cat,omitempty"`
	Ph    string         `json:"ph"` // Event phase: B, E, X, s, f, etc.
	Ts    float64        `json:"ts"` // Timestamp in microseconds
	Pid   uint32         `json:"pid"`
	Tid   uint32         `json:"tid"`
	Dur   float64        `json:"dur,omitempty"` // Duration in microseconds
	Args  map[string]any `json:"args,omitempty"`
	Id    string         `json:"id,omitempty"` // For flow events
	Bp    string         `json:"bp,omitempty"` // Binding point for flows
	Scope string         `json:"scope,omitempty"`
}

type Trace struct {
	TraceEvents []TraceEvent `json:"traceEvents"`
}

// LockEvent tracks lock operations across all backends
type LockEvent struct {
	Timestamp uint64
	PID       uint32
	Xid       uint32
	EventType EventType
}

// LockTimeline tracks lock events for generating flow events
type LockTimeline struct {
	// Map from lock key (reloid:block:offset) to chronological events
	Events map[string][]LockEvent
}

func buildLockTimeline(files []*ProfileFile) *LockTimeline {
	timeline := &LockTimeline{
		Events: make(map[string][]LockEvent),
	}

	// Collect all lock-related events across all backends
	for _, pf := range files {
		for _, evt := range pf.Events {
			// Only track lock acquisition and release events
			if evt.EventType == LockAcquired || evt.EventType == LockReleased {
				lockKey := fmt.Sprintf("%d:%d:%d", evt.Reloid, evt.Blocknum, evt.Offnum)
				timeline.Events[lockKey] = append(timeline.Events[lockKey], LockEvent{
					Timestamp: evt.TimestampNs,
					PID:       pf.Header.PID,
					Xid:       evt.Xid,
					EventType: evt.EventType,
				})
			}
		}
	}

	// Sort events by timestamp for each lock
	for key := range timeline.Events {
		events := timeline.Events[key]
		// Simple bubble sort (fine for small event counts per lock)
		for i := 0; i < len(events); i++ {
			for j := i + 1; j < len(events); j++ {
				if events[i].Timestamp > events[j].Timestamp {
					events[i], events[j] = events[j], events[i]
				}
			}
		}
	}

	return timeline
}

func GeneratePerfettoTrace(files []*ProfileFile, outputPath string) error {
	trace := &Trace{
		TraceEvents: make([]TraceEvent, 0, 10000),
	}

	// Build global lock timeline for flow events
	lockTimeline := buildLockTimeline(files)

	// Process each file (each backend)
	for _, pf := range files {
		processBackendEvents(pf, trace, lockTimeline)
	}

	// Write JSON
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(trace); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}

	fmt.Printf("Generated Perfetto trace with %d events\n", len(trace.TraceEvents))
	return nil
}

func processBackendEvents(pf *ProfileFile, trace *Trace, lockTimeline *LockTimeline) {
	pid := pf.Header.PID
	baseTid := pf.Header.BackendID

	// Use different track IDs for different event types to avoid nesting issues
	txnTid := baseTid*10 + 0   // Transaction track
	queryTid := baseTid*10 + 1 // Query track
	lockTid := baseTid*10 + 2  // Lock track

	// Debug: Show event sequence for all backends with lock events
	hasLockEvents := false
	for _, evt := range pf.Events {
		if evt.EventType >= LockAttempt && evt.EventType <= LockTimeout {
			hasLockEvents = true
			break
		}
	}
	if hasLockEvents {
		fmt.Printf("\n=== Event sequence for Backend PID %d ===\n", pid)
		for i, evt := range pf.Events {
			ts := float64(evt.TimestampNs) / 1e9 // seconds
			fmt.Printf("  %2d: %-15s t=%10.3fs xid=%d", i, evt.EventType, ts, evt.Xid)
			if evt.EventType >= LockAttempt && evt.EventType <= LockTimeout {
				fmt.Printf(" ctid=(%d,%d)", evt.Blocknum, evt.Offnum)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	// Track open events for duration calculation
	type PendingEvent struct {
		Event    Event
		StartTs  float64
		Category string // Track category for END events
		Name     string // Track name for debugging
	}

	// For lock attempt->acquire tracking
	lockAttemptStarts := make(map[string]*PendingEvent) // key: reloid:block:offset
	// For lock hold tracking (acquire->release at transaction end)
	lockHoldStarts := make(map[string]*PendingEvent) // key: reloid:block:offset
	// For wait tracking
	waitStarts := make(map[string]*PendingEvent)
	// For transaction tracking
	txnStarts := make(map[uint32]*PendingEvent) // key: xid
	// Track which locks each transaction holds (for closing at commit/abort)
	txnLocks := make(map[uint32][]string) // key: xid, value: list of lock keys
	// For query tracking
	queryStarts := make(map[uint64]*PendingEvent) // key: query_id

	// Add track name metadata for each track
	trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
		Name: "thread_name",
		Ph:   "M", // Metadata
		Pid:  pid,
		Tid:  txnTid,
		Args: map[string]interface{}{
			"name": fmt.Sprintf("Backend %d - Transactions", pid),
		},
	})
	trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
		Name: "thread_name",
		Ph:   "M",
		Pid:  pid,
		Tid:  queryTid,
		Args: map[string]interface{}{
			"name": fmt.Sprintf("Backend %d - Queries", pid),
		},
	})
	trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
		Name: "thread_name",
		Ph:   "M",
		Pid:  pid,
		Tid:  lockTid,
		Args: map[string]interface{}{
			"name": fmt.Sprintf("Backend %d - Locks", pid),
		},
	})

	for _, evt := range pf.Events {
		ts := float64(evt.TimestampNs) / 1000.0 // Convert ns to µs

		lockKey := fmt.Sprintf("%d:%d:%d", evt.Reloid, evt.Blocknum, evt.Offnum)

		// Helper to emit END event with proper category and track
		emitEnd := func(cat string, trackTid uint32) {
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Ph:  "E",
				Cat: cat,
				Ts:  ts,
				Pid: pid,
				Tid: trackTid,
			})
		}

		// Helper to determine which track to use based on category
		getTid := func(cat string) uint32 {
			if cat == "transaction" {
				return txnTid
			} else if cat == "query" {
				return queryTid
			} else {
				return lockTid // lock_attempt, lock_hold, lock_wait
			}
		}

		switch evt.EventType {
		case TxnBegin:
			// Skip xid=0 (InvalidTransactionId) - these are read-only implicit transactions
			if evt.Xid == 0 {
				break
			}
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Name: fmt.Sprintf("Txn %d", evt.Xid),
				Cat:  "transaction",
				Ph:   "B", // Begin
				Ts:   ts,
				Pid:  pid,
				Tid:  txnTid,
				Args: map[string]interface{}{
					"xid": evt.Xid,
				},
			})
			txnStarts[evt.Xid] = &PendingEvent{Event: evt, StartTs: ts, Category: "transaction"}

		case TxnCommit:
			// Skip xid=0 (InvalidTransactionId) - these are read-only implicit transactions
			if evt.Xid == 0 {
				break
			}

			// Close all locks held by this transaction
			if locks, exists := txnLocks[evt.Xid]; exists {
				for _, lockKey := range locks {
					// Close any pending wait
					if pending, waitExists := waitStarts[lockKey]; waitExists {
						emitEnd(pending.Category, getTid(pending.Category))
						delete(waitStarts, lockKey)
					}
					// Close any pending lock attempt
					if pending, attemptExists := lockAttemptStarts[lockKey]; attemptExists {
						emitEnd(pending.Category, getTid(pending.Category))
						delete(lockAttemptStarts, lockKey)
					}
					// Close any held lock
					if pending, holdExists := lockHoldStarts[lockKey]; holdExists {
						// End the hold slice for this lock
						emitEnd(pending.Category, getTid(pending.Category))
						delete(lockHoldStarts, lockKey)
					}
				}
				delete(txnLocks, evt.Xid)
			}

			// End transaction slice with COMMIT indicator
			if pending, exists := txnStarts[evt.Xid]; exists {
				emitEnd(pending.Category, getTid(pending.Category))
			}
			// Add instant event to show commit
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Name: "COMMIT",
				Cat:  "transaction",
				Ph:   "i", // Instant
				Ts:   ts,
				Pid:  pid,
				Tid:  txnTid,
				Args: map[string]interface{}{
					"xid":    evt.Xid,
					"result": "committed",
				},
			})
			delete(txnStarts, evt.Xid)

		case TxnAbort:
			// Skip xid=0 (InvalidTransactionId) - these are read-only implicit transactions
			if evt.Xid == 0 {
				break
			}

			// Close all locks held by this transaction (including any we're waiting on)
			if locks, exists := txnLocks[evt.Xid]; exists {
				for _, lockKey := range locks {
					// Close any pending wait
					if pending, waitExists := waitStarts[lockKey]; waitExists {
						emitEnd(pending.Category, getTid(pending.Category))
						delete(waitStarts, lockKey)
					}
					// Close any pending lock attempt
					if pending, attemptExists := lockAttemptStarts[lockKey]; attemptExists {
						emitEnd(pending.Category, getTid(pending.Category))
						delete(lockAttemptStarts, lockKey)
					}
					// Close any held lock
					if pending, holdExists := lockHoldStarts[lockKey]; holdExists {
						emitEnd(pending.Category, getTid(pending.Category))
						delete(lockHoldStarts, lockKey)
					}
				}
				delete(txnLocks, evt.Xid)
			}

			// End transaction slice with ABORT indicator
			if pending, exists := txnStarts[evt.Xid]; exists {
				emitEnd(pending.Category, getTid(pending.Category))
			}
			// Add instant event to show abort (critical for deadlock visualization)
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Name: "ABORT",
				Cat:  "transaction",
				Ph:   "i", // Instant
				Ts:   ts,
				Pid:  pid,
				Tid:  txnTid,
				Args: map[string]interface{}{
					"xid":    evt.Xid,
					"result": "aborted",
				},
			})
			delete(txnStarts, evt.Xid)

		case QueryStart:
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Name: fmt.Sprintf("Query %d", evt.QueryID),
				Cat:  "query",
				Ph:   "B",
				Ts:   ts,
				Pid:  pid,
				Tid:  queryTid,
				Args: map[string]interface{}{
					"query_id": evt.QueryID,
					"xid":      evt.Xid,
				},
			})
			queryStarts[evt.QueryID] = &PendingEvent{Event: evt, StartTs: ts, Category: "query"}

		case QueryEnd:
			if pending, exists := queryStarts[evt.QueryID]; exists {
				emitEnd(pending.Category, getTid(pending.Category))
				delete(queryStarts, evt.QueryID)
			}

		case LockAttempt:
			// Begin slice for lock ATTEMPT (will end at LockAcquired or LockTimeout)
			// This slice represents "trying to get the lock"
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Name: fmt.Sprintf("Acquiring %s:%d:%d",
					getRelName(evt.Reloid), evt.Blocknum, evt.Offnum),
				Cat: "lock_attempt",
				Ph:  "B", // Begin
				Ts:  ts,
				Pid: pid,
				Tid: lockTid,
				Args: map[string]interface{}{
					"xid":    evt.Xid,
					"mode":   evt.LockMode,
					"reloid": evt.Reloid,
					"ctid":   fmt.Sprintf("(%d,%d)", evt.Blocknum, evt.Offnum),
				},
			})
			lockAttemptStarts[lockKey] = &PendingEvent{
				Event:    evt,
				StartTs:  ts,
				Category: "lock_attempt",
			}
			// Track this lock for this transaction
			txnLocks[evt.Xid] = append(txnLocks[evt.Xid], lockKey)

		case LockAcquired:
			// End the lock ATTEMPT slice
			if pending, exists := lockAttemptStarts[lockKey]; exists {
				emitEnd(pending.Category, getTid(pending.Category))
				delete(lockAttemptStarts, lockKey)
			}

			// Start the lock HOLD slice (will end at LockReleased)
			// This slice represents "holding the lock"
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Name: fmt.Sprintf("Hold %s:%d:%d",
					getRelName(evt.Reloid), evt.Blocknum, evt.Offnum),
				Cat: "lock_hold",
				Ph:  "B",
				Ts:  ts,
				Pid: pid,
				Tid: lockTid,
				Args: map[string]interface{}{
					"xid":  evt.Xid,
					"mode": evt.LockMode,
					"ctid": fmt.Sprintf("(%d,%d)", evt.Blocknum, evt.Offnum),
				},
			})
			lockHoldStarts[lockKey] = &PendingEvent{
				Event:    evt,
				StartTs:  ts,
				Category: "lock_hold",
			}

		case LockWaitStart:
			// Begin wait slice - this shows blocked time
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Name: fmt.Sprintf("Waiting on %s:%d:%d",
					getRelName(evt.Reloid), evt.Blocknum, evt.Offnum),
				Cat: "lock_wait",
				Ph:  "B",
				Ts:  ts,
				Pid: pid,
				Tid: lockTid,
				Args: map[string]interface{}{
					"xid":  evt.Xid,
					"ctid": fmt.Sprintf("(%d,%d)", evt.Blocknum, evt.Offnum),
				},
			})
			waitStarts[lockKey] = &PendingEvent{
				Event:    evt,
				StartTs:  ts,
				Category: "lock_wait",
			}

		case LockWaitEnd:
			// End wait slice
			if pending, exists := waitStarts[lockKey]; exists {
				emitEnd(pending.Category, getTid(pending.Category))
				delete(waitStarts, lockKey)
			}

		// LockReleased: No longer emitted from C code - locks held until transaction end

		case LockTimeout:
			// End the lock attempt slice (timeout = failed acquisition)
			if pending, exists := lockAttemptStarts[lockKey]; exists {
				emitEnd(pending.Category, getTid(pending.Category))
				delete(lockAttemptStarts, lockKey)
			}

			// Add instant event to highlight the timeout
			trace.TraceEvents = append(trace.TraceEvents, TraceEvent{
				Name: fmt.Sprintf("TIMEOUT %s:%d:%d",
					getRelName(evt.Reloid), evt.Blocknum, evt.Offnum),
				Cat: "error",
				Ph:  "i", // Instant event
				Ts:  ts,
				Pid: pid,
				Tid: lockTid,
				Args: map[string]interface{}{
					"xid":  evt.Xid,
					"ctid": fmt.Sprintf("(%d,%d)", evt.Blocknum, evt.Offnum),
				},
			})
		}
	}
}

func getRelName(oid uint32) string {
	// For now, just return OID. Could enhance to lookup actual relation name
	return fmt.Sprintf("rel_%d", oid)
}
