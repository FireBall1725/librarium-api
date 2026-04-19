# librarium-api

The backend service for [Librarium](https://github.com/fireball1725) — a self-hosted personal library tracker for books, manga, and other print media.

Go 1.25 · PostgreSQL 16 · [River](https://riverqueue.com) for background jobs · Swagger-generated OpenAPI docs.

## Quickstart (development)

```bash
docker compose up -d --build
```

The API listens on `:8080`. A healthy response:

```bash
curl http://localhost:8080/health
```

API documentation is served at `http://localhost:8080/swagger/index.html` once the container is up.

> For **self-hosting the full stack** (api + web + db) see the [Deployment](#deployment) section below.

## Environment

Set in `docker-compose.yml` for local dev; in production pass via environment or a secrets manager.

| Variable | Purpose | Example |
|---|---|---|
| `DATABASE_URL` | Postgres DSN | `postgres://librarium:librarium@db:5432/librarium?sslmode=disable` |
| `JWT_SECRET` | HMAC secret for auth tokens | *any long random string* |
| `LOG_LEVEL` | `debug` / `info` / `warn` / `error` | `info` |
| `COVER_STORAGE_PATH` | Where cover images are written | `/data/covers` |

A `.env.example` file documents the full set.

## Local development

```bash
# rebuild and restart after any Go/migration/swagger change
docker compose up -d --build api

# follow logs
docker compose logs -f api

# regenerate swagger (after editing handler annotations)
make swagger
```

Migrations live in `internal/db/migrations/` and are applied automatically on startup.

## Versioning

Format: **`YY.MM.revision`** (e.g. `26.4.0`).

- `YY` — two-digit release year.
- `MM` — release month, *not* zero-padded (`26.4`, not `26.04`).
- `revision` — feature counter within the month, starting at `0`. Resets to `0` when the month rolls over.
- `-dev` suffix — local, unshipped builds. Never used for released artifacts.

Release history in [CHANGELOG.md](./CHANGELOG.md).

## Deployment

Two supported paths for self-hosting. Both use the multi-arch images published to GHCR — no local build required.

### Docker Compose (whole stack)

[`deploy/docker-compose/`](./deploy/docker-compose/) contains a compose file that runs db + api + web together using images from GHCR:

```bash
cd deploy/docker-compose
cp .env.example .env
# edit .env — set JWT_SECRET and POSTGRES_PASSWORD
docker compose up -d
```

Open http://localhost:3000. The API is not published externally — it's reached through the web service's nginx proxy at `/api`.

### Kubernetes

Two options live under [`deploy/`](./deploy/):

- **[`deploy/helm/librarium-api/`](./deploy/helm/librarium-api/)** — Helm chart built on [firelabs-helm-common](https://github.com/FireBall1725/firelabs-helm-common) with a [CloudNativePG](https://cloudnative-pg.io/) `Cluster` for Postgres. Use this if you already run CNPG, or want templated values across environments.
- **[`deploy/kubernetes/`](./deploy/kubernetes/)** — plain manifests (namespace, StatefulSet postgres, ConfigMap, Secret, Deployment, Service, Ingress). Use these if you want to read every knob before applying, or you don't run CNPG.

For the web UI, apply the matching manifests from [`librarium-web/deploy/kubernetes/`](https://github.com/fireball1725/librarium-web/tree/main/deploy/kubernetes) into the same namespace.

**In production, pin the image tag** (e.g. `ghcr.io/fireball1725/librarium-api:26.4.0`) rather than using `:latest` so upgrades are explicit and rollbacks are possible.

## Releasing

Releases are cut from `main` via the `release` GitHub Actions workflow (`workflow_dispatch`). It:

1. Computes the next `YY.MM.revision` from the latest tag.
2. Updates `internal/version/version.go`, commits `release: <version>`, tags `v<version>`.
3. Builds a multi-arch Docker image and pushes it to `ghcr.io/fireball1725/librarium-api:<version>` and `:latest`.
4. Bumps `version.go` to the next `-dev` revision, commits, pushes.
5. Creates a GitHub Release with auto-generated notes.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md). PRs must sign off on the [Developer Certificate of Origin](./DCO) (`git commit -s`) — a CI check enforces this.

## License

AGPL-3.0-only. See [LICENSE](./LICENSE) for the full text.
