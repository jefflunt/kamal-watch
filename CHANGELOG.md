# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.7.1] - 2026-11-03

### Added
- Added `cmd/deploy` to `.gitignore` to support local deployment scripting without committing environment-specific deploy scripts.

## [0.7.0] - 2026-11-03

### Added
- Per-core CPU utilization meters displaying individual core percentage bars arranged in a responsive multi-column grid, calculated from `/proc/stat` deltas during SSH polling cycles.
- Interactive CPU view toggle: clicking the CPU core count / model number label toggles between the unified CPU meter and the per-core meters across all servers, with view preference persisted in `localStorage`.
- Automatic CPU model detection and sanitization from `/proc/cpuinfo` (e.g. `AMD 5950X`, `Intel Xeon Gold 6248R`).

### Changed
- Server header labels now render as `<server-name>@<host>` (e.g., `compute-01@192.168.0.31`) removing the previous `SERVER:` prefix.
- CPU meter headers now format as `<sockets>x<cores> [<model>]` (e.g., `1x32 [AMD 5950X]`) instead of `1 [CPU Total: 32 Cores]`.
- Logical service rows now display the direct service name without the redundant `[service]` prefix.

## [0.6.1] - 2026-11-02

### Fixed
- Updated demo video and preview GIF URLs in `README.md` to point to `media.jefflunt.com/misc/kamal-watch/`.

## [0.6.0] - 2026-11-02

### Added
- Automated GitHub Release publishing on version tag pushes (`v*`), cross-compiling release binaries for Linux (`amd64`, `arm64`) and macOS (`amd64`, `arm64`) with SHA-256 checksums and automatic release notes.

## [0.5.0] - 2026-11-02

### Added
- Zero-overhead detection of host reboot-required state (`/run/reboot-required`, `/run/reboot-required.pkgs`, `needs-restarting -r`) and cached pending OS/security updates (`/var/lib/update-notifier/updates-available`, `/var/run/motd.dynamic`) during the SSH poll cycle (#11, #12).
- Host maintenance badges in the dashboard and server cards with interactive tooltips detailing reboot-triggering packages and update summaries (#11, #12).
- Automated GitHub Actions CI workflow (`.github/workflows/ci.yml`) running in a `golang:alpine` container triggered on version tag pushes (`v*`) and manual dispatch to test and compile the project (#13, #14).

## [0.4.0] - 2026-11-02

### Security
- Replaced the runtime `https://cdn.tailwindcss.com` external script with a compiled, embedded stylesheet (`GET /assets/styles.css`) for full offline/air-gapped operation and zero third-party script dependencies (#4, #10).
- Added security response headers (`Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`) across all HTTP responses (#4, #10).

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
