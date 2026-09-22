package gorchestra

import (
	"sort"
	"time"
)

// RoutineRecord is a bounded-history entry for a routine that reached a
// terminal state. Records are kept in a ring buffer so long-running services
// that create many short-lived workers do not grow memory without bound.
type RoutineRecord struct {
	ID         uint64
	Name       string
	State      RoutineState
	Health     Health
	Restarts   uint64
	StartedAt  time.Time
	FinishedAt time.Time
	Uptime     time.Duration
	Err        string
}

// OverflowRoutineName is the bucket that terminal counters fall back to once
// the configured terminal cardinality limit is reached (see
// WithTerminalCardinalityLimit).
const OverflowRoutineName = "__other__"

// TerminalCount aggregates finished routines by name and final state.
// Cardinality is bounded by the number of distinct routine names and states,
// which makes it safe for Prometheus labels (unlike per-routine-ID labels).
//
// Contract: routine names are expected to be low-cardinality labels (e.g.
// "price-feed", "order-worker"), not per-entity identifiers (e.g.
// "order-918272"). High-cardinality names inflate both memory and Prometheus
// series; use WithTerminalCardinalityLimit to cap the number of distinct
// series and bucket the rest under OverflowRoutineName.
type TerminalCount struct {
	Name  string
	State RoutineState
	Count uint64
}

type historyRing struct {
	buf  []RoutineRecord
	next int
	size int
}

// push appends a record, overwriting the oldest entry once the ring is full.
// The limit is fixed for the lifetime of the orchestrator.
func (h *historyRing) push(rec RoutineRecord, limit int) {
	if limit <= 0 {
		return
	}
	if len(h.buf) != limit {
		h.buf = make([]RoutineRecord, limit)
		h.next = 0
		h.size = 0
	}
	h.buf[h.next] = rec
	h.next = (h.next + 1) % len(h.buf)
	if h.size < len(h.buf) {
		h.size++
	}
}

// snapshot returns the buffered records, oldest first.
func (h *historyRing) snapshot() []RoutineRecord {
	out := make([]RoutineRecord, 0, h.size)
	if h.size == 0 {
		return out
	}
	start := (h.next - h.size + len(h.buf)) % len(h.buf)
	for i := 0; i < h.size; i++ {
		out = append(out, h.buf[(start+i)%len(h.buf)])
	}
	return out
}

// find returns the record with the given routine ID, if still buffered.
func (h *historyRing) find(id uint64) (RoutineRecord, bool) {
	if h.size == 0 {
		return RoutineRecord{}, false
	}
	start := (h.next - h.size + len(h.buf)) % len(h.buf)
	for i := 0; i < h.size; i++ {
		if rec := h.buf[(start+i)%len(h.buf)]; rec.ID == id {
			return rec, true
		}
	}
	return RoutineRecord{}, false
}

func sortTerminalCounts(out []TerminalCount) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].State < out[j].State
	})
}
