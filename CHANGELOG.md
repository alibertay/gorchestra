# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-09-22

Lifecycle hardening release (second technical review): orchestrator close
semantics, supervisor state visibility, deterministic Wait, honest blocked
metrics, dashboard v2 and stress/fuzz coverage.

### Added

- **Orchestrator lifecycle** `OPEN → CLOSING → CLOSED` with
  `Orchestrator.State()`. `Shutdown` stops accepting new routines; `TryGo`
  reports `ErrOrchestratorClosed` and `Go` returns an already-finished
  handle whose `Wait` surfaces the error. A timed-out `Shutdown` stays
  `CLOSING` so a later call can complete the drain.
- **Supervisor state visibility**: `SupervisorState`
  (`NONE`/`RUNNING`/`BACKOFF`/`STOPPING`) on `Routine`, in
  `Snapshot`/`PublicSnapshot` (`supervisorState`) and as the
  `gorchestra_supervisor_state{id,name,phase}` metric.
- `WithTerminalCardinalityLimit(n)` and `OverflowRoutineName` (`__other__`):
  caps distinct terminal counter series; overflow is aggregated.
- **Dashboard v2**: activity columns (uptime, idle, busy%, blocked, queue
  bytes), supervisor phase, plus history and terminal-count tables.
- `/gorchestra/history` and `/gorchestra/terminals` JSON endpoints.
- `PublicSnapshot.queueCap`.
- Stress tests (10k routines, TryGo/Shutdown races, rapid create/destroy,
  supervisor restart storms, high topic throughput) and
  `FuzzRoutineStateTransitions`.

### Changed

- `Wait()` now also waits for history retirement, making
  `Wait(); GetRecord(id)` deterministic.
- `TryGo` racing with `Shutdown` can never add to the WaitGroup after the
  drain begins.
- `obs.WithCPUSampleEvery` / `obs.WithTopicSampleEvery` ignore non-positive
  durations (defaults kept) instead of panicking at runtime.
- Channel cancellation is deterministic: an already-cancelled context fails
  even when buffer space is available.
- Routine names are documented as low-cardinality labels.

### Fixed

- `BlockedSendNs` / `BlockedRecvNs` no longer include timing noise from
  non-blocking operations; only real waits are measured.
- Dashboard escapes HTML in routine/topic values.

## [0.2.0] - 2026-09-22

Hardening release: lifecycle semantics, supervisor resilience, bounded
memory/metrics, observability lifecycle fixes and library maturity.

### Added

- **Lifecycle state machine**: `Routine.State()`, `RoutineState.Terminal()`
  and the exported `ErrIdleTimeout` error. Terminal states (`STOPPED`,
  `TIMED_OUT`, `PANICKED`) are final.
- **Bounded routine history**: `New(WithHistoryLimit(n))` (default 1000),
  `Orchestrator.History()`, `GetRecord()`, `TerminalCounts()` plus the
  `RoutineRecord` and `TerminalCount` types. Finished routines are retired
  from the active registry.
- **Supervisor panic recovery**: panics inside a supervised worker are
  recovered per attempt and flow through the restart policy.
- `WithSupStableWindow(d)` (default 30s): attempts that ran at least `d`
  reset the exponential backoff to its initial value.
- The supervisor heartbeats while waiting out a backoff delay.
- `Bus.Topics()` accessor; `Bus.Topic()` is now safe for concurrent callers.
- **Observability**: `obs.Server.StartAsync()` (non-blocking, reports bind
  errors synchronously) and `obs.Server.Addr()`.
- **Metrics**: `gorchestra_routines_terminal_total{name,state}` (bounded
  cardinality), `gorchestra_routine_busy_seconds`,
  `gorchestra_routine_blocked_seconds`.
- `PublicSnapshot` activity fields: `uptimeSeconds`, `idleSeconds`,
  `busySeconds`, `blockedSeconds`, `busyPercent`.
- Comprehensive test suite (lifecycle, supervision, backoff, concurrency,
  shutdown, history, metrics, obs server) with `go.uber.org/goleak` in the
  core and `obs` packages.
- GitHub Actions CI running `go build`, `go vet` and `go test -race ./...`.
- MIT `LICENSE`.
- `.gitignore`.

### Changed (breaking)

- `obs.Server.Start()` now **blocks** and returns bind/serve errors; use
  `StartAsync()` for background serving.
- Idle timeout default changed from 5s to **0 (disabled, opt-in)**;
  `WithIdleTimeout(0)` now genuinely disables the watchdog.
- `Orchestrator.Get()` / `List()` now return **active routines only**;
  finished routines live in the bounded history.
- `Snapshot.ActivePercent` replaced by `Snapshot.BusyPercent`
  (`AddBusy / uptime`); `PrintStats` now shows `BUSY%`, `BUSY`, `BLOCKED`.
- Safer observability defaults: `Addr` is `127.0.0.1:9090` and
  `EnablePProf` is `false` by default.
- `obs.Server.Stop()` is idempotent and waits for sampler goroutines.
- `Orchestrator.Shutdown()` is idempotent and handles `d <= 0`.
- Supervisor idle timeout is disabled by default, consistent with routines.
- `go mod tidy`: `prometheus/client_golang` and `client_model` are direct
  dependencies.

### Fixed

- The idle watchdog no longer outlives the worker on natural completion.
- `TIMED_OUT` is preserved (no more silent downgrade to `STOPPED`) and
  `Wait()` returns `ErrIdleTimeout`.
- `Kill()` on a finished routine no longer revives it as `STOPPING`.
- A routine killed before its goroutine starts finishes as `STOPPED`
  without invoking `fn`.
- Data race in `Bus.Topic()` under concurrent access.
- Supervised workers are restarted after panics instead of dying.
- `obs.Server.Start()` used to return `nil` even when the port was already
  in use; errors are now surfaced.
- `obs.WithDashboard(false)` actually disables `/gorchestra*` routes.
- Double `Stop()` no longer panics (`sync.Once`).
- Activity metric no longer reports a sleeping worker as 100% active.
- Prometheus cardinality no longer grows with every finished routine.

## [0.1.0] - 2025-10-27

Initial public version: `Orchestrator` + `Routine`, `Supervisor` with
restart policies and exponential backoff, typed `Channel[T]`/`Bus[T]`,
observability server (metrics, dashboard, pprof, healthz) and a Prometheus
collector.

[Unreleased]: https://github.com/alibertay/gorchestra/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/alibertay/gorchestra/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/alibertay/gorchestra/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/alibertay/gorchestra/releases/tag/v0.1.0
