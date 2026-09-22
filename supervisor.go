package gorchestra

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/alibertay/gorchestra/internalutil"
)

type RestartPolicy int

const (
	RestartNever RestartPolicy = iota
	RestartOnFailure
	RestartAlways
)

type SupervisorOption func(*SupervisorConfig)

type SupervisorConfig struct {
	Policy       RestartPolicy
	Initial      time.Duration // backoff start
	Max          time.Duration // backoff cap
	Multiplier   float64
	Jitter       float64       // 0..1
	StableWindow time.Duration // attempts that ran this long reset the backoff
	Name         string
	Idle         time.Duration
	QueueCap     int
}

func defaultSupCfg() SupervisorConfig {
	return SupervisorConfig{
		Policy:       RestartOnFailure,
		Initial:      200 * time.Millisecond,
		Max:          5 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.2,
		StableWindow: 30 * time.Second,
		Idle:         0, // idle timeout disabled unless explicitly enabled
		QueueCap:     128,
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

// WithSupStableWindow resets the backoff to its initial value after an
// attempt that ran at least d without returning. 0 disables the reset.
func WithSupStableWindow(d time.Duration) SupervisorOption {
	return func(c *SupervisorConfig) {
		if d < 0 {
			d = 0
		}
		c.StableWindow = d
	}
}

// WithSupIdleTimeout cancels the supervised routine (like a normal routine)
// when it stops heartbeating. The supervisor keeps heartbeating while it is
// waiting out a backoff delay. 0 disables the idle watchdog.
func WithSupIdleTimeout(d time.Duration) SupervisorOption {
	return func(c *SupervisorConfig) {
		if d < 0 {
			d = 0
		}
		c.Idle = d
	}
}
func WithSupQueueCap(n int) SupervisorOption { return func(c *SupervisorConfig) { c.QueueCap = n } }

// backoff computes exponential backoff delays with jitter and knows how to
// reset itself after a stable run.
type backoff struct {
	initial time.Duration
	max     time.Duration
	mult    float64
	jitter  float64
	current time.Duration
}

func newBackoff(cfg SupervisorConfig) *backoff {
	return &backoff{
		initial: cfg.Initial,
		max:     cfg.Max,
		mult:    cfg.Multiplier,
		jitter:  cfg.Jitter,
		current: cfg.Initial,
	}
}

func (b *backoff) reset() { b.current = b.initial }

// next returns the sleep duration for the current step and advances the
// backoff progression.
func (b *backoff) next() time.Duration {
	sleep := b.current
	if b.jitter > 0 {
		factor := 1.0 + (b.jitter * (rand.Float64()*2 - 1)) // 1±jitter
		sleep = time.Duration(float64(sleep) * factor)
	}
	if sleep > b.max {
		sleep = b.max
	}
	if sleep < 0 {
		sleep = 0
	}

	nextStep := time.Duration(float64(b.current) * b.mult)
	if nextStep > b.max {
		nextStep = b.max
	}
	if nextStep < b.initial {
		nextStep = b.initial
	}
	b.current = nextStep
	return sleep
}

// runAttempt runs a single supervised worker attempt inside its own panic
// boundary. A panic is converted into an error so the restart policy can
// decide what to do with it.
func runAttempt(fn func(ctx context.Context, self *Routine) error, ctx context.Context, self *Routine) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("gorchestra: supervised worker panic: %v", rec)
		}
	}()
	return fn(ctx, self)
}

// waitBackoff waits out a backoff delay while keeping the heartbeat alive, so
// a supervised routine with an idle timeout is not mistaken for a dead one
// while it is merely waiting to restart. It uses the injected clock so the
// whole supervisor path shares one time source. Returns false if ctx was
// cancelled.
func waitBackoff(ctx context.Context, self *Routine, clock internalutil.Clock, d time.Duration, idle time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	beat := time.Second
	if idle > 0 && idle/2 < beat {
		beat = idle / 2
	}
	if beat < time.Millisecond {
		beat = time.Millisecond
	}

	timer := clock.NewTimer(d)
	defer timer.Stop()
	ticker := clock.NewTicker(beat)
	defer ticker.Stop()

	self.Beat()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			self.Beat()
		case <-timer.C:
			return true
		}
	}
}

// GoSupervised runs fn under a restart policy: when an attempt returns (or
// panics) the supervisor waits out an exponential backoff and starts a new
// attempt. Attempts that ran at least StableWindow reset the backoff.
func (o *Orchestrator) GoSupervised(fn func(ctx context.Context, self *Routine) error, opts ...SupervisorOption) *Routine {
	cfg := defaultSupCfg()
	for _, opt := range opts {
		opt(&cfg)
	}
	// tek Routine içinde loop ederek supervise edelim
	return o.Go(func(ctx context.Context, self *Routine) error {
		defer self.setSupervisorState(SupervisorStopping)
		bo := newBackoff(cfg)
		for {
			self.setSupervisorState(SupervisorRunning)
			// her denemede yeni child-context
			attemptStart := o.clock.Now()
			childCtx, cancel := context.WithCancel(ctx)
			err := runAttempt(fn, childCtx, self)
			cancel()

			if ctx.Err() != nil {
				return ctx.Err()
			}

			if cfg.StableWindow > 0 && o.clock.Since(attemptStart) >= cfg.StableWindow {
				bo.reset()
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

			self.setSupervisorState(SupervisorBackoff)
			if !waitBackoff(ctx, self, o.clock, bo.next(), cfg.Idle) {
				return ctx.Err()
			}
		}
	}, WithName(cfg.Name), WithIdleTimeout(cfg.Idle), WithQueueCap(cfg.QueueCap))
}
