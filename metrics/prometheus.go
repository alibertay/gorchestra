package metrics

import (
	"github.com/alibertay/gorchestra"

	"github.com/prometheus/client_golang/prometheus"
)

// Collector exports gorchestra state as Prometheus metrics.
//
// Cardinality strategy: per-routine series (id label) are only exported for
// routines that are still active. Finished routines are retired from the
// active registry, so their series disappear instead of accumulating
// forever. Cumulative terminal counters are aggregated by name and state,
// which keeps their cardinality bounded by the number of distinct worker
// names rather than the number of routine IDs ever created.
type Collector struct {
	o *gorchestra.Orchestrator

	activeRoutines *prometheus.Desc
	infoGauge      *prometheus.Desc
	queueLen       *prometheus.Desc
	queueBytes     *prometheus.Desc
	restarts       *prometheus.Desc
	busySeconds    *prometheus.Desc
	blockedSeconds *prometheus.Desc
	terminals      *prometheus.Desc
}

func NewPrometheusCollector(o *gorchestra.Orchestrator) *Collector {
	labels := []string{"id", "name", "state", "health"}
	return &Collector{
		o:              o,
		activeRoutines: prometheus.NewDesc("gorchestra_active_routines", "Number of RUNNING routines", nil, nil),
		infoGauge:      prometheus.NewDesc("gorchestra_routine_info", "Routine info gauge=1 (active routines only)", labels, nil),
		queueLen:       prometheus.NewDesc("gorchestra_queue_len", "Routine mailbox length", []string{"id", "name"}, nil),
		queueBytes:     prometheus.NewDesc("gorchestra_queue_bytes", "Approximate mailbox bytes", []string{"id", "name"}, nil),
		restarts:       prometheus.NewDesc("gorchestra_restarts_total", "Total restarts since start", []string{"id", "name"}, nil),
		busySeconds:    prometheus.NewDesc("gorchestra_routine_busy_seconds", "Instrumented busy time (AddBusy)", []string{"id", "name"}, nil),
		blockedSeconds: prometheus.NewDesc("gorchestra_routine_blocked_seconds", "Time blocked on channels", []string{"id", "name"}, nil),
		terminals:      prometheus.NewDesc("gorchestra_routines_terminal_total", "Finished routines by name and final state", []string{"name", "state"}, nil),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.activeRoutines
	ch <- c.infoGauge
	ch <- c.queueLen
	ch <- c.queueBytes
	ch <- c.restarts
	ch <- c.busySeconds
	ch <- c.blockedSeconds
	ch <- c.terminals
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	snaps := c.o.PublicSnapshots()
	var running float64
	for _, s := range snaps {
		if s.State == "RUNNING" {
			running++
		}
		ch <- prometheus.MustNewConstMetric(c.infoGauge, prometheus.GaugeValue, 1,
			idStr(s.ID), s.Name, s.State, string(s.Health))
		ch <- prometheus.MustNewConstMetric(c.queueLen, prometheus.GaugeValue, float64(s.QueueLen),
			idStr(s.ID), s.Name)
		ch <- prometheus.MustNewConstMetric(c.queueBytes, prometheus.GaugeValue, float64(s.QueueBytes),
			idStr(s.ID), s.Name)
		ch <- prometheus.MustNewConstMetric(c.restarts, prometheus.CounterValue, float64(s.Restarts),
			idStr(s.ID), s.Name)
		ch <- prometheus.MustNewConstMetric(c.busySeconds, prometheus.GaugeValue, s.BusySec,
			idStr(s.ID), s.Name)
		ch <- prometheus.MustNewConstMetric(c.blockedSeconds, prometheus.GaugeValue, s.BlockedSec,
			idStr(s.ID), s.Name)
	}
	ch <- prometheus.MustNewConstMetric(c.activeRoutines, prometheus.GaugeValue, running)

	for _, tc := range c.o.TerminalCounts() {
		ch <- prometheus.MustNewConstMetric(c.terminals, prometheus.CounterValue, float64(tc.Count),
			tc.Name, tc.State.String())
	}
}

// küçük yardımcı:
func idStr(v uint64) string {
	var buf [20]byte
	i := len(buf)
	for {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
		if v == 0 {
			break
		}
	}
	return string(buf[i:])
}
