# Architecture & System Design

## Overview
`kamal-watch` (`github.com/jefflunt/kamal-watch`) is an agentless, lightweight multi-server telemetry dashboard and log observer designed specifically for observing applications deployed with [Kamal](https://kamal-deploy.org/). It operates strictly inside private networks (`127.0.0.1`, LAN, VPC, or VPN) and polls target hosts directly over SSH without installing host-side agents.

## Core Components

1. **Remote SSH Pool (`pkg/remote/ssh.go`)**:
   - Manages persistent SSH client connections using private key authentication and verifies server host keys against `~/.ssh/known_hosts` (or a configured `known_hosts_path`) by default, with an optional `insecure_ignore_host_key` override.
   - Tracks per-host exponential backoff (`4s` up to `60s`) on dial failures (`BackoffError`), invalidates broken or timed-out client connections automatically, and closes all pooled SSH connections cleanly on shutdown.
   - Provides safe thread-safe execution of diagnostic commands (`uptime`, `docker stats`, `docker ps`, `kamal-proxy`) and real-time streaming command execution (`docker logs --follow`).

2. **Telemetry Monitor (`pkg/monitor/monitor.go`)**:
   - Periodically polls configured hosts at a defined cadence (default 4s).
   - Ingests:
     - Host resource health: Load averages, CPU sockets/cores/model numbers, per-core CPU utilization (via `/proc/stat` deltas), RAM / Swap utilization, root disk space.
     - Host maintenance posture: Reboot-required detection (`/run/reboot-required`, `/run/reboot-required.pkgs`, `needs-restarting -r`) and cached pending OS/security updates (`/var/lib/update-notifier/updates-available`, `/var/run/motd.dynamic`).
     - Docker container status: Active vs exited containers, image tags, role labels (`web`, `job`, accessories).
     - Kamal proxy integration: Routes, virtual hosts, TLS certificate status.
     - Kamal audit logs: Deployment history from `$HOME/.kamal/<app>-audit.log`.
   - Computes aggregated CPU% and MEM% across logical service groups.

3. **Web Server & UI (`pkg/web/server.go`, `pkg/web/assets/`, & `pkg/web/templates/`)**:
   - Monospaced, high-density terminal interface inspired by `htop`, displaying the embedded release version (`version.go` -> `VERSION`) in the top navigation bar and serving self-contained compiled CSS (`GET /assets/styles.css`) with zero external CDN dependencies.
   - Live telemetry updates without full page reloads via `/partial/servers` DOM swapping.
   - Preserves user interaction state (collapsed/expanded services and unified vs. per-core CPU meter view preference in `localStorage`) across periodic refreshes.
   - Simplified host headers (`<name>@<host>`) and logical service rows without redundant bracket tags.
   - Interactive CPU meter header displaying `<sockets>x<cores> [<model>]` with click-to-toggle between unified and multi-column per-core utilization meters across all servers.
   - Interactive per-host settings dialog (opened via gear icon left of host/IP) configuring separate multi-line "install updates" and "reboot" commands stored in `localStorage`, coupled with non-modal direct background execution on pill clicks (`POST /api/server/{name}/run`), live animated pill spinners during execution with a minimum 5-second duration on reboot, dynamic reboot pill preservation across background refreshes, and compact `OFFLINE` status badges replacing verbose SSH errors with hover tooltips.
   - Slide-over terminal modal with SSE log streaming, log filtering, and auto-scroll, protected by a per-server concurrency cap (`max_concurrent_log_streams`, default `6`, returning HTTP `429` when saturated) and HTTP `ReadHeaderTimeout` / `IdleTimeout` limits.
   - Optional HTTP Basic Authentication (`auth.username` / `auth.password` or `KAMAL_WATCH_AUTH_USER` / `KAMAL_WATCH_AUTH_PASSWORD` env overrides), unauthenticated `GET /up` healthcheck endpoint, HTTP `Host` header allowlist middleware (`allowed_hosts` plus automatic loopback DNS-rebinding protection), and hardened response headers (`Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`).
   - Concept 2 ("The Phosphor Viewfinder `[ • ]`") branding icon and vector SVG favicon.

4. **CI & Build Pipeline (`.github/workflows/ci.yml`, `cmd/build`, `Dockerfile`)**:
   - Automated GitHub Actions workflow triggered on version tag pushes (`v*`) and manual workflow dispatch.
   - Executes inside a `golang:alpine` container mirroring the multi-stage `Dockerfile` build environment.
   - Validates module dependencies, executes the full test suite (`go test -v ./...`), and verifies clean binary compilation via `cmd/build`.
   - On tagged pushes, automatically cross-compiles release binaries for Linux (`amd64`, `arm64`) and macOS (`amd64`, `arm64`), generates SHA-256 checksums, and publishes an official GitHub Release with release notes and distribution tarballs.


