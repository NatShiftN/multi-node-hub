# NodeHub

Source-only distribution for the NodeHub multi-node control plane.

## Build

```sh
go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=1.0.0-beta" -o bin/nodehub-server ./server
go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=1.0.0-beta" -o bin/nodehub-agent ./agent
cd web && pnpm install && pnpm build
```

## Run

```sh
./bin/nodehub-server --addr 127.0.0.1:8080 --token change-me \
  --public-url http://127.0.0.1:8080 --db nodehub.db \
  --web-dir web/dist --admin-password 'change-this-password'
```

## Traefik Manager

Pro mode `/traefik` provides:

- topology route map: EntryPoint → Router → Middleware → Service → Backend
- backend health, TLS and middleware badges
- access-log analytics: status rate, error rate, top paths/routers, per-minute timeline
- filtering, pause, download and line-count controls
- encrypted Traefik API/basic-auth settings and local access-log path

Traefik API does not expose access logs; point the manager at a server-local access log file. The route map/log feature is an independent implementation inspired by Traefik Manager, not a copy of its GPL source.

## Safety

Persistent 24h high-risk ledger, create-first Vultr replacement, orphan retry worker, GFW cross-validation, local Agent state restore, encrypted Telegram/webhook settings, bilingual UI.

See `SECURITY.md`.

## License

GPL-3.0. See `LICENSE`.
