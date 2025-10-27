package gorchestra

import (
	"context"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestRoutine_Lifecycle_NoLeak(t *testing.T) {
	o := New()
	r := o.Go(func(ctx context.Context, self *Routine) error {
		// do a short heartbeat loop then exit
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for i := 0; i < 5; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				self.Beat()
			}
		}
		return nil
	}, WithName("unit"), WithQueueCap(8), WithIdleTimeout(2*time.Second))

	if err := r.Wait(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := o.Shutdown(1 * time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
