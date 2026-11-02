# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.1] - 2026-11-02

### Security
- Added optional HTTP Basic Authentication (`auth.username` / `auth.password` and `KAMAL_WATCH_AUTH_USER` / `KAMAL_WATCH_AUTH_PASSWORD` environment overrides) with constant-time credential comparison (#3, #9).
- Added HTTP `Host` header validation (`allowed_hosts` config and automatic `localhost`/`127.0.0.1`/`::1` enforcement on loopback binds) to guard against DNS-rebinding attacks (#3, #9).

### Added
- Unauthenticated `GET /up` healthcheck endpoint for `kamal-proxy` and container probes (#3, #9).
- Accurate startup listen address logging (`FormatListenAddr`) distinguishing `0.0.0.0` (all interfaces) from loopback binds (#3, #9).

## [0.3.0] - 2026-11-02

### Added
- Configurable per-server concurrency cap for `/logs/stream` SSH sessions (`max_concurrent_log_streams`, default `6`), returning `429 Too Many Requests` when saturated (#5, #8).
- HTTP server `ReadHeaderTimeout` (`10s`) and `IdleTimeout` (`120s`) to guard against slowloris header stalls while preserving long-lived SSE streams (#5, #8).

## [0.2.6] - 2026-11-02

### Changed
- Improved SSH connection pool resilience with per-host exponential backoff (`4s` up to `60s`) on dial failures, automatic client invalidation on session/timeout errors, and clean pool teardown on shutdown (#2, #7).

## [0.2.5] - 2026-11-02

### Security
- Verify SSH server host keys against `~/.ssh/known_hosts` (or a configured global/per-server `known_hosts_path`) by default instead of `ssh.InsecureIgnoreHostKey()`, with an optional `insecure_ignore_host_key` override (#1, #6).

### Added
- `CONTRIBUTING.md` contributor guide and GitHub issue templates.

## [0.2.4] - 2026-11-01

### Fixed
- Replaced the outer `<video>` wrapper in `README.md` (which GitHub strips along with its child elements) with a clickable `<a><img></a>` preview linking directly to the full-resolution `.mp4` on `media.jefflunt.com`.

## [0.2.3] - 2026-11-01

### Added
- Embedded demo video (`https://media.jefflunt.com/code/kamal-watch/kamal-watch-demo.mp4` with animated GIF fallback for GitHub Markdown sanitization) at the top of `README.md`.
- Compile-time version embedding (`version.go` via `//go:embed VERSION`) exposed to web templates.

### Changed
- Replaced the `"LIVE"` label in the dashboard top navigation bar with the running application version (`v0.2.3`).

## [0.2.2] - 2026-11-01

### Added
- MIT `LICENSE` for open-source distribution.
- Public quickstart `README.md` covering design philosophy, early-alpha private-network security warnings, local setup, Docker usage, and self-hosted Kamal deployment.

### Changed
- Updated Go module path to `github.com/jefflunt/kamal-watch` to support direct `go install`.
- Added `-trimpath` to `cmd/build` and `Dockerfile` builds to strip local filesystem paths from compiled binaries.
- Defaulted `kamal-watch.example.yml` to `127.0.0.1:8080` and tightened `.gitignore` and `.dockerignore` rules for config and SSH key patterns.

## [0.2.1] - 2026-11-01

### Changed
- Right-justified table headers and cell data for "MEM USAGE" and "NET I/O" columns across the cluster dashboard and app detail view for consistent visual alignment with numeric telemetry.

## [0.2.0] - 2026-11-01

### Added
- Aggregate RAM memory usage calculation and display (e.g. `587.3MiB`) at the logical service level across all child containers (web, workers, accessories).
- Docker stats byte parser (`parseBytes` & `formatBytes`) to sum container memory footprints accurately into human-readable quantities.
- Service summary banner on the app detail page showing aggregate CPU and RAM metrics.

## [0.1.0] - 2026-11-01

### Added
- Multi-server inventory monitoring over SSH key authentication.
- Full auto-discovery of Kamal services, roles (`web`, `job`), and accessories (`db`, `redis`, etc.).
- Kamal-proxy route discovery, virtual host mapping, and TLS status inspection.
- Host telemetry meters: 2-column `htop` style gauges for CPU usage, Cores, Memory, Swap, and Root Disk.
- Aggregate and per-container CPU% and MEM% utilization.
- Real-time Docker log streaming with SSE, client-side regex/text filtering, and auto-scroll.
- Collapsible service groups defaulting to collapsed mode with state preservation across auto-refreshes.
- Dedicated service detail pages with Kamal deployment audit logs and rollback version histories.
- Branding icon and SVG favicon using Concept 2 (The Phosphor Viewfinder `[ • ]`).
- Multi-stage `Dockerfile` for containerized deployment.
