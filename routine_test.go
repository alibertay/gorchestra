package gorchestra

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRoutine_NormalCompletion_Stopped(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.Go(func(ctx context.Context, self *Routine) error { return nil })

	if err := r.Wait(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}

func TestRoutine_ErrorCompletion_StoppedWithError(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	boom := errors.New("boom")
	r := o.Go(func(ctx context.Context, self *Routine) error { return boom })

	if err := r.Wait(); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}

func TestRoutine_Kill_Stopped(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	started := make(chan struct{})
	r := o.Go(func(ctx context.Context, self *Routine) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started

	r.Kill()
	if err := r.Wait(); err != nil {
		t.Fatalf("kill should not surface an error, got %v", err)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}

func TestRoutine_KillAfterCompletion_StateStaysStopped(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.Go(func(ctx context.Context, self *Routine) error { return nil })
	_ = r.Wait()

	r.Kill() // must be a no-op on a terminal routine
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED to remain terminal, got %s", got)
	}
}

func TestRoutine_IdleTimeout_TimedOut(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.Go(func(ctx context.Context, self *Routine) error {
		<-ctx.Done()
		return ctx.Err()
	}, WithIdleTimeout(150*time.Millisecond))

	err := r.Wait()
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("expected ErrIdleTimeout, got %v", err)
	}
	if got := r.State(); got != StateTimedOut {
		t.Fatalf("expected TIMED_OUT, got %s", got)
	}
}

func TestRoutine_TimeoutThenKill_StateStaysTimedOut(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.Go(func(ctx context.Context, self *Routine) error {
		<-ctx.Done()
		return ctx.Err()
	}, WithIdleTimeout(100*time.Millisecond))

	_ = r.Wait()
	r.Kill()
	if got := r.State(); got != StateTimedOut {
		t.Fatalf("expected TIMED_OUT to remain terminal, got %s", got)
	}
}

func TestRoutine_Panic_Panicked(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.Go(func(ctx context.Context, self *Routine) error {
		panic("boom")
	})

	err := r.Wait()
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected panic error containing boom, got %v", err)
	}
	if got := r.State(); got != StatePanicked {
		t.Fatalf("expected PANICKED, got %s", got)
	}
}

func TestRoutine_TerminalStatesRejectTransitions(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.Go(func(ctx context.Context, self *Routine) error { return nil })
	_ = r.Wait()

	if !r.State().Terminal() {
		t.Fatalf("expected terminal state, got %s", r.State())
	}
	if r.transition(StateRunning) {
		t.Fatal("terminal state must reject transition to RUNNING")
	}
	if r.transition(StateTimedOut) {
		t.Fatal("terminal state must reject transition to TIMED_OUT")
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("state must not change, got %s", got)
	}
}

func TestRoutine_IdleTimeoutDisabledByDefault(t *testing.T) {
	if got := defaultRoutineOptions().IdleTimeout; got != 0 {
		t.Fatalf("idle timeout must be disabled by default, got %v", got)
	}

	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.Go(func(ctx context.Context, self *Routine) error {
		time.Sleep(250 * time.Millisecond)
		return nil
	})
	if err := r.Wait(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}

func TestRoutine_WithIdleTimeout_ZeroAndNegativeDisable(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		opts := defaultRoutineOptions()
		WithIdleTimeout(d)(&opts)
		if opts.IdleTimeout != 0 {
			t.Fatalf("WithIdleTimeout(%v) should disable idle timeout, got %v", d, opts.IdleTimeout)
		}
	}
}

func TestRoutine_HeartbeatPreventsTimeout(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.Go(func(ctx context.Context, self *Routine) error {
		deadline := time.After(400 * time.Millisecond)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline:
				return nil
			case <-time.After(30 * time.Millisecond):
				self.Beat()
			}
		}
	}, WithIdleTimeout(120*time.Millisecond))

	if err := r.Wait(); err != nil {
		t.Fatalf("heartbeats should keep the routine alive, got %v", err)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}
