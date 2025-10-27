
# gorchestra

Goroutine orchestration & observability toolkit for Go. Manage worker lifecycles, supervise restarts with backoff, pass messages over typed channels, and expose live metrics & a tiny dashboard — all with minimal dependencies.

> **Why?** When you build concurrent systems, you quickly need the same primitives: a place to spin up/tear down goroutines cleanly, a way to restart crashed workers with backoff, a simple mailbox/bus, and basic health/metrics to see what's going on. **gorchestra** gives you these pieces without forcing a framework.

---

## Table of Contents

- [Features](#features)
- [Install](#install)
- [Quick Start](#quick-start)
  - [Run managed goroutines](#run-managed-goroutines)
  - [Send messages with typed channels](#send-messages-with-typed-channels)
  - [Supervise with restart backoff](#supervise-with-restart-backoff)
  - [Expose metrics & dashboard](#expose-metrics--dashboard)
  - [Graceful shutdown](#graceful-shutdown)
- [Core Concepts](#core-concepts)
  - [Orchestrator](#orchestrator)
  - [Routine](#routine)
  - [Channel & Bus](#channel--bus)
  - [Supervisor](#supervisor)
  - [Observability Server](#observability-server)
  - [Prometheus Collector](#prometheus-collector)
- [API Reference](#api-reference)
  - [Orchestrator](#orchestrator-api)
  - [Routine](#routine-api)
  - [Channel & Bus](#channel--bus-api)
  - [Supervisor options](#supervisor-options)
  - [Observability options](#observability-options)
- [Testing & Leak Safety](#testing--leak-safety)
- [Design Notes & Guarantees](#design-notes--guarantees)
- [FAQ](#faq)
- [Version & Requirements](#version--requirements)
- [License](#license)

---

## Features

- 🧵 **Managed goroutines** with a central `Orchestrator`
- 💓 **Heartbeat & idle-timeout** guard (stop workers that go quiet)
- 🔁 **Supervisor** with **restart policies** and **exponential backoff + jitter**
- ✉️ **Typed mailbox/channel** with per-channel stats (len/cap/blocked/bytes)
- 🚌 **Named bus** to create topic channels on demand
- 📊 **Prometheus metrics** + **/metrics** endpoint
- 📈 **Tiny HTML dashboard** at `/gorchestra` (sortable table + live CPU/queues)
- 🧠 **goleak**-friendly tests (no goroutine leaks)
- 🧹 **Graceful shutdown** with bounded wait
- 🧰 No heavy framework; import what you need

> Go’s runtime doesn’t expose per-goroutine CPU/mem. gorchestra approximates CPU/busyness via blocking/processing time it can observe (mailboxes; explicit `AddBusy`, etc.). Treat CPU% as a helpful, not exact, signal.

---

## Install

```bash
go get github.com/yourusername/gorchestra
```

Add Prometheus only if you use metrics or the obs server:

```bash
go get github.com/prometheus/client_golang@v1
```

---

## Quick Start

### Run managed goroutines

```go
package main

import (
    "context"
    "log"
    "time"

    g "github.com/yourusername/gorchestra"
)

func main() {
    orch := g.New()

    // Start a managed routine
    r := orch.Go(func(ctx context.Context, self *g.Routine) error {
        ticker := time.NewTicker(250 * time.Millisecond)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return ctx.Err()
            case <-ticker.C:
                self.Beat()             // heartbeat (resets idle timer)
                self.AddBusy(10*time.Millisecond) // optional: record work time
                // do work...
            }
        }
    }, g.WithName("ticker-250ms"), g.WithIdleTimeout(5*time.Second), g.WithQueueCap(256))

    // Wait a little, then shut down everything
    time.Sleep(2 * time.Second)
    r.Kill()
    if err := orch.Shutdown(3 * time.Second); err != nil {
        log.Printf("shutdown: %v", err)
    }
}
```

### Send messages with typed channels

```go
// Channel is typed; optionally implement Sizer (SizeBytes() int) on your payload
ch := g.NewChannel[string](1024)

go func() {
    _ = ch.Send(context.Background(), "hello")
}()

msg, err := ch.Recv(context.Background())
_ = msg; _ = err

stats := ch.Stats() // Len/Cap/Sends/Recvs/Blocked[Send|Recv]Ns/ApproxBytes
```

Or create a **bus** of named topics:

```go
bus := g.NewBus[[]byte]()
topic := bus.Topic("price-feed", 4096)
_ = topic.Send(ctx, payload)
```

### Supervise with restart backoff

```go
r := orch.GoSupervised(func(ctx context.Context, self *g.Routine) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            // do risky work; return error to trigger RestartOnFailure/Always
            return nil
        }
    }
},
    g.WithSupName("super-worker"),
    g.WithSupPolicy(g.RestartOnFailure),
    g.WithSupBackoff(500*time.Millisecond, 30*time.Second, 2.0, 0.2), // initial, max, multiplier, jitter
    g.WithSupIdleTimeout(10*time.Second),
    g.WithSupQueueCap(128),
)
_ = r
```

### Expose metrics & dashboard

```go
import (
    "log"
    obs "github.com/yourusername/gorchestra/obs"
    g "github.com/yourusername/gorchestra"
)

func main() {
    orch := g.New()

    // Start HTTP server with /metrics, /gorchestra, and optional pprof
    s := obs.NewServer(orch,
        obs.WithAddr(":9090"),
        obs.WithPProf(true),
        obs.WithDashboard(true),
        obs.WithCPUSampleEvery(500*time.Millisecond),
        obs.WithTopicSampleEvery(1*time.Second),
    )
    go func() { log.Fatal(s.Start()) }()

    // ...
}
```

- **Dashboard**: `GET http://localhost:9090/gorchestra`
- **Prometheus**: `GET http://localhost:9090/metrics`
- **pprof**: `GET http://localhost:9090/debug/pprof/` (if enabled)

> You can also hook directly into Prometheus without the server via `metrics.NewPrometheusCollector(orch)`.

### Graceful shutdown

```go
orch.KillAll()
if err := orch.Shutdown(5 * time.Second); err != nil {
    log.Printf("shutdown timed out: %v", err)
}
```

---

## Core Concepts

### Orchestrator

Central registry of managed goroutines. Spawns routines, tracks their state, prints stats, shuts them down, and exposes snapshots for metrics/UI.

### Routine

A managed goroutine with a unique ID & name. Provides a **context**, **heartbeat** (`Beat()`), **busy-time recording** (`AddBusy`), a **mailbox** (`Mailbox()`), **restart counter** (`Restarts()`), `Kill()` and `Wait()` helpers, and an **idle watchdog** (if `WithIdleTimeout` > 0).

### Channel & Bus

A typed channel wrapper with additional stats and (optional) payload sizing via `Sizer{ SizeBytes() int }`. A `Bus[T]` lets you request or create named topic channels on demand.

### Supervisor

A thin wrapper that restarts a routine based on a policy and backoff configuration. Useful for “always-on” workers that may fail transiently.

### Observability Server

A small HTTP server that ships with gorchestra. It serves Prometheus metrics, a tiny dashboard, and optionally pprof. It also samples **process CPU usage** and exports gauges for topic lengths/bytes so you see backpressure build-ups.

### Prometheus Collector

If you already have your own HTTP stack, you can register the standalone collector and expose it yourself.

---

## API Reference

> Package import paths (shortened below):
>
> - Core: `github.com/yourusername/gorchestra`
> - Observability: `github.com/yourusername/gorchestra/obs`
> - Prometheus collector: `github.com/yourusername/gorchestra/metrics`

### Orchestrator API

```go
// Create a new orchestrator
func New() *Orchestrator

// Run a managed routine once
func (o *Orchestrator) Go(
    fn func(ctx context.Context, self *Routine) error,
    opts ...RoutineOption,
) *Routine

// Run a supervised routine that can restart based on policy/backoff
func (o *Orchestrator) GoSupervised(
    fn func(ctx context.Context, self *Routine) error,
    opts ...SupervisorOption,
) *Routine

// Lookup & listing
func (o *Orchestrator) Get(id uint64) (*Routine, bool)
func (o *Orchestrator) List() []*Routine

// Printing current stats in a table
func (o *Orchestrator) PrintStats(w io.Writer)

// Stop all routines now (best-effort cancel)
func (o *Orchestrator) KillAll()

// Wait for drain with a bound (best-effort)
func (o *Orchestrator) Shutdown(d time.Duration) error

// Stable snapshot for metrics/UI
type PublicSnapshot struct {
    ID         uint64 `json:"id"`
    Name       string `json:"name"`
    State      string `json:"state"` // INIT/RUNNING/STOPPING/TIMED_OUT/PANICKED/STOPPED
    Health     Health `json:"health"` // OK/IDLE_TIMEOUT/STOPPING/...
    QueueLen   int    `json:"queueLen"`
    QueueBytes int64  `json:"queueBytes"`
    Restarts   uint64 `json:"restarts"`
}
func (o *Orchestrator) PublicSnapshots() []PublicSnapshot
```

### Routine API

```go
type RoutineState int32
const (
    StateInit StateRunning StateStopping StateTimedOut StatePanicked StateStopped
)

func (r *Routine) ID() uint64
func (r *Routine) Name() string
func (r *Routine) Context() context.Context
func (r *Routine) Mailbox() *Channel[any]

func (r *Routine) Restarts() uint64
func (r *Routine) Beat()                    // heartbeat (resets idle timer)
func (r *Routine) AddBusy(d time.Duration)  // add "busy" time (approx CPU)
func (r *Routine) Kill()
func (r *Routine) Wait() error

// Routine options
func WithName(n string) RoutineOption
func WithQueueCap(n int) RoutineOption
func WithIdleTimeout(d time.Duration) RoutineOption
```

### Channel & Bus API

```go
type Sizer interface{ SizeBytes() int }

type Channel[T any] struct{ /* ... */ }
func NewChannel[T any](capacity int) *Channel[T]
func (c *Channel[T]) Send(ctx context.Context, v T) error
func (c *Channel[T]) Recv(ctx context.Context) (T, error)
func (c *Channel[T]) Len() int
func (c *Channel[T]) Cap() int
type ChannelStats struct {
    Len, Cap      int
    Sends, Recvs  uint64
    BlockedSendNs int64
    BlockedRecvNs int64
    ApproxBytes   int64
}
func (c *Channel[T]) Stats() ChannelStats

type Bus[T any] struct{ /* ... */ }
func NewBus[T any]() *Bus[T]
func (b *Bus[T]) Topic(name string, capacity int) *Channel[T]
```

### Supervisor options

```go
type RestartPolicy int
const (
    RestartNever RestartOnFailure RestartAlways
)

type SupervisorOption func(*SupervisorConfig)
func WithSupName(n string) SupervisorOption
func WithSupPolicy(p RestartPolicy) SupervisorOption
// initial, max, multiplier (>=1), jitter (0..1)
func WithSupBackoff(initial, max time.Duration, mult float64, jitter float64) SupervisorOption
func WithSupIdleTimeout(d time.Duration) SupervisorOption
func WithSupQueueCap(n int) SupervisorOption
```

### Observability options

```go
// obs.NewServer wires HTTP mux with metrics, dashboard and optional pprof
s := obs.NewServer(orch,
    obs.WithAddr(":9090"),
    obs.WithPProf(true),
    obs.WithDashboard(true),
    obs.WithCPUSampleEvery(500*time.Millisecond),
    obs.WithTopicSampleEvery(1*time.Second),
    // Optionally use your own Prometheus registry:
    // obs.WithRegistry(prometheus.NewRegistry()),
)

// Control server lifecycle
if err := s.Start(); err != nil { /* ... */ }
_ = s.Stop(context.Background())
```

The server mounts:
- `/metrics` (Prometheus; uses the same snapshots as the UI)
- `/gorchestra` (static HTML dashboard)
- `/debug/pprof/*` (when enabled)

---

## Testing & Leak Safety

The repo includes `goleak_test.go` with `go.uber.org/goleak` integration to ensure shutdown paths don’t leak goroutines.

Tips:
- Always `Kill()` routine(s) and `Shutdown()` the orchestrator at test end.
- Prefer short timeouts in tests; use `t.Helper()` wrappers if you need.
- For flaky external deps, wrap the worker in **GoSupervised** with `RestartOnFailure` and assert restart counts.

---

## Design Notes & Guarantees

- **No hidden magic**: You control contexts and cancellation boundaries.
- **Idle watchdog**: If `WithIdleTimeout` > 0 and the routine doesn’t call `Beat()` within that period, gorchestra cancels it with `StateTimedOut`.
- **Busy/CPU approximation**: `AddBusy` and measured channel blocking capture a rough “how hot is this routine?” signal.
- **Payload sizing**: If your channel payload implements `Sizer`, queue bytes are estimated; otherwise bytes may be `0`.
- **Best-effort shutdown**: `KillAll()` cancels; `Shutdown(d)` waits up to `d` (total) for all routines to exit.
- **Thread-safe**: Public orchestrator methods use RW locks where appropriate.
- **No panics**: Library code avoids panics; your worker may panic — use `GoSupervised` with `RestartOnFailure/Always` for resilience.

---

## FAQ

**Q: Do I need the dashboard to use gorchestra?**  
No. You can use only the orchestration pieces. The obs server is optional.

**Q: Can I plug metrics into an existing HTTP server?**  
Yes. Use `metrics.NewPrometheusCollector(orch)` and register it on your own `*prometheus.Registry` or the default one.

**Q: Is CPU% accurate per goroutine?**  
It’s an approximation based on what gorchestra can observe (blocked times, explicit busy windows). For precision, rely on end-to-end timings and system profilers.

**Q: How do I stop a stuck worker?**  
Call `Kill()` on the `Routine`, or configure an `IdleTimeout` and ensure your worker calls `Beat()` when alive.

**Q: What Go versions are supported?**  
Go 1.23+ (see `go.mod`).

---

## Version & Requirements

- Go **1.23+** (toolchain 1.24 supported)
- Optional: `github.com/prometheus/client_golang` if you enable metrics/server
- Optional: `go.uber.org/goleak` for tests

---

## License

MIT (or your preferred OSS license). Replace this section if you ship a different one.
