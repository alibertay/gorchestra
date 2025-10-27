package obs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Option func(*Options)

type Options struct {
	Addr                string        // HTTP dinleme adresi (örn. ":9090")
	EnablePProf         bool          // /debug/pprof* aç
	EnableDashboard     bool          // /gorchestra (grafikli dashboard) aç
	CPUSampleInterval   time.Duration // process CPU örnekleme periyodu
	TopicSampleInterval time.Duration // topic gauge güncelleme periyodu
	Registry            *prometheus.Registry
}

func defaults() Options {
	return Options{
		Addr:                ":9090",
		EnablePProf:         true,
		EnableDashboard:     true,
		CPUSampleInterval:   time.Second,
		TopicSampleInterval: 500 * time.Millisecond,
		Registry:            prometheus.NewRegistry(),
	}
}

func WithAddr(a string) Option                  { return func(o *Options) { o.Addr = a } }
func WithPProf(v bool) Option                   { return func(o *Options) { o.EnablePProf = v } }
func WithDashboard(v bool) Option               { return func(o *Options) { o.EnableDashboard = v } }
func WithCPUSampleEvery(d time.Duration) Option { return func(o *Options) { o.CPUSampleInterval = d } }
func WithTopicSampleEvery(d time.Duration) Option {
	return func(o *Options) { o.TopicSampleInterval = d }
}
func WithRegistry(r *prometheus.Registry) Option {
	return func(o *Options) { o.Registry = r }
}
