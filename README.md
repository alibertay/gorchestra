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
  - [Lifecycle & states](#lifecycle--states)
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
- 🔒 **Hardened lifecycle state machine** — terminal states (`STOPPED`, `TIMED_OUT`, `PANICKED`) are final
- 💓 **Heartbeat & opt-in idle-timeout** guard (stop workers that go quiet)
- 🔁 **Supervisor** with **panic recovery**, **restart policies**, **exponential backoff + jitter** and **stable-window backoff reset**
- 🧹 **Bounded history** for finished routines (active registry + ring buffer, no unbounded growth)
- ✉️ **Typed mailbox/channel** with per-channel stats (len/cap/blocked/bytes)
- 🚌 **Thread-safe named bus** to create topic channels on demand
- 📊 **Prometheus metrics** + **/metrics** endpoint with a bounded-cardinality strategy
- 📈 **Tiny HTML dashboard** at `/gorchestra` (sortable table + live CPU/queues)
- 🧠 **goleak**-friendly tests (no goroutine leaks) + `go test -race` in CI
- 🧹 **Graceful shutdown** with bounded wait, idempotent `Shutdown`/`Stop`
- 🧰 No heavy framework; import what you need

> Go’s runtime doesn’t expose per-goroutine CPU/mem. gorchestra approximates busyness only from what you explicitly instrument (`AddBusy`, mailbox blocking). Treat `BusyPercent` as a helpful, not exact, signal — an uninstrumented worker reports `0`, never a fabricated `100`.

---

## Install

```bash
go get github.com/alibertay/gorchestra
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

    g "github.com/alibertay/gorchestra"
)

func main() {
    orch := g.New()

    // Start a managed routine.
    // The idle watchdog is opt-in: pass WithIdleTimeout(d) and call Beat()
    // while the worker is healthy.
    r := orch.Go(func(ctx context.Context, self *g.Routine) error {
        ticker := time.NewTicker(250 * time.Millisecond)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return ctx.Err()
            case <-ticker.C:
                self.Beat()                       // heartbeat (resets idle timer)
                self.AddBusy(10 * time.Millisecond) // optional: record work time
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

Or create a **bus** of named topics (safe for concurrent use):

```go
bus := g.NewBus[[]byte]()
topic := bus.Topic("price-feed", 4096)
_ = topic.Send(ctx, payload)
```

### Supervise with restart backoff

```go
r := orch.GoSupervised(func(ctx context.Context, self *g.Routine) error {
    // do risky work; return an error (or panic) to trigger a restart
    return doWork(ctx)
},
    g.WithSupName("super-worker"),
    g.WithSupPolicy(g.RestartOnFailure), // RestartNever | RestartOnFailure | RestartAlways
    g.WithSupBackoff(500*time.Millisecond, 30*time.Second, 2.0, 0.2), // initial, max, multiplier, jitter
    g.WithSupStableWindow(30*time.Second), // a stable attempt resets the backoff
    g.WithSupIdleTimeout(10*time.Second),  // opt-in; the supervisor heartbeats during backoff
    g.WithSupQueueCap(128),
)
_ = r
```

Panics inside a supervised worker are recovered per attempt and flow through the restart policy instead of killing the routine.

### Expose metrics & dashboard

```go
import (
    "log"
    obs "github.com/alibertay/gorchestra/obs"
    g "github.com/alibertay/gorchestra"
)

func main() {
    orch := g.New()

    s := obs.NewServer(orch,
        obs.WithAddr("127.0.0.1:9090"),
        obs.WithPProf(false),      // opt-in; disabled by default
        obs.WithDashboard(true),
        obs.WithCPUSampleEvery(500*time.Millisecond),
        obs.WithTopicSampleEvery(1*time.Second),
    )

    // Option A: serve in the background (bind errors are returned here)
    if err := s.StartAsync(); err != nil {
        log.Fatal(err)
    }

    // Option B: block (Start returns bind/serve errors)
    // if err := s.Start(); err != nil { log.Fatal(err) }

    // ...
    // _ = s.Stop(context.Background()) // idempotent
}
```

- **Dashboard**: `GET http://127.0.0.1:9090/gorchestra`
- **Prometheus**: `GET http://127.0.0.1:9090/metrics`
- **pprof**: `GET http://127.0.0.1:9090/debug/pprof/` (only when enabled)

> You can also hook directly into Prometheus without the server via `metrics.NewPrometheusCollector(orch)`.

### Graceful shutdown

```go
orch.KillAll()
if err := orch.Shutdown(5 * time.Second); err != nil {
    log.Printf("shutdown timed out: %v", err)
}
```

`Shutdown` and `obs.Server.Stop` are idempotent: calling them again is a no-op.

---

## Core Concepts

### Orchestrator

Central registry of **active** goroutines. Spawns routines, tracks their state, prints stats, shuts them down, and exposes snapshots for metrics/UI. Finished routines are retired from the active registry into a **bounded history ring** (default: last 1000, configurable with `WithHistoryLimit`).

### Routine

A managed goroutine with a unique ID & name. Provides a **context**, **heartbeat** (`Beat()`), **busy-time recording** (`AddBusy`), a **mailbox** (`Mailbox()`), **restart counter** (`Restarts()`), `Kill()`, `Wait()` and `State()` helpers.

The **idle watchdog is opt-in**: pass `WithIdleTimeout(d > 0)` and call `Beat()` while the worker is healthy. If the routine goes quiet for `d`, it is cancelled with `StateTimedOut` and `Wait()` returns `ErrIdleTimeout`.

### Lifecycle & states

```text
INIT
 │
 ▼
RUNNING ──(fn returns)──────────────→ STOPPED
   │
   ├── Kill() ──────────────────────→ STOPPING → STOPPED
   ├── idle watchdog ───────────────→ TIMED_OUT
   └── panic ───────────────────────→ PANICKED
```

`STOPPED`, `TIMED_OUT` and `PANICKED` are **terminal**: once reached, no transition (including a late `Kill()`) can change them. A routine that is killed before its goroutine starts finishes as `STOPPED` without invoking `fn`.

### Channel & Bus

A typed channel wrapper with additional stats and (optional) payload sizing via `Sizer{ SizeBytes() int }`. A `Bus[T]` lets you request or create named topic channels on demand; `Topic()` is safe for concurrent callers and the first caller wins.

### Supervisor

A wrapper that runs `fn` under a restart policy and exponential backoff with jitter. Each attempt runs inside its own panic boundary. Attempts that ran at least `WithSupStableWindow` reset the backoff to its initial value, and the supervisor keeps heartbeating while waiting out a backoff delay.

### Observability Server

A small HTTP server that ships with gorchestra. It serves Prometheus metrics, a tiny dashboard, and optionally pprof. It also samples **process CPU usage** and exports gauges for topic lengths/bytes so you see backpressure build-ups. Safe defaults: listens on `127.0.0.1:9090` and pprof is disabled unless requested.

### Prometheus Collector

If you already have your own HTTP stack, register the standalone collector and expose it yourself.

Cardinality strategy:

- per-routine series (`id` label) exist **only while the routine is active**
- finished routines are aggregated into `gorchestra_routines_terminal_total{name,state}`, bounded by worker names — not by routine IDs

---

## API Reference

> Package import paths (shortened below):
>
> - Core: `github.com/alibertay/gorchestra`
> - Observability: `github.com/alibertay/gorchestra/obs`
> - Prometheus collector: `github.com/alibertay/gorchestra/metrics`

### Orchestrator API

```go
// Create a new orchestrator (optional: WithHistoryLimit)
func New(opts ...OrchestratorOption) *Orchestrator

// Bound the finished-routine history ring (0 disables it; default 1000)
func WithHistoryLimit(n int) OrchestratorOption

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

// Lookup & listing (active routines only)
func (o *Orchestrator) Get(id uint64) (*Routine, bool)
func (o *Orchestrator) List() []*Routine

// Bounded history of finished routines
func (o *Orchestrator) History() []RoutineRecord
func (o *Orchestrator) GetRecord(id uint64) (RoutineRecord, bool)
func (o *Orchestrator) TerminalCounts() []TerminalCount

// Printing current stats in a table
func (o *Orchestrator) PrintStats(w io.Writer)

// Stop all routines now (best-effort cancel)
func (o *Orchestrator) KillAll()

// Wait for drain with a bound (best-effort, idempotent)
func (o *Orchestrator) Shutdown(d time.Duration) error

// Stable snapshot for metrics/UI (active routines)
type PublicSnapshot struct {
    ID          uint64  `json:"id"`
    Name        string  `json:"name"`
    State       string  `json:"state"` // INIT/RUNNING/STOPPING/TIMED_OUT/PANICKED/STOPPED
    Health      Health  `json:"health"` // OK/IDLE_TIMEOUT/STOPPING/PANIC
    UptimeSec   float64 `json:"uptimeSeconds"`
    IdleSec     float64 `json:"idleSeconds"`
    BusySec     float64 `json:"busySeconds"`
    BlockedSec  float64 `json:"blockedSeconds"`
    BusyPercent float64 `json:"busyPercent"`
    QueueLen    int     `json:"queueLen"`
    QueueBytes  int64   `json:"queueBytes"`
    Restarts    uint64  `json:"restarts"`
}
func (o *Orchestrator) PublicSnapshots() []PublicSnapshot
```

### Routine API

```go
type RoutineState int32
const (
    StateInit StateRunning StateStopping StateTimedOut StatePanicked StateStopped
)

// Terminal reports whether the state is final.
func (s RoutineState) Terminal() bool

// ErrIdleTimeout is returned by Wait() when the idle watchdog fired.
var ErrIdleTimeout error

func (r *Routine) ID() uint64
func (r *Routine) Name() string
func (r *Routine) State() RoutineState
func (r *Routine) Context() context.Context
func (r *Routine) Mailbox() *Channel[any]

func (r *Routine) Restarts() uint64
func (r *Routine) Beat()                    // heartbeat (resets idle timer)
func (r *Routine) AddBusy(d time.Duration)  // add "busy" time (approx CPU)
func (r *Routine) Kill()                    // no-op on terminal routines
func (r *Routine) Wait() error

// Routine options
func WithName(n string) RoutineOption
func WithQueueCap(n int) RoutineOption
func WithIdleTimeout(d time.Duration) RoutineOption // 0 disables (default)
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
func (b *Bus[T]) Topic(name string, capacity int) *Channel[T] // thread-safe
func (b *Bus[T]) Topics() []string
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
// attempts running at least d reset the backoff (0 disables; default 30s)
func WithSupStableWindow(d time.Duration) SupervisorOption
// 0 disables the idle watchdog (default); supervisor heartbeats during backoff
func WithSupIdleTimeout(d time.Duration) SupervisorOption
func WithSupQueueCap(n int) SupervisorOption
```

### Observability options

```go
// obs.NewServer wires HTTP mux with metrics, dashboard and optional pprof
s := obs.NewServer(orch,
    obs.WithAddr("127.0.0.1:9090"),
    obs.WithPProf(false),     // default false
    obs.WithDashboard(true),  // default true
    obs.WithCPUSampleEvery(500*time.Millisecond),
    obs.WithTopicSampleEvery(1*time.Second),
    // Optionally use your own Prometheus registry:
    // obs.WithRegistry(prometheus.NewRegistry()),
)

// Lifecycle
if err := s.StartAsync(); err != nil { /* bind error */ } // background
// or: if err := s.Start(); err != nil { /* blocks until Stop */ }
_ = s.Addr() // actual listening address
_ = s.Stop(context.Background()) // idempotent
```

The server mounts:
- `/metrics` (Prometheus; uses the same snapshots as the UI)
- `/gorchestra` (static HTML dashboard + `/gorchestra/snapshots`, `/gorchestra/topics`) — only when the dashboard is enabled
- `/debug/pprof/*` (only when enabled)
- `/healthz`

---

## Testing & Leak Safety

The repo integrates `go.uber.org/goleak` in the core and `obs` packages to ensure shutdown paths don’t leak goroutines, and CI runs the whole suite with the race detector:

```bash
go test ./...
go test -race ./...
```

Covered behaviors include: normal completion, `Kill`, idle timeout, panic, terminal-state stability, supervised error/panic restarts, `RestartNever`/`RestartAlways`, backoff reset/cap/jitter, concurrent `Bus.Topic`, concurrent channel send/recv, shutdown timeout, repeated `Shutdown`/`Stop`, bounded history, and metric cardinality.

Tips:
- Always `Kill()` routine(s) and `Shutdown()` the orchestrator at test end.
- Prefer short timeouts in tests; use `t.Helper()` wrappers if you need.
- For flaky external deps, wrap the worker in **GoSupervised** with `RestartOnFailure` and assert restart counts.

---

## Design Notes & Guarantees

- **No hidden magic**: You control contexts and cancellation boundaries.
- **Lifecycle**: explicit state machine; terminal states (`STOPPED`, `TIMED_OUT`, `PANICKED`) are final. The routine context is cancelled when the worker exits, so watchdogs never outlive the routine.
- **Idle watchdog is opt-in**: only `WithIdleTimeout(d > 0)` enables it; on timeout the state becomes `TIMED_OUT` and `Wait()` returns `ErrIdleTimeout`.
- **Supervision**: panics are recovered per attempt and treated as failures; backoff resets after a stable attempt; heartbeats continue during backoff.
- **Bounded memory**: finished routines leave the active registry; history is a fixed-size ring.
- **Busy metric**: `BusyPercent` reflects only explicit `AddBusy` instrumentation.
- **Payload sizing**: If your channel payload implements `Sizer`, queue bytes are estimated; otherwise bytes may be `0`.
- **Best-effort shutdown**: `KillAll()` cancels; `Shutdown(d)` waits up to `d` (total) for all routines to exit; repeated calls are safe.
- **Thread-safe**: Public orchestrator, bus and server methods are safe for concurrent use.
- **No panics from the library**: worker panics are captured by the routine or the supervisor.

---

## FAQ

**Q: Do I need the dashboard to use gorchestra?**  
No. You can use only the orchestration pieces. The obs server is optional, and `obs.WithDashboard(false)` disables `/gorchestra` routes entirely.

**Q: Can I plug metrics into an existing HTTP server?**  
Yes. Use `metrics.NewPrometheusCollector(orch)` and register it on your own `*prometheus.Registry` or the default one.

**Q: Is BusyPercent accurate per goroutine?**  
It’s strictly what you instrumented with `AddBusy` relative to uptime. Uninstrumented workers report `0`. For precision, rely on end-to-end timings and system profilers.

**Q: How do I stop a stuck worker?**  
Call `Kill()` on the `Routine`, or enable an idle timeout (`WithIdleTimeout`) and call `Beat()` when the worker is alive.

**Q: What happens if a supervised worker panics?**  
The panic is recovered, converted into an error, and the restart policy decides: `RestartOnFailure`/`RestartAlways` restart it with backoff, `RestartNever` surfaces the error through `Wait()`.

**Q: What Go versions are supported?**  
Go 1.23+ (see `go.mod`).

---

## Version & Requirements

- Go **1.23+** (toolchain 1.24 supported)
- CI: GitHub Actions runs `go build`, `go vet` and `go test -race ./...`
- Optional: `github.com/prometheus/client_golang` if you enable metrics/server
- Optional: `go.uber.org/goleak` for tests

---

## License

MIT — see [LICENSE](LICENSE).
