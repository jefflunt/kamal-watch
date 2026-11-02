# Kamal Watch

<a href="https://media.jefflunt.com/misc/kamal-watch/kamal-watch-demo.mp4">
  <img src="https://media.jefflunt.com/misc/kamal-watch/kamal-watch-demo.gif" alt="Kamal Watch live demo — click to open full-resolution MP4" width="100%">
</a>

[Kamal](https://kamal-deploy.org/) strips the bloat out of deploying containers to bare metal and VPS hosts. Day-two visibility, however, still means juggling SSH tabs to run `docker ps`, `docker stats`, and `docker logs -f` box by box—or maintaining a Prometheus and Grafana stack heavier than the apps it watches. **Kamal Watch** fixes that with a single Go binary and zero host agents. Give it SSH key access to your Kamal servers, and it discovers every service (`web`, `job` workers, and accessories like Postgres or Redis), maps live `kamal-proxy` routes and TLS status, reads deployment audit logs, and streams container logs in real time.

Inspired by `htop`, the interface favors dense, monospaced terminal telemetry over sprawling charts. Each server card displays live CPU, core count, memory, swap, and root disk gauges above collapsible service rows that update automatically every few seconds. Nothing extra gets installed on your application servers: if Kamal can SSH into a host, Kamal Watch can observe it immediately.

> [!WARNING]
> **Early Alpha — Private Networks Only (`localhost`, Private LAN/VPC, or VPN)**
>
> **Do not expose Kamal Watch to the public internet.** This early alpha release is built strictly for trusted private networks:
> - **No HTTP authentication:** Anyone who can reach the web port can view server topology, deployment history, `/api/inventory` JSON, and live Docker logs for every container on your monitored hosts.
> - **Direct SSH access:** Kamal Watch connects to your servers with an SSH private key and verifies server host keys against `~/.ssh/known_hosts` (or a configured `known_hosts_path`) by default.
> - **Required posture:** Run it bound to `127.0.0.1` on your workstation, or deploy it inside an isolated LAN, private VPC, or mesh VPN (such as Tailscale or WireGuard) unreachable from the open internet.

---

## Quickstart

### Prerequisites

1. **Go 1.26+** (to build from source) or **Docker**.
2. **SSH access to your Kamal hosts** as a user that can run `docker` commands without `sudo` (`root` or a user in the `docker` group—the same user Kamal uses).
3. **An unencrypted (passphraseless) SSH private key** authorized on each monitored host so `kamal-watch` can connect non-interactively. Generate a dedicated key if needed:
   ```bash
   ssh-keygen -t ed25519 -f ~/.ssh/kamal_watch -N "" -C "kamal-watch"
   ssh-copy-id -i ~/.ssh/kamal_watch.pub deploy@10.0.1.10
   ```

### 1. Build

Clone the repository and build the static binary:

```bash
git clone https://github.com/jefflunt/kamal-watch.git
cd kamal-watch
./cmd/build
```

This writes the compiled binary to `./bin/kamal-watch`. You can also install directly with Go:

```bash
go install github.com/jefflunt/kamal-watch/cmd/kamal-watch@latest
```

### 2. Configure

Copy the example config and list the servers you want to monitor:

```bash
cp kamal-watch.example.yml kamal-watch.yml
```

Edit `kamal-watch.yml`:

```yaml
# Use "127.0.0.1:8080" locally; use ":8080" inside a container on a private network
listen_addr: "127.0.0.1:8080"
poll_interval: 4s

servers:
  - name: app-server-01
    host: 10.0.1.10              # Private LAN, VPC, or Tailscale IP/hostname
    port: 22
    user: deploy                 # Must have Docker access and ~/.kamal/*-audit.log
    key_path: ~/.ssh/kamal_watch # Unencrypted private key path
```

### 3. Run Locally

```bash
./bin/kamal-watch -config kamal-watch.yml
```

Open `http://127.0.0.1:8080` in your browser. Override the listen address anytime with `-addr` (for example, `./bin/kamal-watch -addr 127.0.0.1:9090`).

---

## Deploying to Your Own Cluster (Docker or Kamal)

Inside the provided [`Dockerfile`](./Dockerfile), `kamal-watch` listens on port `8080` and reads `/app/config/kamal-watch.yml` by default. Mount your configuration directory and SSH key from the host as read-only volumes.

### Prepare Host Files on Your Target Server

On the private/VPN server that will run `kamal-watch`:

```bash
sudo mkdir -p /etc/kamal-watch
sudo cp ~/.ssh/kamal_watch /etc/kamal-watch/id_ed25519
sudo chmod 600 /etc/kamal-watch/id_ed25519
```

Create `/etc/kamal-watch/kamal-watch.yml` on that host (note `listen_addr: ":8080"` and the in-container `key_path`):

```yaml
listen_addr: ":8080"
poll_interval: 4s

servers:
  - name: app-server-01
    host: 10.0.1.10
    port: 22
    user: deploy
    key_path: /app/ssh/id_ed25519
```

### Option A: Run with Docker

Bind port `8080` strictly to localhost or a private LAN/Tailscale interface IP (for example, `100.64.0.10`):

```bash
docker build -t kamal-watch .

docker run -d --name kamal-watch --restart unless-stopped \
  -p 100.64.0.10:8080:8080 \
  -v /etc/kamal-watch:/app/config:ro \
  -v /etc/kamal-watch/id_ed25519:/app/ssh/id_ed25519:ro \
  kamal-watch
```

### Option B: Deploy with Kamal

To deploy `kamal-watch` alongside your apps using Kamal on a private network or VPN, create `config/deploy.yml` (gitignored by default):

```yaml
service: kamal-watch
image: your-registry/kamal-watch

servers:
  web:
    - 100.64.0.10 # Private VPC, LAN, or Tailscale IP ONLY

proxy:
  host: watch.internal # Private DNS / split-horizon host — do NOT expose publicly
  app_port: 8080
  healthcheck:
    path: /up
    interval: 5
    timeout: 10

registry:
  server: ghcr.io
  username: your-github-user
  password:
    - KAMAL_REGISTRY_PASSWORD

builder:
  arch: amd64

volumes:
  - "/etc/kamal-watch:/app/config:ro"
  - "/etc/kamal-watch/id_ed25519:/app/ssh/id_ed25519:ro"
```

Then deploy:

```bash
kamal setup
```

---

## How It Works

Every `poll_interval`, `kamal-watch` runs a single batched, read-only SSH command per server to collect:
- **Host gauges:** `uptime`, `nproc`, `top -bn1`, `df -h /`, and `free -h`
- **Container state & telemetry:** `docker ps -a --format '{{json .}}'` and `docker stats --no-stream --format '{{json .}}'`
- **Proxy routes:** `docker exec kamal-proxy kamal-proxy list`
- **Audit logs:** `head -n 30 "$HOME"/.kamal/*-audit.log`
- **Live log streams (on demand):** `docker logs --tail 150 -f <container>` streamed to the browser over Server-Sent Events (SSE)

### HTTP Endpoints

| Method & Path | Purpose |
| :--- | :--- |
| `GET /` | Main `htop`-style multi-server dashboard and interactive SSE log drawer |
| `GET /up` | Lightweight unauthenticated healthcheck endpoint for `kamal-proxy` / Docker |
| `GET /server/{name}` | Full Docker container table for a single server |
| `GET /server/{name}/app/{app}` | Service role breakdown, aggregate CPU/RAM, and Kamal audit log history |
| `GET /api/inventory` | JSON snapshot of all servers and apps (also used for Kamal healthchecks) |
| `GET /events` | SSE stream pushing live cluster inventory updates |
| `GET /logs/stream` | SSE Docker log stream (`?server=<name>&container=<id-or-name>&tail=150&follow=true`) |

## License

Released under the [MIT License](./LICENSE).
