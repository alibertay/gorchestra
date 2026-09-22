package gorchestra

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// FuzzRoutineStateTransitions drives a routine through arbitrary sequences of
// lifecycle operations and asserts the core invariant: whatever happens, the
// routine ends in a terminal state and Wait never hangs.
func FuzzRoutineStateTransitions(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3})
	f.Add([]byte{3, 3, 3, 0})
	f.Add([]byte{1, 2})

	f.Fuzz(func(t *testing.T, seq []byte) {
		o := New()
		defer func() { _ = o.Shutdown(time.Second) }()

		r := o.Go(func(ctx context.Context, self *Routine) error {
			<-ctx.Done()
			return ctx.Err()
		})

		for _, b := range seq {
			switch b % 4 {
			case 0:
				r.Kill()
			case 1:
				r.transition(StateRunning)
			case 2:
				r.transition(StateTimedOut)
			case 3:
				r.Beat()
			}
		}
		r.Kill()

		if err := r.Wait(); err != nil && !errors.Is(err, ErrIdleTimeout) {
			t.Fatalf("unexpected wait error: %v", err)
		}
		if !r.State().Terminal() {
			t.Fatalf("state must be terminal after Wait, got %s", r.State())
		}
	})
}

// FuzzLifecyclePublicAPI drives the lifecycle exclusively through exported
// API (Go/TryGo/Kill/Wait/Beat/State/SupervisorState/Restarts/Shutdown) to
// validate behavioral correctness independently from the internal
// transition primitive fuzzed above.
func FuzzLifecyclePublicAPI(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4})
	f.Add([]byte{2, 2, 0})
	f.Add([]byte{4, 4, 1})

	f.Fuzz(func(t *testing.T, seq []byte) {
		o := New(WithHistoryLimit(8))
		defer func() { _ = o.Shutdown(time.Second) }()

		var once sync.Once
		started := make(chan struct{})
		r := o.Go(func(ctx context.Context, self *Routine) error {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return ctx.Err()
		})
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}

		for _, b := range seq {
			switch b % 5 {
			case 0:
				r.Kill()
			case 1:
				r.Beat()
			case 2:
				_ = r.State()
			case 3:
				_ = r.SupervisorState()
			case 4:
				other, err := o.TryGo(func(ctx context.Context, self *Routine) error { return nil })
				if err != nil {
					t.Fatalf("TryGo on an open orchestrator: %v", err)
				}
				if werr := other.Wait(); werr != nil {
					t.Fatalf("child wait: %v", werr)
				}
			}
		}

		r.Kill()
		if err := r.Wait(); err != nil {
			t.Fatalf("unexpected wait error: %v", err)
		}
		if !r.State().Terminal() {
			t.Fatalf("state must be terminal after Wait, got %s", r.State())
		}
		if got := o.State(); got != OrchestratorOpen {
			t.Fatalf("deferred shutdown must not have run yet, got %s", got)
		}
	})
}
