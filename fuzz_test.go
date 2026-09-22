package gorchestra

import (
	"context"
	"errors"
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
