package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// EventType matches TxnProfileEventType from C
type EventType uint32

const (
	TxnBegin EventType = iota
	TxnCommit
	TxnAbort
	QueryStart
	QueryEnd
	LockAttempt
	LockAcquired
	LockWaitStart
	LockWaitEnd
	LockReleased
	LockTimeout
)

func (e EventType) String() string {
	names := []string{
		"TxnBegin", "TxnCommit", "TxnAbort",
		"QueryStart", "QueryEnd",
		"LockAttempt", "LockAcquired", "LockWaitStart", "LockWaitEnd",
		"LockReleased", "LockTimeout",
	}
	if int(e) < len(names) {
		return names[e]
	}
	return fmt.Sprintf("Unknown(%d)", e)
}

// Header matches the file header written in C
type Header struct {
	Version    uint32
	PGVersion  uint32
	BackendID  uint32
	PID        uint32
	Padding    uint64 // Reserved (was start_time)
	EventCount int32
}

// Event matches TxnProfileEvent from C (56 bytes with C struct packing)
// Note: C compiler adds padding for alignment
type Event struct {
	TimestampNs uint64    // offset 0, 8 bytes
	BackendID   uint32    // offset 8, 4 bytes
	PID         uint32    // offset 12, 4 bytes
	Xid         uint32    // offset 16, 4 bytes
	Padding1    uint32    // offset 20, 4 bytes (implicit C padding)
	QueryID     uint64    // offset 24, 8 bytes
	Reloid      uint32    // offset 32, 4 bytes
	Blocknum    uint32    // offset 36, 4 bytes
	Offnum      uint16    // offset 40, 2 bytes
	Padding2    uint16    // offset 42, 2 bytes (implicit C padding)
	EventType   EventType // offset 44, 4 bytes (enum is int32)
	LockMode    uint16    // offset 48, 2 bytes
	Padding3    [6]byte   // offset 50, 6 bytes
}

type ProfileFile struct {
	Header Header
	Events []Event
}

func ParseProfileFile(filename string) (*ProfileFile, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	pf := &ProfileFile{}

	// Read header
	if err := binary.Read(f, binary.LittleEndian, &pf.Header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	fmt.Printf("Parsing %s: version=%d, pid=%d, events=%d\n",
		filename, pf.Header.Version, pf.Header.PID, pf.Header.EventCount)

	// Read events
	pf.Events = make([]Event, pf.Header.EventCount)
	for i := 0; i < int(pf.Header.EventCount); i++ {
		if err := binary.Read(f, binary.LittleEndian, &pf.Events[i]); err != nil {
			if err == io.EOF {
				pf.Events = pf.Events[:i]
				break
			}
			return nil, fmt.Errorf("read event %d: %w", i, err)
		}
	}

	return pf, nil
}
