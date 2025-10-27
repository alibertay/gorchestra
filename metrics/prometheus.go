package metrics

import (
	"github.com/alibertay/gorchestra"

	"github.com/prometheus/client_golang/prometheus"
)

type Collector struct {
	o *gorchestra.Orchestrator

	activeRoutines *prometheus.Desc
	infoGauge      *prometheus.Desc
	queueLen       *prometheus.Desc
	queueBytes     *prometheus.Desc
	restarts       *prometheus.Desc
}

func NewPrometheusCollector(o *gorchestra.Orchestrator) *Collector {
	labels := []string{"id", "name", "state", "health"}
	return &Collector{
		o:              o,
		activeRoutines: prometheus.NewDesc("gorchestra_active_routines", "Number of RUNNING routines", nil, nil),
		infoGauge:      prometheus.NewDesc("gorchestra_routine_info", "Routine info gauge=1", labels, nil),
		queueLen:       prometheus.NewDesc("gorchestra_queue_len", "Routine mailbox length", []string{"id", "name"}, nil),
		queueBytes:     prometheus.NewDesc("gorchestra_queue_bytes", "Approximate mailbox bytes", []string{"id", "name"}, nil),
		restarts:       prometheus.NewDesc("gorchestra_restarts_total", "Total restarts since start", []string{"id", "name"}, nil),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.activeRoutines
	ch <- c.infoGauge
	ch <- c.queueLen
	ch <- c.queueBytes
	ch <- c.restarts
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
	}
	ch <- prometheus.MustNewConstMetric(c.activeRoutines, prometheus.GaugeValue, running)
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
