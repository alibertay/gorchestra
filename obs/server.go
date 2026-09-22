package obs

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	g "github.com/alibertay/gorchestra"
	gom "github.com/alibertay/gorchestra/metrics"
)

//go:embed dashboard.html
var dashboardHTML []byte

type topicStatsFn func() g.ChannelStats

type Server struct {
	o     *g.Orchestrator
	opt   Options
	mux   *http.ServeMux
	http  *http.Server
	stopC chan struct{}
	wg    sync.WaitGroup

	startOnce sync.Once
	stopOnce  sync.Once
	lnMu      sync.Mutex
	ln        net.Listener

	reg        *prometheus.Registry
	collector  *gom.Collector
	topicLen   *prometheus.GaugeVec
	topicCap   *prometheus.GaugeVec
	topicBytes *prometheus.GaugeVec
	procCPU    prometheus.Gauge

	muTopics sync.RWMutex
	topics   map[string]topicStatsFn
}

func NewServer(o *g.Orchestrator, opts ...Option) *Server {
	opt := defaults()
	for _, f := range opts {
		f(&opt)
	}
	s := &Server{
		o:      o,
		opt:    opt,
		mux:    http.NewServeMux(),
		stopC:  make(chan struct{}),
		reg:    opt.Registry,
		topics: make(map[string]topicStatsFn),
	}

	// Routine metrics
	s.collector = gom.NewPrometheusCollector(o)
	s.reg.MustRegister(s.collector)

	// Topic gauges
	s.topicLen = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gorchestra_topic_len", Help: "Topic queue length"}, []string{"topic"})
	s.topicCap = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gorchestra_topic_cap", Help: "Topic queue capacity"}, []string{"topic"})
	s.topicBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gorchestra_topic_bytes", Help: "Approximate topic bytes queued"}, []string{"topic"})
	s.reg.MustRegister(s.topicLen, s.topicCap, s.topicBytes)

	// Process CPU%
	s.procCPU = prometheus.NewGauge(prometheus.GaugeOpts{Name: "gorchestra_process_cpu_percent", Help: "Process-level CPU utilization percent (runtime/metrics)"})
	s.reg.MustRegister(s.procCPU)

	// Routes
	s.mux.Handle("/metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	if s.opt.EnableDashboard {
		// dashboard (hem /gorchestra hem /gorchestra/ çalışsın)
		s.mux.HandleFunc("/gorchestra", s.handleDashboard)
		s.mux.HandleFunc("/gorchestra/", s.handleDashboard)
		s.mux.HandleFunc("/gorchestra/snapshots", s.handleSnapshotsJSON)
		s.mux.HandleFunc("/gorchestra/topics", s.handleTopicsJSON)
		s.mux.HandleFunc("/gorchestra/history", s.handleHistoryJSON)
		s.mux.HandleFunc("/gorchestra/terminals", s.handleTerminalsJSON)
	}

	if s.opt.EnablePProf {
		s.mountPProf(s.mux)
	}
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	s.http = &http.Server{Addr: s.opt.Addr, Handler: s.mux}
	return s
}

func (s *Server) RegisterTopic(name string, f topicStatsFn) {
	s.muTopics.Lock()
	s.topics[name] = f
	s.muTopics.Unlock()
}

// Start binds the listener and serves HTTP, blocking until Stop is called
// (or the server fails). Bind errors are returned synchronously. Use
// StartAsync to serve in a background goroutine.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.opt.Addr)
	if err != nil {
		return err
	}
	s.setListener(ln)
	s.startBackground()

	err = s.http.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// StartAsync binds the listener and serves HTTP in a background goroutine.
// Bind errors are returned synchronously; call Stop to shut the server down.
func (s *Server) StartAsync() error {
	ln, err := net.Listen("tcp", s.opt.Addr)
	if err != nil {
		return err
	}
	s.setListener(ln)
	s.startBackground()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.http.Serve(ln)
	}()
	return nil
}

func (s *Server) setListener(ln net.Listener) {
	s.lnMu.Lock()
	s.ln = ln
	s.lnMu.Unlock()
}

