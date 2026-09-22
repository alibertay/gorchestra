package gorchestra

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alibertay/gorchestra/internalutil"
)

const defaultHistoryLimit = 1000

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

type terminalKey struct {
	name  string
	state RoutineState
}

type Orchestrator struct {
	mu           sync.RWMutex
	routines     map[uint64]*Routine // active routines only
	history      historyRing         // bounded ring of finished routines
	historyLimit int
	terminal     map[terminalKey]uint64
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

func (o *Orchestrator) Go(fn func(ctx context.Context, self *Routine) error, opts ...RoutineOption) *Routine {
	cfg := defaultRoutineOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	id := o.gen.Next()
	r := newRoutine(id, cfg.Name, cfg.IdleTimeout, cfg.QueueCap, o.clock)

	o.mu.Lock()
	o.routines[id] = r
	o.mu.Unlock()

	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		r.run(fn)
		o.retire(r)
	}()

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
	o.terminal[terminalKey{name: rec.Name, state: rec.State}]++
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

// Shutdown gracefully stops all routines, waiting up to d in total
// (best-effort). It is safe to call multiple times.
func (o *Orchestrator) Shutdown(d time.Duration) error {
	o.KillAll()

	done := make(chan struct{})
	go func() {
		o.wg.Wait()
		close(done)
	}()

	if d <= 0 {
		select {
		case <-done:
			return nil
		default:
			return fmt.Errorf("shutdown timed out after %v", d)
		}
	}

	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return nil
	case <-t.C:
		return fmt.Errorf("shutdown timed out after %v", d)
	}
}
