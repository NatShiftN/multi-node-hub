<div align="center">

# NodeHub

### One dashboard. Many machines. Zero babysitting.

A lightweight, self-hosted control plane for Xray nodes, Traefik routes and unattended infrastructure operations.

[![Release](https://img.shields.io/github/v/release/NatShiftN/multi-node-hub?style=flat-square)](https://github.com/NatShiftN/multi-node-hub/releases)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![Vue](https://img.shields.io/badge/Vue-3-42b883?style=flat-square&logo=vue.js)](https://vuejs.org)
[![Docker](https://img.shields.io/badge/Docker-ready-2496ED?style=flat-square&logo=docker)](Dockerfile)

[**Live Demo**](https://demo.hkvps.xyz/) · [Quick start](#-quick-start) · [中文](README.zh-TW.md) · [简体中文](README.zh-CN.md)

<img src="docs/screenshots/dashboard.png" alt="NodeHub dashboard" width="92%" />

</div>

---

## What is NodeHub?

NodeHub is a **single control plane** for people who run more than one server.

Instead of SSHing into every machine to install Xray, edit Nginx/Traefik, check CPU, rotate IPs or fix a blocked node, you open one page:

- see every node online / offline
- create Xray inbounds and copy subscription links
- watch CPU, RAM, traffic and access logs
- push certificates and camouflage sites
- replace a blocked IP safely
- manage Traefik routes from a visual map

It is built for **homelabs, small hosting providers and private proxy fleets** — not for giant SaaS platforms.

> **No Kubernetes. No plugin marketplace. No 40-minute onboarding.**
> One binary, one SQLite file, one dashboard.

---

## Who is it for?

| You are… | NodeHub helps you… |
|---|---|
| Running 2–50 VPS nodes | Stop logging into each one |
| Selling Xray/VPN service | Give users subscription links and quotas |
| Using Traefik | See routes and access logs in one place |
| Tired of blocked IPs | Use guarded Auto-Heal instead of blind scripts |
| Not a Linux expert | Deploy an agent with one copied command |

---

## See it first

### Live demo

```text
https://demo.hkvps.xyz/
```

The demo is **read-only**, with three virtual nodes, simulated traffic and sample Traefik routes/logs. Login is prefilled on the page.

---

## 🚀 Quick start

### Option A — One command with Docker (recommended)

Requirements: Linux, Docker, Docker Compose v2.

```sh
git clone https://github.com/NatShiftN/multi-node-hub.git && cd multi-node-hub && ./install.sh
```

The installer:

1. creates `.env` with random `AGENT_TOKEN`, `JWT_SECRET` and admin password
2. builds the image
3. starts NodeHub on port `8080`
4. prints the URL and admin password

Open:

```text
http://YOUR-SERVER:8080
```

Then:

```text
Nodes → Add Node → copy the generated install command
```

Paste that command on any clean Debian/Ubuntu VPS. The agent connects back automatically — no manual config file.

### Option B — Docker Compose manually

```sh
cp .env.example .env
# edit .env: AGENT_TOKEN, ADMIN_PASSWORD, JWT_SECRET, NODEHUB_PUBLIC_URL
docker compose up -d --build
```

### Option C — Build from source

```sh
go build -trimpath -buildvcs=false -ldflags "-s -w" -o bin/nodehub-server ./server
go build -trimpath -buildvcs=false -ldflags "-s -w" -o bin/nodehub-agent ./agent
cd web && pnpm install && pnpm build && cd ..
./bin/nodehub-server --addr 127.0.0.1:8080 --token change-me \
  --public-url http://127.0.0.1:8080 --db nodehub.db \
  --web-dir web/dist --admin-password 'change-this-password'
```

---

## Screenshots

### Simple mode — everything a beginner needs

<img src="docs/screenshots/login.png" alt="NodeHub login" width="48%" />
<img src="docs/screenshots/dashboard.png" alt="NodeHub dashboard" width="48%" />

### Traefik Manager — route map and access logs

<img src="docs/screenshots/traefik.png" alt="Traefik route map" width="92%" />

<img src="docs/screenshots/traefik-logs.png" alt="Traefik access log analytics" width="92%" />

---

## ✨ What it does

### Nodes without the busywork

- One dashboard for all machines
- Online / offline / network error / GFW blocked states
- CPU, RAM, network and Xray traffic
- Tags, search, pagination and mobile cards
- Simple mode for daily use, Pro mode for operations

### Xray management

- VLESS / Trojan / Shadowsocks inbounds
- TCP / WebSocket / gRPC
- TLS, REALITY, UUID and password generation
- Share links, QR codes and subscriptions
- Per-user traffic, quotas and bans
- Xray process status and restart

### Unattended operations

- **Guarded Auto-Heal** — only when overseas TCP succeeds and independent domestic probes agree
- **24h circuit breaker** — max 2 IP/rebuild attempts per node, persisted in SQLite
- **Vultr create-first replacement** — the old instance is removed only after the new IP is ready
- **Orphan retry worker** — failed old-instance deletes are retried and escalated
- **ADSL / PPPoE redial** recovery
- **iptables port forwarding** with local state restore
- **One-click DD** for clean OS reinstall

### Traefik Manager

- Topology: `EntryPoint → Router → Middleware → Service → Backend`
- Backend health, TLS and middleware badges
- Access-log analytics: 4xx/5xx, error rate, duration, top paths/routers, per-minute chart
- Live filtering, pause and download
- Configurable Traefik API + basic auth + local access log path

> This is an independent implementation inspired by the well-known Traefik Manager project, not a copy of its GPL source.

### Security

- TOTP two-factor authentication
- RBAC: SuperAdmin / Operator / Viewer
- API keys, audit log
- AES-GCM encrypted cloud keys, certificate private keys, Telegram/webhook secrets
- Release binaries built with `-s -w -buildvcs=false`
- Demo write APIs return 403

### Bilingual and mobile

- 中文 / English toggle
- `?lang=en` and `?lang=zh` links
- Mobile node cards

---

## How it works

```text
┌──────────────────────────────┐
│  Browser (Vue 3)             │
└──────────────┬───────────────┘
               │ HTTPS / JSON / SSE
┌──────────────▼───────────────┐
│  NodeHub Server (Go)         │
│  SQLite · RBAC · Traefik API │
└──────────────┬───────────────┘
               │ WebSocket (control plane)
     ┌─────────┼─────────┐
     ▼         ▼         ▼
  Agent A    Agent B    Agent C
  Xray       Xray       Xray
  iptables   iptables   iptables
```

The data plane stays local. If the control plane goes offline, nodes keep running; the agent reconnects with exponential backoff and restores local Xray/iptables state.

---

## Requirements

| Component | Requirement |
|---|---|
| Server | Linux / Windows / Docker, 1 vCPU, 512 MB RAM |
| Database | SQLite (bundled) |
| Agent | Linux amd64 / arm64 |
| Browser | Modern Chrome / Edge / Safari / Firefox |
| Optional | Traefik v2/v3 API and access log |

---

## Common tasks

### Add your first node

1. Open `Nodes → Add Node`
2. Enter host and root password, or paste an SSH private key
3. Optional custom SSH port
4. Copy the generated one-line installer and run it on the VPS

### Create a subscription

1. Open a node → Subscriptions
2. Add a VLESS / Trojan / Shadowsocks inbound
3. Copy the share link or QR code
4. Send it to the client

### Connect Traefik Manager

1. Enable the Traefik API
2. Open `Traefik → Settings`
3. Fill API URL, basic auth and access log path
4. Save → Route Map and Logs load automatically

### Reset the database

```sh
docker compose down
docker volume rm multi-node-hub_nodehub-data
docker compose up -d
```

---

## Security checklist

- [ ] Change `ADMIN_PASSWORD`
- [ ] Use a random `JWT_SECRET` (32+ chars) and back it up
- [ ] Change `AGENT_TOKEN`
- [ ] Put NodeHub behind HTTPS
- [ ] Restrict port 8080 with a firewall
- [ ] Never commit `.env`, `nodehub.db`, `backups/` or certificates
- [ ] Rotate any credential ever pasted into chat or logs

See [SECURITY.md](SECURITY.md).

---

## Development

```sh
make test          # go test
make vet           # go vet
make build         # server + agent
cd web && pnpm dev # frontend on :5173
```

Project layout:

```text
agent/          edge agent entrypoint
internal/       control plane, store, plugins, cloud, deploy
server/         server entrypoint
web/            Vue 3 frontend
docs/           screenshots and docs
```

---

## FAQ

**Does it work without Docker?**  
Yes. Build the Go binary and point `--web-dir` at `web/dist`.

**Does NodeHub proxy traffic?**  
No. Xray runs on each node; NodeHub only controls it.

**What happens if the server dies?**  
Nodes keep running. Agents reconnect when the server returns.

**Can I use it commercially?**  
GPL-3.0 allows commercial use, but derivative distributions must comply with GPL.

**Is Traefik required?**  
No. Traefik Manager is optional.

---

## License

GPL-3.0. See [LICENSE](LICENSE).

<div align="center">

**If NodeHub saves you time, star the repo ⭐**

[Live Demo](https://demo.hkvps.xyz/) · [Releases](https://github.com/NatShiftN/multi-node-hub/releases) · [Issues](https://github.com/NatShiftN/multi-node-hub/issues)

</div>
