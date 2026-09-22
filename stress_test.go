package gorchestra

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStress_ManyShortLivedRoutines(t *testing.T) {
	n := 10000
	if testing.Short() {
		n = 1000
	}

	o := New(WithHistoryLimit(128))
	defer func() { _ = o.Shutdown(10 * time.Second) }()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := o.TryGo(func(ctx context.Context, self *Routine) error { return nil })
			if err != nil {
				return
			}
			_ = r.Wait()
		}()
	}
	wg.Wait()

	if got := len(o.History()); got != 128 {
		t.Fatalf("expected bounded history of 128, got %d", got)
	}
	if got := len(o.List()); got != 0 {
		t.Fatalf("expected no active routines, got %d", got)
	}
}

func TestStress_ShutdownRaceWithGo(t *testing.T) {
	o := New()
	const workers = 100

	var accepted, rejected atomic.Int64
	var wg sync.WaitGroup

	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				r, err := o.TryGo(func(ctx context.Context, self *Routine) error {
					time.Sleep(time.Millisecond)
					return nil
				})
				if err != nil {
					rejected.Add(1)
					continue
				}
				accepted.Add(1)
				_ = r.Wait()
			}
		}()
	}
	close(start)
	time.Sleep(5 * time.Millisecond)

	if err := o.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	wg.Wait()

	if accepted.Load() == 0 {
		t.Fatal("expected at least one accepted routine")
	}
	if rejected.Load() == 0 {
		t.Fatal("expected rejections once shutdown started")
	}
	if got := o.State(); got != OrchestratorClosed {
		t.Fatalf("expected CLOSED, got %s", got)
	}
}

func TestStress_RapidCreateDestroy(t *testing.T) {
	o := New(WithHistoryLimit(64))

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				r := o.Go(func(ctx context.Context, self *Routine) error { return nil })
				r.Kill()
				_ = r.Wait()
			}
		}()
	}
	wg.Wait()

	if err := o.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := len(o.List()); got != 0 {
		t.Fatalf("expected no active routines, got %d", got)
	}
}

func TestStress_SupervisorRestartStorm(t *testing.T) {
	o := New()
	defer func() { _ = o.Shutdown(time.Second) }()

	r := o.GoSupervised(func(ctx context.Context, self *Routine) error {
		return errors.New("always failing")
	},
		WithSupPolicy(RestartAlways),
		WithSupBackoff(time.Millisecond, 2*time.Millisecond, 1.0, 0),
		WithSupStableWindow(0),
	)

	waitFor(t, 3*time.Second, "at least 50 restarts", func() bool { return r.Restarts() >= 50 })

	r.Kill()
	if err := r.Wait(); err != nil {
		t.Fatalf("expected nil after kill, got %v", err)
	}
}

func TestStress_HighTopicThroughput(t *testing.T) {
	bus := NewBus[int]()
	ch := bus.Topic("events", 256)

	const producers, perProd = 4, 5000
	const total = producers * perProd

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProd; i++ {
				if err := ch.Send(context.Background(), i); err != nil {
					t.Errorf("send: %v", err)
					return
				}
			}
		}()
	}

	recvd := 0
	for recvd < total {
		if _, err := ch.Recv(context.Background()); err != nil {
			t.Fatalf("recv: %v", err)
		}
		recvd++
	}
	wg.Wait()

	st := ch.Stats()
	if st.Sends != total || st.Recvs != total {
		t.Fatalf("expected %d sends/recvs, got %d/%d", total, st.Sends, st.Recvs)
	}
	if st.Len != 0 {
		t.Fatalf("expected empty queue, got len=%d", st.Len)
	}
}
