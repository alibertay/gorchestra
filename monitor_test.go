package gorchestra

import (
	"context"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time                         { return f.now }
func (f *fakeClock) Since(t time.Time) time.Duration        { return f.now.Sub(t) }
func (f *fakeClock) NewTicker(d time.Duration) *time.Ticker { return time.NewTicker(d) }
func (f *fakeClock) NewTimer(d time.Duration) *time.Timer   { return time.NewTimer(d) }
func (f *fakeClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func TestSnapshot_UsesInjectedClock(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	r := newRoutine(1, "fake", 0, 8, clk)

	clk.now = clk.now.Add(2 * time.Second)

	s := snapshotOf(r)
	if s.Uptime != 2*time.Second {
		t.Fatalf("expected 2s uptime from the injected clock, got %v", s.Uptime)
	}
	if s.IdleFor != 2*time.Second {
		t.Fatalf("expected 2s idle from the injected clock, got %v", s.IdleFor)
	}
}

func TestSnapshot_IdleWorkerReportsZeroBusy(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	started := make(chan struct{})
	r := o.Go(func(ctx context.Context, self *Routine) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	time.Sleep(50 * time.Millisecond)

	s := snapshotOf(r)
	if s.BusyPercent != 0 {
		t.Fatalf("an uninstrumented idle worker must report 0%% busy, got %.1f%%", s.BusyPercent)
	}
	if s.Busy != 0 {
		t.Fatalf("expected no recorded busy time, got %v", s.Busy)
	}
}

func TestSnapshot_BusyPercentReflectsInstrumentation(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	started := make(chan struct{})
	r := o.Go(func(ctx context.Context, self *Routine) error {
		self.AddBusy(50 * time.Millisecond)
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	time.Sleep(100 * time.Millisecond) // let uptime grow past the busy time

	s := snapshotOf(r)
	if s.BusyPercent < 20 || s.BusyPercent > 80 {
		t.Fatalf("expected ~50%% busy from instrumentation, got %.1f%%", s.BusyPercent)
	}
	if s.Busy != 50*time.Millisecond {
		t.Fatalf("expected 50ms busy, got %v", s.Busy)
	}
}

func TestPublicSnapshot_ExposesActivityFields(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	started := make(chan struct{})
	r := o.Go(func(ctx context.Context, self *Routine) error {
		self.AddBusy(20 * time.Millisecond)
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	time.Sleep(50 * time.Millisecond)

	snaps := o.PublicSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("expected one active snapshot, got %d", len(snaps))
	}
	s := snaps[0]
	if s.BusySec != 0.02 {
		t.Fatalf("expected 0.02 busy seconds, got %v", s.BusySec)
	}
	if s.UptimeSec <= 0 || s.IdleSec <= 0 {
		t.Fatalf("expected positive uptime/idle, got uptime=%v idle=%v", s.UptimeSec, s.IdleSec)
	}
	_ = r
}
