package gorchestra

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alibertay/gorchestra/internalutil"
)

type Orchestrator struct {
	mu       sync.RWMutex
	routines map[uint64]*Routine
	wg       sync.WaitGroup
	gen      internalutil.IDGen
	clock    internalutil.Clock
}

func New() *Orchestrator {
	return &Orchestrator{
		routines: make(map[uint64]*Routine),
		clock:    internalutil.RealClock{},
	}
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
	}()

	return r
}

func (o *Orchestrator) Get(id uint64) (*Routine, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	r, ok := o.routines[id]
	return r, ok
}

func (o *Orchestrator) List() []*Routine {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]*Routine, 0, len(o.routines))
	for _, r := range o.routines {
		out = append(out, r)
	}
	return out
}

func (o *Orchestrator) KillAll() {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, r := range o.routines {
		r.Kill()
	}
}

// Shutdown gracefully stops all routines, waiting up to d per routine (best-effort).
func (o *Orchestrator) Shutdown(d time.Duration) error {
	o.KillAll()

	done := make(chan struct{})
	go func() {
		o.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(d):
		return fmt.Errorf("shutdown timed out after %v", d)
	}
}
