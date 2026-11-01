# Kamal Watch

A lightweight, multi-server dashboard and log observer for [Kamal](https://kamal-deploy.org/) deployments.

## Features

- **Multi-Server Monitoring**: Connects over SSH with public keys to any number of servers running Kamal.
- **Service & Role Inventory**: Automatically detects and groups services into `web`, `job`/worker roles, and accessories (Postgres, Redis, etc.).
- **Kamal Proxy Integration**: Surfaces virtual host routing, target backend containers, and TLS status directly from `kamal-proxy`.
- **Deployment Timestamps & Audit Log**: Extracts recent deployments, commit version hashes, deployer identity, and timestamps from Kamal's audit logs (`~/.kamal/*-audit.log`).
- **Live Real-time Log Streaming**: Stream Docker logs with instant auto-scroll, log search/filter, and colorized status codes (SSE-backed, zero page refreshes).
- **Responsive Mobile & Desktop UI**: Built with Tailwind CSS, clean dark mode aesthetic, and embedded HTML templates (single static binary, zero external asset dependencies).

## Configuration

Copy `kamal-watch.example.yml` to `kamal-watch.yml` and configure your servers:

```yaml
listen_addr: ":8080"
poll_interval: 4s
servers:
  - name: app-server-01
    host: 203.0.113.10
    port: 22
    user: deploy
    key_path: ~/.ssh/id_ed25519
```

> **Security Note:** `kamal-watch` does not include built-in HTTP authentication and connects directly to your infrastructure over SSH. Run it locally (`127.0.0.1`), inside a private LAN/VPC/VPN (e.g., Tailscale/WireGuard), or behind an authenticating reverse proxy—never expose it directly to the public internet.

## Building & Running

Build the binary:
```bash
./cmd/build
```

Start the web application:
```bash
./bin/kamal-watch
```
Or specify a custom address:
```bash
./bin/kamal-watch -addr :8080
```
Open your browser at `http://localhost:8080`.
