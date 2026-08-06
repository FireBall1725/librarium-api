# Deploy Librarium on Railway

Railway is a PaaS that handles Postgres provisioning, HTTPS, and custom domains for you — nice if you want Librarium running at `library.example.com` in under 10 minutes without managing a VPS. Floor is ~$5/month on the Hobby plan.

> ⚠️ Not a pre-built "Deploy on Railway" template yet. Follow the manual steps below; the button will land once the template is published to the Railway marketplace.

## What you'll end up with

Three Railway services in one project:

- **api** — this repo (`librarium-api`), running the Go binary
- **web** — [`librarium-web`](https://github.com/FireBall1725/librarium-web), serving the React SPA through nginx and proxying `/api` back to the api service
- **Postgres** — Railway's managed Postgres add-on

There's no Meilisearch — the api falls back to Postgres full-text search when `MEILI_URL` is unset.

## Prerequisites

- A Railway account with a Hobby plan or higher (Trial works to try, but spins down and doesn't support custom domains).
- A (custom) domain you control, if you want prettier URLs than `*.up.railway.app`.

## Steps

### 1. Create the project and Postgres

1. From Railway's dashboard: **New Project → Empty Project**.
2. Inside the new project: **+ New → Database → Add PostgreSQL**. Wait for it to provision.

### 2. Deploy the api service

1. **+ New → GitHub Repo → FireBall1725/librarium-api**.
2. Railway auto-detects `railway.toml` in the repo root. The build uses the repo `Dockerfile`; healthcheck is `/health`.
3. Open the service's **Variables** tab and set:
   - `DATABASE_URL` = `${{Postgres.DATABASE_URL}}?sslmode=require` (click "Add Reference" and pick the Postgres plugin, then append `?sslmode=require`)
   - `JWT_SECRET` = a long random string. Generate with `openssl rand -hex 32`.
   - `REGISTRATION_ENABLED` = `false` if you want to lock down signups after creating the first admin (optional — default is `true`).
4. Under **Settings → Networking**, click **Generate Domain**. Railway gives you a `*.up.railway.app` URL. Custom domains can be added here later.

### 3. Deploy the web service

1. **+ New → GitHub Repo → FireBall1725/librarium-web**.
2. Again, Railway picks up `railway.toml` and uses the repo Dockerfile (multi-stage: Vite build → nginx).
3. Set Variables:
   - `API_UPSTREAM` = `${{api.RAILWAY_PRIVATE_DOMAIN}}` (reference the api service you just created)
   - `API_UPSTREAM_PORT` = `8080`
4. **Settings → Networking → Generate Domain** — this one is the URL you'll actually visit.

### 4. First-run setup

Open the web service's URL. You'll be redirected to `/setup`. Create the first admin account. Done.

## Custom domain

In either service's **Settings → Networking**, add your domain and copy the CNAME target Railway gives you into your DNS. Propagation usually takes <5 minutes. Railway provisions a Let's Encrypt cert automatically.

## Upgrading

Railway watches the tracked branch (default: `main`) and redeploys on push. If you want to pin a release tag instead, change the service's deployment branch to the tag in **Settings → Source**.

## Cost expectation

Hobby plan is $5/month *credit*; actual usage:

- api service: idle ~0.05 vCPU / 100 MB — a few dollars a month at idle
- web service: negligible — nginx serving a static bundle
- Postgres plugin: ~$5/month for the 1 GB tier, which is plenty for a personal library

Total: $5–10/month on the Hobby plan. Scales up as your library grows.

## Rollback

In Railway's service dashboard, each deploy has a **Redeploy** action. Pick any previous successful deploy to roll back.

## Alternatives

If Railway isn't for you, see [`deploy/docker-compose/`](../docker-compose/) for the reference self-hosted stack, or the Helm chart in [`deploy/helm/`](../helm/).
