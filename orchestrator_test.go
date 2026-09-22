package gorchestra

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOrchestrator_CompletedRoutinesLeaveActiveRegistry(t *testing.T) {
	const n = 10
	o := New(WithHistoryLimit(3))

	for i := 0; i < n; i++ {
		r := o.Go(func(ctx context.Context, self *Routine) error { return nil })
		if err := r.Wait(); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}

	// Retirement happens right after run() returns; Shutdown waits for the
	// orchestrator waitgroup which includes retirement.
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := o.List(); len(got) != 0 {
		t.Fatalf("expected no active routines, got %d", len(got))
	}

	hist := o.History()
	if len(hist) != 3 {
		t.Fatalf("expected bounded history of 3, got %d", len(hist))
	}
	if hist[0].ID != n-2 || hist[2].ID != n {
		t.Fatalf("expected the newest records, got ids %d..%d", hist[0].ID, hist[2].ID)
	}
	for _, rec := range hist {
		if rec.State != StateStopped {
			t.Fatalf("expected STOPPED record, got %s", rec.State)
		}
		if rec.Uptime < 0 {
			t.Fatalf("unexpected negative uptime %v", rec.Uptime)
		}
	}

	var total uint64
	for _, c := range o.TerminalCounts() {
		if c.State != StateStopped {
			t.Fatalf("unexpected terminal state %s", c.State)
		}
		total += c.Count
	}
	if total != n {
		t.Fatalf("expected %d terminal routines, got %d", n, total)
	}
}

func TestOrchestrator_GetRecordFromHistory(t *testing.T) {
	o := New()
	r := o.Go(func(ctx context.Context, self *Routine) error { return nil })
	_ = r.Wait()
	_ = o.Shutdown(time.Second)

	if _, ok := o.Get(r.ID()); ok {
		t.Fatal("finished routine must not remain in the active registry")
	}
	rec, ok := o.GetRecord(r.ID())
	if !ok {
		t.Fatal("expected the routine in history")
	}
	if rec.ID != r.ID() || rec.State != StateStopped {
		t.Fatalf("unexpected record: %+v", rec)
	}
}

func TestOrchestrator_HistoryDisabled(t *testing.T) {
	o := New(WithHistoryLimit(0))
	r := o.Go(func(ctx context.Context, self *Routine) error { return nil })
	_ = r.Wait()
	_ = o.Shutdown(time.Second)

	if got := o.History(); len(got) != 0 {
		t.Fatalf("expected no history, got %d records", len(got))
	}
	if _, ok := o.GetRecord(r.ID()); ok {
		t.Fatal("history is disabled, record must not be found")
	}
}

