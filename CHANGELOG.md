# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
