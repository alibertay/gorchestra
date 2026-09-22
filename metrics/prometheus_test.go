package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	g "github.com/alibertay/gorchestra"
)

func gather(t *testing.T, c *Collector) map[string]*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		out[f.GetName()] = f
	}
	return out
}

func TestCollector_CompletedRoutinesDoNotLeakSeries(t *testing.T) {
	o := g.New()
	for i := 0; i < 5; i++ {
		r := o.Go(func(ctx context.Context, self *g.Routine) error { return nil }, g.WithName("short-lived"))
		_ = r.Wait()
	}
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	families := gather(t, NewPrometheusCollector(o))

	if f := families["gorchestra_routine_info"]; f != nil && len(f.GetMetric()) != 0 {
		t.Fatalf("finished routines must not keep per-id series, got %d", len(f.GetMetric()))
	}
	if f := families["gorchestra_active_routines"]; f != nil {
		if got := f.GetMetric()[0].GetGauge().GetValue(); got != 0 {
			t.Fatalf("expected 0 active routines, got %v", got)
		}
	}
}

func TestCollector_TerminalTotalsAggregatedByNameAndState(t *testing.T) {
	o := g.New()
	const n = 4
	for i := 0; i < n; i++ {
		r := o.Go(func(ctx context.Context, self *g.Routine) error { return nil }, g.WithName("worker"))
		_ = r.Wait()
	}
	if err := o.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	families := gather(t, NewPrometheusCollector(o))
	f := families["gorchestra_routines_terminal_total"]
	if f == nil || len(f.GetMetric()) != 1 {
		t.Fatalf("expected a single aggregated terminal series, got %v", f)
	}

	m := f.GetMetric()[0]
	var name, state string
	for _, l := range m.GetLabel() {
		switch l.GetName() {
		case "name":
			name = l.GetValue()
		case "state":
			state = l.GetValue()
		}
	}
	if name != "worker" || state != "STOPPED" {
		t.Fatalf("unexpected labels name=%q state=%q", name, state)
	}
	if got := m.GetCounter().GetValue(); got != n {
		t.Fatalf("expected %d terminal routines, got %v", n, got)
	}
}

func TestCollector_ActiveRoutineSeries(t *testing.T) {
	o := g.New()
	started := make(chan struct{})
	r := o.Go(func(ctx context.Context, self *g.Routine) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, g.WithName("live"))
	<-started
	defer func() {
		r.Kill()
		_ = r.Wait()
		_ = o.Shutdown(time.Second)
	}()

	families := gather(t, NewPrometheusCollector(o))

	info := families["gorchestra_routine_info"]
	if info == nil || len(info.GetMetric()) != 1 {
		t.Fatalf("expected one active routine series, got %v", info)
	}
	labels := map[string]string{}
	for _, l := range info.GetMetric()[0].GetLabel() {
		labels[l.GetName()] = l.GetValue()
	}
	if labels["name"] != "live" || labels["state"] != "RUNNING" || labels["id"] == "" {
		t.Fatalf("unexpected labels: %v", labels)
	}

	active := families["gorchestra_active_routines"]
	if active == nil || active.GetMetric()[0].GetGauge().GetValue() != 1 {
		t.Fatalf("expected 1 active routine, got %v", active)
	}
	if families["gorchestra_routine_busy_seconds"] == nil {
		t.Fatal("expected busy seconds series for the active routine")
	}
	if families["gorchestra_routine_blocked_seconds"] == nil {
		t.Fatal("expected blocked seconds series for the active routine")
	}
}
