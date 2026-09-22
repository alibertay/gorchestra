package gorchestra

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alibertay/gorchestra/internalutil"
)

const defaultHistoryLimit = 1000

// ErrOrchestratorClosed is returned by TryGo (and surfaced by the Wait of a
// routine returned by Go) when the orchestrator has begun shutting down and
// no longer accepts new routines.
var ErrOrchestratorClosed = errors.New("gorchestra: orchestrator is closed")

// OrchestratorState describes the lifecycle of an Orchestrator.
type OrchestratorState int32

const (
	OrchestratorOpen OrchestratorState = iota
	OrchestratorClosing
	OrchestratorClosed
)

func (s OrchestratorState) String() string {
	switch s {
	case OrchestratorOpen:
		return "OPEN"
	case OrchestratorClosing:
		return "CLOSING"
	case OrchestratorClosed:
		return "CLOSED"
	default:
		return fmt.Sprintf("ORCHESTRATOR_STATE(%d)", int(s))
	}
}

// OrchestratorOption configures an Orchestrator.
type OrchestratorOption func(*Orchestrator)

// WithHistoryLimit bounds how many finished routines are kept in the
// in-memory history ring. 0 disables history. Defaults to 1000.
func WithHistoryLimit(n int) OrchestratorOption {
	return func(o *Orchestrator) {
		if n < 0 {
			n = 0
		}
		o.historyLimit = n
	}
}

// WithTerminalCardinalityLimit limits how many (name, state) terminal
// counters are tracked individually. Once n combinations exist, any new
// name is aggregated into a per-state OverflowRoutineName bucket (state is
// preserved); combinations that are already tracked keep counting
// normally.
//
// The total number of series is therefore not exactly n: it is at most n
// individually tracked combinations plus one overflow bucket per terminal
// state that overflows.
//
// 0 (the default) means unlimited. Routine names should be low-cardinality
// labels by contract; set a limit when names can be dynamic.
func WithTerminalCardinalityLimit(n int) OrchestratorOption {
	return func(o *Orchestrator) {
		if n < 0 {
			n = 0
		}
		o.terminalLimit = n
	}
}

type terminalKey struct {
	name  string
	state RoutineState
}

type Orchestrator struct {
	mu           sync.RWMutex
	lifecycle    OrchestratorState
	routines      map[uint64]*Routine // active routines only
	history       historyRing         // bounded ring of finished routines
	historyLimit  int
	terminal      map[terminalKey]uint64
	terminalLimit int
	wg           sync.WaitGroup
	gen          internalutil.IDGen
	clock        internalutil.Clock
}

