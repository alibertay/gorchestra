package gorchestra

import (
	"context"
	"sync"
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
