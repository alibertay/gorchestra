package gorchestra

import (
	"context"
	"math/rand"
	"time"
)

type RestartPolicy int

const (
	RestartNever RestartPolicy = iota
	RestartOnFailure
	RestartAlways
)

type SupervisorOption func(*SupervisorConfig)

type SupervisorConfig struct {
	Policy     RestartPolicy
	Initial    time.Duration // backoff start
	Max        time.Duration // backoff cap
	Multiplier float64
	Jitter     float64 // 0..1
	Name       string
	Idle       time.Duration
	QueueCap   int
}

func defaultSupCfg() SupervisorConfig {
	return SupervisorConfig{
		Policy:     RestartOnFailure,
		Initial:    200 * time.Millisecond,
		Max:        5 * time.Second,
		Multiplier: 2.0,
		Jitter:     0.2,
		Idle:       5 * time.Second,
		QueueCap:   128,
	}
}

func WithSupName(n string) SupervisorOption { return func(c *SupervisorConfig) { c.Name = n } }
func WithSupPolicy(p RestartPolicy) SupervisorOption {
	return func(c *SupervisorConfig) { c.Policy = p }
}
func WithSupBackoff(initial, max time.Duration, mult float64, jitter float64) SupervisorOption {
	return func(c *SupervisorConfig) {
		if initial > 0 {
			c.Initial = initial
		}
		if max > 0 {
			c.Max = max
		}
		if mult > 0 {
			c.Multiplier = mult
		}
		if jitter >= 0 && jitter <= 1 {
			c.Jitter = jitter
		}
	}
}
func WithSupIdleTimeout(d time.Duration) SupervisorOption {
	return func(c *SupervisorConfig) { c.Idle = d }
}
func WithSupQueueCap(n int) SupervisorOption { return func(c *SupervisorConfig) { c.QueueCap = n } }

// GoSupervised: fn dönerse policy'e göre yeniden başlatır.
func (o *Orchestrator) GoSupervised(fn func(ctx context.Context, self *Routine) error, opts ...SupervisorOption) *Routine {
	cfg := defaultSupCfg()
	for _, opt := range opts {
		opt(&cfg)
	}
	// tek Routine içinde loop ederek supervise edelim
	return o.Go(func(ctx context.Context, self *Routine) error {
		backoff := cfg.Initial
		for {
			// her denemede yeni child-context
			childCtx, cancel := context.WithCancel(ctx)
			err := fn(childCtx, self)
			cancel()

			if ctx.Err() != nil {
				return ctx.Err()
			}

			restart := false
			switch cfg.Policy {
			case RestartNever:
				restart = false
			case RestartOnFailure:
				restart = (err != nil)
			case RestartAlways:
				restart = true
			}

			if !restart {
				return err
			}

			self.incrementRestarts()

			// backoff + jitter
			j := 1.0 + (cfg.Jitter * (rand.Float64()*2 - 1)) // 1±jitter
			sleep := time.Duration(float64(backoff) * j)
			if sleep > cfg.Max {
				sleep = cfg.Max
			}
			timer := time.NewTimer(sleep)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			next := time.Duration(float64(backoff) * cfg.Multiplier)
			if next > cfg.Max {
				next = cfg.Max
			}
			backoff = next
		}
	}, WithName(cfg.Name), WithIdleTimeout(cfg.Idle), WithQueueCap(cfg.QueueCap))
}
