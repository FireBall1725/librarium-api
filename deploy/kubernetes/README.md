# Deploying librarium-api to Kubernetes

Minimal, unopinionated manifests to get `librarium-api` running in a Kubernetes cluster. They're a starting point — expect to adapt them to your storage class, ingress controller, and secret management.

The manifests here deploy **api + postgres**. For the web UI, see the matching manifests in [`librarium-web`](https://github.com/fireball1725/librarium-web/tree/main/deploy/kubernetes).

> **Prefer Helm?** There's also a chart at [`../helm/librarium-api/`](../helm/librarium-api/) built on [firelabs-helm-common](https://github.com/FireBall1725/firelabs-helm-common) with [CloudNativePG](https://cloudnative-pg.io/) for Postgres. Use it if you want templated values or already run CNPG. The plain manifests below are easier to read top-to-bottom and don't require any operators.

## What's in the box

| File | Purpose |
|---|---|
| `00-namespace.yaml` | Creates the `librarium` namespace |
| `10-postgres.yaml` | Postgres 16 as a StatefulSet with a 10Gi PVC + ClusterIP service |
| `20-configmap.yaml` | Non-sensitive config — log level, storage paths, JWT TTLs, etc. |
| `30-secret.example.yaml` | Template for `JWT_SECRET`, `DATABASE_URL`, `POSTGRES_PASSWORD` — **do not commit the filled-in version** |
| `40-api.yaml` | API Deployment, Service (ClusterIP :8080), and PVCs for cover and media storage |
| `50-ingress.example.yaml` | Example ingress — edit the host, TLS secret, and class for your cluster |

## Prerequisites

- A Kubernetes cluster with a default `StorageClass` that supports `ReadWriteOnce` (for Postgres and the PVCs below), or access to an NFS/SMB share you can bind in as a `PersistentVolume`.
- An ingress controller (nginx, Traefik, etc.) if you want to expose the API outside the cluster.
- `kubectl` pointed at the target cluster.

## Quickstart

```bash
# 1. Namespace
kubectl apply -f 00-namespace.yaml

# 2. ConfigMap (edit paths/TTLs first if you want, defaults are fine)
kubectl apply -f 20-configmap.yaml

# 3. Secret — copy, fill in, apply. Never commit the filled-in copy.
#    For production use sealed-secrets, External Secrets, SOPS, or Vault
#    instead of editing a plaintext YAML.
cp 30-secret.example.yaml 30-secret.yaml
# edit 30-secret.yaml — set JWT_SECRET, POSTGRES_PASSWORD, and the password inside DATABASE_URL
kubectl apply -f 30-secret.yaml

# 4. Database, then API
kubectl apply -f 10-postgres.yaml
kubectl apply -f 40-api.yaml

# 5. (Optional) Ingress — edit the host first
cp 50-ingress.example.yaml 50-ingress.yaml
# edit: spec.rules[0].host, tls.hosts, ingressClassName
kubectl apply -f 50-ingress.yaml
```

## Configuration (ConfigMap)

Non-sensitive settings live in `20-configmap.yaml`. You can edit any value and re-apply without touching the Secret or the Deployment spec:

```bash
# edit 20-configmap.yaml
kubectl apply -f 20-configmap.yaml
kubectl rollout restart deploy/librarium-api -n librarium
```

| Key | Purpose | Default |
|---|---|---|
| `LOG_LEVEL` | `debug` / `info` / `warn` / `error` | `info` |
| `COVER_STORAGE_PATH` | Where covers are written inside the container | `/data/covers` |
| `EBOOK_STORAGE_PATH` | Where uploaded ebooks are written | `/data/media/ebooks` |
| `AUDIOBOOK_STORAGE_PATH` | Where uploaded audiobooks are written | `/data/media/audiobooks` |
| `EBOOK_PATH_TEMPLATE` | Filename template for ebooks (`{title}`, `{author}`, …) | `{title}` |
| `AUDIOBOOK_PATH_TEMPLATE` | Filename template for audiobooks | `{title}` |
| `JWT_ACCESS_TTL` | Access token lifetime (Go duration) | `15m` |
| `JWT_REFRESH_TTL` | Refresh token lifetime | `168h` |
| `REGISTRATION_ENABLED` | `"false"` to disable open signups | `"true"` |

If you change a storage path, also update the corresponding `volumeMounts` entry in `40-api.yaml` so the PVC is mounted at the same location.

## Storage

The api uses two PVCs:

| PVC | Mount | Default size | What it holds |
|---|---|---|---|
| `librarium-covers` | `/data/covers` | 5Gi | Book cover images, managed by the api itself |
| `librarium-media` | `/data/media` | 100Gi | Uploaded ebooks and audiobooks — typically much bigger |

Plus Postgres has its own 10Gi PVC from `volumeClaimTemplates` in `10-postgres.yaml`.

### Using an existing NFS / SMB share

Most self-hosters want the `librarium-media` PVC to land on an existing library share instead of dynamically-provisioned block storage. Pre-create a `PersistentVolume` that references the share and bind the PVC to it.

**NFS example** — create before applying `40-api.yaml`:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: librarium-media-nfs
  labels:
    app: librarium
    tier: media
spec:
  capacity:
    storage: 500Gi
  accessModes: ["ReadWriteMany"]   # NFS supports RWX, unlike most block CSI
  persistentVolumeReclaimPolicy: Retain
  storageClassName: ""              # empty — bind this PV directly, don't dynamic-provision
  nfs:
    server: nas.example.internal
    path: /export/librarium/media
```

Then edit `40-api.yaml`:
- On the `librarium-media` PVC, set `storageClassName: ""`, set `accessModes: ["ReadWriteMany"]`, and match the `storage` request.
- Drop the `strategy: Recreate` restriction on the Deployment — with RWX you can run multiple replicas safely.

`librarium-covers` is small and frequently written by the api process itself — keep it on fast local block storage for responsiveness.

### Sizing

- **Covers**: ~50-200 KB per book. 5 GiB handles tens of thousands of books. Scale up if you curate high-res art.
- **Media**: ebooks average 1-5 MB, audiobooks 200 MB - 2 GB. Size to your library; you can always `kubectl edit pvc` later if your `StorageClass` supports expansion.
- **Postgres**: grows slowly — 10 GiB is plenty for most personal libraries.

## External Postgres

`10-postgres.yaml` bundles Postgres for convenience. In production you likely want a managed DB (CloudNativePG, Zalando operator, RDS, etc.). To use an external DB:

1. Skip `10-postgres.yaml`.
2. In `30-secret.yaml`, set `DATABASE_URL` to your external DSN and leave `POSTGRES_PASSWORD` blank (or remove it).
3. No changes needed in `40-api.yaml` — it reads `DATABASE_URL` directly from the Secret via `envFrom`.

## Image tags

`40-api.yaml` uses `ghcr.io/fireball1725/librarium-api:latest`. For real deployments **pin to a specific release** (e.g. `:26.4.0`) so upgrades are explicit and rollbacks are possible.

## Upgrading

```bash
# change the image tag in 40-api.yaml, then:
kubectl apply -f 40-api.yaml

# or without editing the file:
kubectl set image deploy/librarium-api librarium-api=ghcr.io/fireball1725/librarium-api:26.4.1 -n librarium
kubectl rollout status deploy/librarium-api -n librarium
```

Migrations run automatically on startup — no separate migration job is required.

## Troubleshooting

```bash
kubectl -n librarium get pods
kubectl -n librarium logs deploy/librarium-api
kubectl -n librarium logs statefulset/librarium-db
kubectl -n librarium describe pod -l app=librarium-api
```

**PVC stuck in `Pending`.** Usually means no default `StorageClass`, or the class you specified can't satisfy the access mode. `kubectl describe pvc -n librarium <name>` shows the exact reason.

**API crash-loops with "connect: connection refused" to Postgres.** The db pod may still be initializing — give it 30 seconds. If it persists, check `kubectl -n librarium logs librarium-db-0` and verify the password in `DATABASE_URL` matches `POSTGRES_PASSWORD`.
