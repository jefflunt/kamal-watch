# Architecture & System Design

## Overview
`kamal-watch` (`github.com/jefflunt/kamal-watch`) is an agentless, lightweight multi-server telemetry dashboard and log observer designed specifically for observing applications deployed with [Kamal](https://kamal-deploy.org/). It operates strictly inside private networks (`127.0.0.1`, LAN, VPC, or VPN) and polls target hosts directly over SSH without installing host-side agents.

## Core Components

1. **Remote SSH Pool (`pkg/remote/ssh.go`)**:
   - Manages persistent SSH client connections using private key authentication.
   - Provides safe thread-safe execution of diagnostic commands (`uptime`, `docker stats`, `docker ps`, `kamal-proxy`) and real-time streaming command execution (`docker logs --follow`).

2. **Telemetry Monitor (`pkg/monitor/monitor.go`)**:
   - Periodically polls configured hosts at a defined cadence (default 4s).
   - Ingests:
     - Host resource health: Load averages, CPU cores, RAM / Swap utilization, root disk space.
     - Docker container status: Active vs exited containers, image tags, role labels (`web`, `job`, accessories).
     - Kamal proxy integration: Routes, virtual hosts, TLS certificate status.
     - Kamal audit logs: Deployment history from `$HOME/.kamal/<app>-audit.log`.
   - Computes aggregated CPU% and MEM% across logical service groups.

3. **Web Server & UI (`pkg/web/server.go` & `pkg/web/templates/`)**:
   - Monospaced, high-density terminal interface inspired by `htop`.
   - Live telemetry updates without full page reloads via `/partial/servers` DOM swapping.
   - Preserves user interaction state (collapsed/expanded services) across periodic refreshes.
   - Slide-over terminal modal with SSE log streaming, log filtering, and auto-scroll.
   - Concept 2 ("The Phosphor Viewfinder `[ • ]`") branding icon and vector SVG favicon.
