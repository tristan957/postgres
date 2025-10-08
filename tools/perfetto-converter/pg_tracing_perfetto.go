package main

import (
	"encoding/json"
	"os"
)

type TraceFile struct {
	TraceEvents []TraceEvent `json:"traceEvents"`
	TraceName   string       `json:"traceName,omitempty"`
}

func spanToTraceEvent(span PgTracingSpan) TraceEvent {
	ts := float64(span.SpanStart.UnixNano()) / 1000.0
	dur := float64(span.SpanEnd.Sub(span.SpanStart).Nanoseconds()) / 1000.0
	args := map[string]any{
		"SpanID":   span.SpanID,
		"TraceID":  span.TraceID,
		"SpanType": span.SpanType,
		"DbID":     span.DbID,
	}
	if span.ParentID != nil {
		args["ParentID"] = *span.ParentID
	}
	if span.QueryID != nil {
		args["QueryID"] = *span.QueryID
	}
	if span.SQLErrorCode != nil {
		args["SQLErrorCode"] = *span.SQLErrorCode
	}

	return TraceEvent{
		Name: span.SpanOperation,
		Cat:  "PG",
		Ph:   "X", // "X" means a Complete event with duration
		Ts:   ts,
		Pid:  uint32(span.PID),
		Tid:  span.UserID,
		Dur:  dur,
		Args: args,
	}
}

// ConvertSpansToEvents converts pg_tracing spans to TraceEvents without writing to disk
func ConvertSpansToEvents(spans []PgTracingSpan) []TraceEvent {
	var traceEvents []TraceEvent
	for _, span := range spans {
		traceEvents = append(traceEvents, spanToTraceEvent(span))
	}
	return traceEvents
}

// WritePerfettoTrace writes a slice of TraceEvents to a Perfetto JSON file
func WritePerfettoTrace(events []TraceEvent, outputPath string) error {
	trace := TraceFile{TraceEvents: events, TraceName: "pgtrace"}
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(trace)
}

func GeneratePerfettoTraceFromSpans(spans []PgTracingSpan, outputPath string) error {
	events := ConvertSpansToEvents(spans)
	return WritePerfettoTrace(events, outputPath)
}
