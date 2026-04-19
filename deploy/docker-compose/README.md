# Deploying Librarium with Docker Compose

A complete self-hosted Librarium install in one compose file. Good fit for a small homelab, a single VPS, or anywhere a Kubernetes cluster would be overkill.

For Kubernetes instead, see [`../kubernetes/`](../kubernetes/).

## What gets deployed

| Service | Image | Purpose |
|---|---|---|
| `db` | `postgres:16-alpine` | Database for everything — books, users, sessions, background jobs |
| `librarium-api` | `ghcr.io/fireball1725/librarium-api` | Go backend — handles all business logic and serves the REST API |
| `web` | `ghcr.io/fireball1725/librarium-web` | nginx serving the React UI, with `/api` transparently proxied to the backend |

The api and db are **not** published to the host — only the web service is. The UI and API are both reachable through a single port (`WEB_PORT`, default `3000`) because nginx proxies `/api/*` to the backend internally.

## Prerequisites

- **Docker** 24+ and the **Compose plugin** (`docker compose version`). If you only have the old `docker-compose` binary, upgrade — these instructions assume the v2 plugin.
- Roughly **500 MB** disk for the images and **whatever you plan to store** for the database and cover art. Both use Docker named volumes (see [Data persistence](#data-persistence)).
- Outbound network access to `ghcr.io` to pull the images.

## Quickstart

```bash
cd deploy/docker-compose

# 1. Create your local secrets file
cp .env.example .env

# 2. Generate and paste values for the two required secrets
openssl rand -base64 48       # copy → .env as JWT_SECRET
openssl rand -base64 24       # copy → .env as POSTGRES_PASSWORD

# 3. Bring it up
docker compose up -d

# 4. Watch it start (Ctrl-C to stop watching — it keeps running)
docker compose logs -f
```

Then open **http://localhost:3000** (or whatever `WEB_PORT` you set). On first load you'll be walked through creating the initial admin user.

## Configuration

Everything is configured through `.env`. The relevant variables:

| Variable | Required? | Default | Notes |
|---|---|---|---|
| `JWT_SECRET` | **yes** | — | Long random string. Rotating invalidates all active sessions. |
| `POSTGRES_PASSWORD` | **yes** | — | Postgres superuser password. Set it once — changing it later requires manually updating the running DB. |
| `WEB_PORT` | no | `3000` | Host port the UI is exposed on. Change if `3000` is taken. |
| `API_TAG` | no | `latest` | Image tag for the API. **Pin to a specific release in production.** |
| `WEB_TAG` | no | `latest` | Image tag for the web UI. Same advice. |
| `POSTGRES_DB` | no | `librarium` | DB name. Only change on a fresh deploy. |
| `POSTGRES_USER` | no | `librarium` | DB user. Only change on a fresh deploy. |
| `LOG_LEVEL` | no | `info` | API log verbosity: `debug` / `info` / `warn` / `error`. |
| `MEDIA_PATH` | no | *(internal volume)* | Absolute host path to bind-mount at `/data/media`. Use this to point at an existing NAS/library share instead of the Docker-managed `media_data` volume. |

## Putting it behind a reverse proxy

For real-world use you probably want HTTPS and a real hostname. The compose file exposes the web service on a host port, so any reverse proxy that can forward to `http://<host>:3000` works — Traefik, Caddy, nginx, Cloudflare Tunnel, etc.

With Traefik using Docker labels, the simplest pattern is to drop the `ports:` stanza on the `web` service and attach Traefik labels + an external Traefik network instead. Keep the other two services on their default internal network so they aren't exposed.

The API itself doesn't need to be reverse-proxied separately — it's only reachable via the web service's `/api` path.

## Upgrading

Pin `API_TAG` and `WEB_TAG` in `.env` to the version you're running. To upgrade:

```bash
# Edit .env and bump API_TAG / WEB_TAG to the new release
docker compose pull
docker compose up -d
```

Compose will replace the api and web containers and leave the db untouched. Database migrations run automatically on API startup.

If something breaks after an upgrade, roll back by pinning the previous tag and re-running `docker compose up -d`. (This only works cleanly within the same schema version — check the CHANGELOG for migration-related notes before upgrading across larger jumps.)

## Data persistence

Three Docker volumes hold your data:

| Volume | Mount | Holds |
|---|---|---|
| `db_data` | `/var/lib/postgresql/data` (in db) | Postgres data directory |
| `cover_data` | `/data/covers` (in api) | Book cover images — small, server-managed |
| `media_data` | `/data/media` (in api) | Uploaded ebooks and audiobooks — potentially large |

All three survive `docker compose down` and `docker compose up` cycles. **`docker compose down -v` deletes them** — don't run that unless you mean it.

### Using an existing NAS / file share for media

Most self-hosters already have a library on a NAS and don't want to duplicate it into a Docker volume. Set `MEDIA_PATH` in `.env` to an absolute host path and the compose file will bind-mount it at `/data/media` instead of using the named volume:

```env
MEDIA_PATH=/mnt/library
```

Any path Docker can reach works — a ZFS dataset, an NFS mount on the host, a bind into another container's volume, etc. Make sure the path contains (or can be populated with) `ebooks/` and `audiobooks/` subdirectories, since those are the defaults the api writes to.

Covers don't get a `MEDIA_PATH`-style override — they're small, frequently written, and tightly coupled to the database, so keep them on the internal volume alongside the db for consistent backups.

### Backups

Nothing fancy, but at minimum:

```bash
# Dump the database
docker compose exec db pg_dump -U librarium librarium > librarium-$(date +%F).sql

# Snapshot the covers volume
docker run --rm -v librarium_cover_data:/data -v "$PWD":/backup alpine \
  tar czf /backup/covers-$(date +%F).tar.gz -C /data .

# Media — if you're using a host path (MEDIA_PATH), back that up with your
# existing tooling. If you're using the internal volume, same trick:
docker run --rm -v librarium_media_data:/data -v "$PWD":/backup alpine \
  tar czf /backup/media-$(date +%F).tar.gz -C /data .
```

Restore is the reverse — `psql` the SQL dump into a fresh DB, then untar the covers/media back into the matching volumes.

## Common tasks

```bash
# Follow logs for everything
docker compose logs -f

# Just the API
docker compose logs -f librarium-api

# Restart only the API (e.g. after changing LOG_LEVEL in .env)
docker compose up -d librarium-api

# Connect to the database
docker compose exec db psql -U librarium librarium

# Stop everything (keeps data)
docker compose down

# Stop and DELETE all data (irreversible)
docker compose down -v
```

## Troubleshooting

**Web UI loads but every API call 502s.** The api container is probably unhealthy — `docker compose logs librarium-api` will usually show a Postgres connection error or a migration failure. Confirm `POSTGRES_PASSWORD` in `.env` matches what the db container was first started with (changing it after the fact doesn't update the db).

**"port is already allocated" on `up`.** Something else is on port 3000. Set `WEB_PORT=8080` (or similar) in `.env` and re-run `docker compose up -d`.

**Images won't pull (`denied` or `unauthorized`).** The GHCR images are public, so this is almost always a stale Docker login. `docker logout ghcr.io` and try again.

**Migration fails on startup, API crash-loops.** Open an issue with the full `librarium-api` log output. Migrations are applied in order and never edited after release, so a failure usually means a partially-applied migration from a previous crash. Recovery instructions will depend on where it stopped.
