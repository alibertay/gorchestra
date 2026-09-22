# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/alibertay/gorchestra/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/alibertay/gorchestra/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/alibertay/gorchestra/releases/tag/v0.1.0