func TestOrchestrator_TerminalCardinalityLimitBucketsOverflow(t *testing.T) {
	o := New(WithTerminalCardinalityLimit(2))

	for _, name := range []string{"a", "b", "c", "d", "a"} {
		r := o.Go(func(ctx context.Context, self *Routine) error { return nil }, WithName(name))
		if err := r.Wait(); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	want := map[string]uint64{"a": 2, "b": 1, OverflowRoutineName: 2}
	counts := o.TerminalCounts()
	if len(counts) != len(want) {
		t.Fatalf("expected %d terminal series, got %d: %+v", len(want), len(counts), counts)
	}
	for _, c := range counts {
		if c.State != StateStopped {
			t.Fatalf("unexpected state %s", c.State)
		}
		if want[c.Name] != c.Count {
			t.Fatalf("name %q: expected %d, got %d", c.Name, want[c.Name], c.Count)
		}
	}
}

func TestOrchestrator_TerminalCardinalityUnlimitedByDefault(t *testing.T) {
	o := New()
	for _, name := range []string{"a", "b", "c", "d"} {
		r := o.Go(func(ctx context.Context, self *Routine) error { return nil }, WithName(name))
		_ = r.Wait()
	}
	_ = o.Shutdown(time.Second)

	if got := len(o.TerminalCounts()); got != 4 {
		t.Fatalf("expected 4 distinct series, got %d", got)
	}
}

func TestOrchestrator_ShutdownTimeout(t *testing.T) {
	o := New()
	started := make(chan struct{})
	r := o.Go(func(ctx context.Context, self *Routine) error {
		close(started)
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	<-started

	if err := o.Shutdown(30 * time.Millisecond); err == nil {
		t.Fatal("expected a shutdown timeout error")
	}
	if err := r.Wait(); err != nil {
		t.Fatalf("worker should complete naturally, got %v", err)
	}
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("second shutdown should succeed, got %v", err)
	}
}

func TestOrchestrator_MultipleShutdown(t *testing.T) {
	o := New()
	started := make(chan struct{})
	r := o.Go(func(ctx context.Context, self *Routine) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started

	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}

func TestOrchestrator_LifecycleStates(t *testing.T) {
	o := New()
	if got := o.State(); got != OrchestratorOpen {
		t.Fatalf("expected OPEN, got %s", got)
	}

	if _, err := o.TryGo(func(ctx context.Context, self *Routine) error { return nil }); err != nil {
		t.Fatalf("TryGo on an open orchestrator: %v", err)
	}
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := o.State(); got != OrchestratorClosed {
		t.Fatalf("expected CLOSED, got %s", got)
	}
}

func TestOrchestrator_TryGoAfterShutdownRejected(t *testing.T) {
	o := New()
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	r, err := o.TryGo(func(ctx context.Context, self *Routine) error { return nil })
	if !errors.Is(err, ErrOrchestratorClosed) {
		t.Fatalf("expected ErrOrchestratorClosed, got %v", err)
	}
	if r == nil {
		t.Fatal("rejection must still return a safe routine handle")
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("rejected routine must be STOPPED, got %s", got)
	}
	if werr := r.Wait(); !errors.Is(werr, ErrOrchestratorClosed) {
		t.Fatalf("Wait must surface ErrOrchestratorClosed, got %v", werr)
	}
}

func TestOrchestrator_GoAfterShutdownDoesNotRun(t *testing.T) {
	o := New()
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	var called atomic.Bool
	r := o.Go(func(ctx context.Context, self *Routine) error {
		called.Store(true)
		return nil
	})
	if called.Load() {
		t.Fatal("worker must not run after shutdown")
	}
	if err := r.Wait(); !errors.Is(err, ErrOrchestratorClosed) {
		t.Fatalf("expected ErrOrchestratorClosed from Wait, got %v", err)
	}
}

func TestOrchestrator_ShutdownTimeoutStaysClosing(t *testing.T) {
	o := New()
	started := make(chan struct{})
	r := o.Go(func(ctx context.Context, self *Routine) error {
		close(started)
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	<-started

	if err := o.Shutdown(20 * time.Millisecond); err == nil {
		t.Fatal("expected a shutdown timeout error")
	}
	if got := o.State(); got != OrchestratorClosing {
		t.Fatalf("expected CLOSING after a timed-out shutdown, got %s", got)
	}
	if _, err := o.TryGo(func(ctx context.Context, self *Routine) error { return nil }); !errors.Is(err, ErrOrchestratorClosed) {
		t.Fatalf("CLOSING orchestrator must reject new routines, got %v", err)
	}

	if err := r.Wait(); err != nil {
		t.Fatalf("worker should complete naturally, got %v", err)
	}
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("second shutdown should complete: %v", err)
	}
	if got := o.State(); got != OrchestratorClosed {
		t.Fatalf("expected CLOSED, got %s", got)
	}
}

func TestOrchestrator_ConcurrentTryGoAndShutdown(t *testing.T) {
	o := New()
	const workers = 64

	var accepted atomic.Int64
	var readyOnce sync.Once
	ready := make(chan struct{})
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := o.TryGo(func(ctx context.Context, self *Routine) error { return nil })
			if err != nil {
				return
			}
			accepted.Add(1)
			readyOnce.Do(func() { close(ready) })
			_ = r.Wait()
		}()
	}
	close(start)

	// Shutdown starts while the remaining TryGo calls are still in flight.
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("no routine was accepted before shutdown")
	}
	if err := o.Shutdown(2 * time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	wg.Wait()

	if accepted.Load() == 0 {
		t.Fatal("expected at least some routines to be accepted before shutdown")
	}
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("final shutdown: %v", err)
	}
	if got := o.State(); got != OrchestratorClosed {
		t.Fatalf("expected CLOSED, got %s", got)
	}
}

func TestOrchestrator_KillAll_StopsEveryRoutine(t *testing.T) {
	o := New()
	const n = 5

	rs := make([]*Routine, n)
	var started sync.WaitGroup
	started.Add(n)
	for i := range rs {
		rs[i] = o.Go(func(ctx context.Context, self *Routine) error {
			started.Done()
			<-ctx.Done()
			return ctx.Err()
		})
	}
	started.Wait()

	o.KillAll()
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	for i, r := range rs {
		if got := r.State(); got != StateStopped {
			t.Fatalf("routine %d expected STOPPED, got %s", i, got)
		}
	}
}