// Addr returns the address the server is listening on, or "" before start.
func (s *Server) Addr() string {
	s.lnMu.Lock()
	defer s.lnMu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// startBackground launches the CPU and topic samplers exactly once.
func (s *Server) startBackground() {
	s.startOnce.Do(func() {
		// CPU sampler
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.sampleProcessCPU(s.opt.CPUSampleInterval)
		}()

		// Topic sampler
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			tk := time.NewTicker(s.opt.TopicSampleInterval)
			defer tk.Stop()
			for {
				select {
				case <-s.stopC:
					return
				case <-tk.C:
					s.updateTopicGauges()
				}
			}
		}()
	})
}

// Stop gracefully shuts the HTTP server down and stops the samplers. It is
// safe to call multiple times.
func (s *Server) Stop(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() {
		close(s.stopC)
		err = s.http.Shutdown(ctx)
	})
	s.wg.Wait()
	return err
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Fallback: embed boş ise basit HTML yaz
	if len(dashboardHTML) == 0 {
		_, _ = w.Write([]byte(`<html><body><h1>gorchestra</h1><p>Dashboard asset not embedded; check obs/dashboard.html placement.</p><p><a href="/metrics">/metrics</a> • <a href="/debug/pprof">/debug/pprof</a></p></body></html>`))
		return
	}
	_, _ = w.Write(dashboardHTML)
}

func (s *Server) handleSnapshotsJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	j := s.o.PublicSnapshots()
	_ = json.NewEncoder(w).Encode(j)
}

type historyItem struct {
	ID            uint64   `json:"id"`
	Name          string   `json:"name"`
	State         string   `json:"state"`
	Health        g.Health `json:"health"`
	Restarts      uint64   `json:"restarts"`
	UptimeSeconds float64  `json:"uptimeSeconds"`
	FinishedAt    string   `json:"finishedAt"`
	Err           string   `json:"err,omitempty"`
}

func (s *Server) handleHistoryJSON(w http.ResponseWriter, _ *http.Request) {
	recs := s.o.History()
	out := make([]historyItem, 0, len(recs))
	for _, r := range recs {
		out = append(out, historyItem{
			ID:            r.ID,
			Name:          r.Name,
			State:         r.State.String(),
			Health:        r.Health,
			Restarts:      r.Restarts,
			UptimeSeconds: r.Uptime.Seconds(),
			FinishedAt:    r.FinishedAt.Format(time.RFC3339),
			Err:           r.Err,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type terminalItem struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Count uint64 `json:"count"`
}

func (s *Server) handleTerminalsJSON(w http.ResponseWriter, _ *http.Request) {
	counts := s.o.TerminalCounts()
	out := make([]terminalItem, 0, len(counts))
	for _, c := range counts {
		out = append(out, terminalItem{Name: c.Name, State: c.State.String(), Count: c.Count})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleTopicsJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	type Topic struct {
		Name string         `json:"name"`
		Stat g.ChannelStats `json:"stat"`
	}
	var out []Topic
	s.muTopics.RLock()
	for name, f := range s.topics {
		out = append(out, Topic{Name: name, Stat: f()})
	}
	s.muTopics.RUnlock()
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) updateTopicGauges() {
	s.muTopics.RLock()
	for name, f := range s.topics {
		st := f()
		s.topicLen.WithLabelValues(name).Set(float64(st.Len))
		s.topicCap.WithLabelValues(name).Set(float64(st.Cap))
		s.topicBytes.WithLabelValues(name).Set(float64(st.ApproxBytes))
	}
	s.muTopics.RUnlock()
}

func (s *Server) mountPProf(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

// ==== CPU (runtime/metrics) ====

func (s *Server) sampleProcessCPU(every time.Duration) {
	ncpu := float64(runtime.NumCPU())
	reader := newCPUReader()
	tk := time.NewTicker(every)
	defer tk.Stop()

	last := reader.Read()
	for {
		select {
		case <-s.stopC:
			return
		case <-tk.C:
			cur := reader.Read()
			delta := cur - last
			last = cur
			max := ncpu * every.Seconds()
			pct := 0.0
			if max > 0 {
				pct = (delta / max) * 100.0
			}
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			s.procCPU.Set(pct)
		}
	}
}
