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