func New(opts ...OrchestratorOption) *Orchestrator {
	o := &Orchestrator{
		routines:     make(map[uint64]*Routine),
		historyLimit: defaultHistoryLimit,
		terminal:     make(map[terminalKey]uint64),
		clock:        internalutil.RealClock{},
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// State returns the orchestrator lifecycle state. Once Shutdown begins the
// state moves to CLOSING and no new routines are accepted; after all
// routines have drained it becomes CLOSED.
func (o *Orchestrator) State() OrchestratorState {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.lifecycle
}

// TryGo is like Go but reports an error when the orchestrator is shutting
// down. On rejection it returns a routine that is already finished
// (STOPPED) and whose Wait returns ErrOrchestratorClosed, so callers that
// ignore the error still get a safe handle.
func (o *Orchestrator) TryGo(fn func(ctx context.Context, self *Routine) error, opts ...RoutineOption) (*Routine, error) {
	cfg := defaultRoutineOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	id := o.gen.Next()
	r := newRoutine(id, cfg.Name, cfg.IdleTimeout, cfg.QueueCap, o.clock)

	o.mu.Lock()
	if o.lifecycle != OrchestratorOpen {
		o.mu.Unlock()
		// A terminal routine must have a done context, exactly like a
		// routine whose worker ran and finished.
		r.cancel()
		r.finish(StateStopped, ErrOrchestratorClosed)
		r.markRetired()
		return r, ErrOrchestratorClosed
	}
	o.routines[id] = r
	o.wg.Add(1) // registered before Wait can run: no Add after Wait
	o.mu.Unlock()

	go func() {
		defer o.wg.Done()
		r.run(fn)
		o.retire(r)
		r.markRetired()
	}()

	return r, nil
}

// Go starts a managed routine. If the orchestrator is already shutting down
// the returned routine is finished and its Wait returns
// ErrOrchestratorClosed; use TryGo to handle that case explicitly.
func (o *Orchestrator) Go(fn func(ctx context.Context, self *Routine) error, opts ...RoutineOption) *Routine {
	r, _ := o.TryGo(fn, opts...)
	return r
}

// retire removes a finished routine from the active registry and stores a
// bounded history record plus terminal counters.
func (o *Orchestrator) retire(r *Routine) {
	finished := o.clock.Now()
	s := snapshotOf(r)
	rec := RoutineRecord{
		ID:         r.id,
		Name:       r.name,
		State:      s.State,
		Health:     s.Health,
		Restarts:   s.Restarts,
		StartedAt:  r.createdAt,
		FinishedAt: finished,
		Uptime:     finished.Sub(r.createdAt),
		Err:        s.Err,
	}

	o.mu.Lock()
	delete(o.routines, r.id)
	o.history.push(rec, o.historyLimit)

	key := terminalKey{name: rec.Name, state: rec.State}
	if o.terminalLimit > 0 && len(o.terminal) >= o.terminalLimit {
		if _, exists := o.terminal[key]; !exists {
			key.name = OverflowRoutineName
		}
	}
	o.terminal[key]++
	o.mu.Unlock()
}

// Get returns an active routine by ID. Finished routines live in the bounded
// history instead; use GetRecord for those.
func (o *Orchestrator) Get(id uint64) (*Routine, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	r, ok := o.routines[id]
	return r, ok
}

// List returns the currently active routines.
func (o *Orchestrator) List() []*Routine {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]*Routine, 0, len(o.routines))
	for _, r := range o.routines {
		out = append(out, r)
	}
	return out
}

// History returns a copy of the most recent finished routines (up to the
// configured limit), oldest first.
func (o *Orchestrator) History() []RoutineRecord {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.history.snapshot()
}

// GetRecord looks up a finished routine in the bounded history.
func (o *Orchestrator) GetRecord(id uint64) (RoutineRecord, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.history.find(id)
}

// TerminalCounts aggregates finished routines by name and final state.
func (o *Orchestrator) TerminalCounts() []TerminalCount {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]TerminalCount, 0, len(o.terminal))
	for k, v := range o.terminal {
		out = append(out, TerminalCount{Name: k.name, State: k.state, Count: v})
	}
	sortTerminalCounts(out)
	return out
}

func (o *Orchestrator) KillAll() {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, r := range o.routines {
		r.Kill()
	}
}

// beginShutdown moves OPEN -> CLOSING. It is idempotent and does not wait.
func (o *Orchestrator) beginShutdown() {
	o.mu.Lock()
	if o.lifecycle == OrchestratorOpen {
		o.lifecycle = OrchestratorClosing
	}
	o.mu.Unlock()
}

// Shutdown gracefully stops all routines, waiting up to d in total
// (best-effort). It is safe to call multiple times. Once called, the
// orchestrator stops accepting new routines (TryGo returns
// ErrOrchestratorClosed). If the drain times out the state stays CLOSING
// and a later Shutdown can complete it.
func (o *Orchestrator) Shutdown(d time.Duration) error {
	o.beginShutdown()
	o.KillAll()

	done := make(chan struct{})
	go func() {
		o.wg.Wait()
		close(done)
	}()

	if d <= 0 {
		select {
		case <-done:
			o.markClosed()
			return nil
		default:
			return fmt.Errorf("shutdown timed out after %v", d)
		}
	}

	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		o.markClosed()
		return nil
	case <-t.C:
		return fmt.Errorf("shutdown timed out after %v", d)
	}
}

func (o *Orchestrator) markClosed() {
	o.mu.Lock()
	if o.lifecycle == OrchestratorClosing {
		o.lifecycle = OrchestratorClosed
	}
	o.mu.Unlock()
}
