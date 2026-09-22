package gorchestra

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSupervisor_RestartsOnError(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	var attempts atomic.Int32
	succeeded := make(chan struct{})
	r := o.GoSupervised(func(ctx context.Context, self *Routine) error {
		if n := attempts.Add(1); n < 3 {
			return fmt.Errorf("attempt %d failed", n)
		}
		close(succeeded)
		<-ctx.Done()
		return ctx.Err()
	}, WithSupBackoff(10*time.Millisecond, 50*time.Millisecond, 2.0, 0))

	select {
	case <-succeeded:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never succeeded after restarts")
	}
	if got := r.Restarts(); got < 2 {
		t.Fatalf("expected at least 2 restarts, got %d", got)
	}

	r.Kill()
	if err := r.Wait(); err != nil {
		t.Fatalf("expected nil error after kill, got %v", err)
	}
}

func TestSupervisor_RestartsOnPanic(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	var attempts atomic.Int32
	succeeded := make(chan struct{})
	r := o.GoSupervised(func(ctx context.Context, self *Routine) error {
		if n := attempts.Add(1); n <= 2 {
			panic(fmt.Sprintf("boom %d", n))
		}
		close(succeeded)
		<-ctx.Done()
		return ctx.Err()
	}, WithSupPolicy(RestartOnFailure), WithSupBackoff(5*time.Millisecond, 20*time.Millisecond, 2.0, 0))

	select {
	case <-succeeded:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never recovered from panic")
	}
	if got := r.Restarts(); got < 2 {
		t.Fatalf("expected at least 2 restarts after panics, got %d", got)
	}

	r.Kill()
	if err := r.Wait(); err != nil {
		t.Fatalf("expected nil error after kill, got %v", err)
	}
}

func TestSupervisor_RestartNever(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.GoSupervised(func(ctx context.Context, self *Routine) error {
		return errors.New("fatal failure")
	}, WithSupPolicy(RestartNever))

	err := r.Wait()
	if err == nil || !strings.Contains(err.Error(), "fatal failure") {
		t.Fatalf("expected fatal failure error, got %v", err)
	}
	if got := r.Restarts(); got != 0 {
		t.Fatalf("RestartNever must not restart, got %d restarts", got)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}

func TestSupervisor_PanicSurfacesWhenRestartNever(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.GoSupervised(func(ctx context.Context, self *Routine) error {
		panic("fatal panic")
	}, WithSupPolicy(RestartNever))

	err := r.Wait()
	if err == nil || !strings.Contains(err.Error(), "supervised worker panic") {
		t.Fatalf("expected supervised panic error, got %v", err)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}

func TestBackoff_GrowthCapAndReset(t *testing.T) {
	b := newBackoff(SupervisorConfig{
		Initial:    10 * time.Millisecond,
		Max:        40 * time.Millisecond,
		Multiplier: 2,
		Jitter:     0,
	})

	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
	}
	for i, w := range want {
		if got := b.next(); got != w {
			t.Fatalf("step %d: expected %v, got %v", i, w, got)
		}
	}

	b.reset()
	if got := b.next(); got != 10*time.Millisecond {
		t.Fatalf("after reset expected initial backoff, got %v", got)
	}
}

func TestBackoff_JitterStaysWithinBounds(t *testing.T) {
	b := newBackoff(SupervisorConfig{
		Initial:    100 * time.Millisecond,
		Max:        time.Second,
		Multiplier: 2,
		Jitter:     0.5,
	})

	for i := 0; i < 200; i++ {
		b.reset()
		got := b.next()
		if got < 50*time.Millisecond || got > 150*time.Millisecond {
			t.Fatalf("jittered backoff out of bounds: %v", got)
		}
	}
}

func TestBackoff_MultiplierBelowOneKeepsInitial(t *testing.T) {
	b := newBackoff(SupervisorConfig{
		Initial:    50 * time.Millisecond,
		Max:        time.Second,
		Multiplier: 0.5,
		Jitter:     0,
	})
	b.next()
	if got := b.next(); got != 50*time.Millisecond {
		t.Fatalf("backoff must not shrink below initial, got %v", got)
	}
}

func TestSupervisor_StableWindowResetsBackoff(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	const window = 50 * time.Millisecond
	var attempts atomic.Int32
	done := make(chan struct{})
	start := time.Now()

	r := o.GoSupervised(func(ctx context.Context, self *Routine) error {
		switch attempts.Add(1) {
		case 1, 2:
			return errors.New("quick failure")
		case 3:
			time.Sleep(window + 20*time.Millisecond) // stable run, then fail
			return errors.New("failure after stable period")
		default:
			close(done)
			<-ctx.Done()
			return ctx.Err()
		}
	},
		WithSupBackoff(5*time.Millisecond, 2*time.Second, 10.0, 0),
		WithSupStableWindow(window),
	)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never recovered after the stable attempt")
	}
	// Without the stable-window reset, the third failure would back off for
	// ~500ms (5ms * 10 * 10).
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("backoff did not reset after a stable attempt (took %v)", elapsed)
	}

	r.Kill()
	_ = r.Wait()
}

func TestSupervisor_BackoffKeepsHeartbeatAlive(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	var attempts atomic.Int32
	done := make(chan struct{})
	r := o.GoSupervised(func(ctx context.Context, self *Routine) error {
		if attempts.Add(1) == 1 {
			return errors.New("first failure")
		}
		close(done)
		<-ctx.Done()
		return ctx.Err()
	},
		WithSupIdleTimeout(120*time.Millisecond),
		WithSupBackoff(300*time.Millisecond, 300*time.Millisecond, 2.0, 0),
	)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor was killed by the idle watchdog during backoff")
	}
	if got := r.State(); got == StateTimedOut {
		t.Fatal("backoff wait must keep the routine alive")
	}

	r.Kill()
	_ = r.Wait()
}

func TestSupervisor_RestartAlways(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	var runs atomic.Int32
	r := o.GoSupervised(func(ctx context.Context, self *Routine) error {
		runs.Add(1)
		return nil
	}, WithSupPolicy(RestartAlways), WithSupBackoff(5*time.Millisecond, 10*time.Millisecond, 2.0, 0))

	waitFor(t, 2*time.Second, "at least 3 runs", func() bool { return runs.Load() >= 3 })

	r.Kill()
	if err := r.Wait(); err != nil {
		t.Fatalf("expected nil error after kill, got %v", err)
	}
	if got := r.State(); got != StateStopped {
		t.Fatalf("expected STOPPED, got %s", got)
	}
}
