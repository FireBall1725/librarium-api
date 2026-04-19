# librarium-api Helm chart

Helm chart for deploying [librarium-api](https://github.com/fireball1725/librarium-api) to Kubernetes.

Built on top of [firelabs-helm-common](https://github.com/FireBall1725/firelabs-helm-common) (a k8s-at-home library-chart fork) and [CloudNativePG](https://cloudnative-pg.io/) for Postgres.

## Prerequisites

- Kubernetes 1.25+
- Helm 3.8+
- [CloudNativePG operator](https://cloudnative-pg.io/documentation/current/installation_upgrade/) installed in the cluster (only if `postgres.create=true`, which is the default)
- A StorageClass that supports `ReadWriteOnce` (or `ReadWriteMany` if you want to run multiple api replicas)

## Install

```bash
cd deploy/helm/librarium-api

# Fetch the common library chart
helm dependency update

# Quick install — generate a JWT secret inline (good for local tests).
# For GitOps / production, use an externally-managed Secret instead. See §Secrets.
helm install librarium . \
  --namespace librarium --create-namespace \
  --set secret.JWT_SECRET="$(openssl rand -base64 48)"
```

Once the pod is ready, port-forward or set up ingress:

```bash
kubectl -n librarium port-forward svc/librarium-api 8080:8080
# then open http://localhost:8080/api/v1/version
```

## Upgrade

```bash
# Bump the image tag
helm upgrade librarium . \
  --namespace librarium --reuse-values \
  --set image.tag=26.4.1
```

Migrations run automatically on api startup.

## Uninstall

```bash
helm uninstall librarium -n librarium
```

This removes the Deployment, Service, ConfigMap, Secret, and — if `postgres.create=true` — the CNPG `Cluster` CR. **PVCs are retained** by default (CNPG's and this chart's `persistence.*`) so your data survives. Delete them manually if you really want them gone:

```bash
kubectl -n librarium delete pvc -l app.kubernetes.io/instance=librarium
```

## Configuration

Most of the schema comes from [firelabs-helm-common](https://github.com/FireBall1725/firelabs-helm-common/blob/main/values.yaml). The librarium-specific knobs:

### Image

| Key | Default | Notes |
|---|---|---|
| `image.repository` | `ghcr.io/fireball1725/librarium-api` | |
| `image.tag` | `latest` | Pin in production. |
| `image.pullPolicy` | `IfNotPresent` | |

### Storage

Two PVCs are created by default:

| Key | Default | Holds |
|---|---|---|
| `persistence.covers` | 5Gi, RWO | Book cover images |
| `persistence.media` | 100Gi, RWO | Uploaded ebooks and audiobooks |

To point media at an existing NFS share, pre-create a matching `PersistentVolume` and set:

```yaml
persistence:
  media:
    existingClaim: librarium-media-nfs
```

Or switch to ReadWriteMany on an RWX-capable class and drop the Recreate strategy to run multiple replicas:

```yaml
controller:
  replicas: 2
  strategy: RollingUpdate

persistence:
  media:
    accessMode: ReadWriteMany
    storageClass: nfs
```

### Non-sensitive config (ConfigMap)

Everything under `configmap.config.data` is rendered as a ConfigMap and loaded via `envFrom`:

```yaml
configmap:
  config:
    data:
      LOG_LEVEL: info
      EBOOK_STORAGE_PATH: /data/media/ebooks
      AUDIOBOOK_STORAGE_PATH: /data/media/audiobooks
      EBOOK_PATH_TEMPLATE: "{title}"
      AUDIOBOOK_PATH_TEMPLATE: "{title}"
      JWT_ACCESS_TTL: 15m
      JWT_REFRESH_TTL: 168h
      REGISTRATION_ENABLED: "true"
```

Change a value and `helm upgrade` — you'll get a new ConfigMap hash and the Deployment will roll. (Or restart manually: `kubectl rollout restart deploy/librarium-api`.)

### Secrets

The api needs exactly one secret value: `JWT_SECRET` (used to sign access and refresh tokens). `DATABASE_URL` is handled separately — CloudNativePG generates it into the `<postgres.clusterName>-app` secret (key: `uri`) and the chart's default `env.DATABASE_URL` reads it from there via `valueFrom`.

You have two equally supported ways to provide `JWT_SECRET`. Pick one.

#### Path A — inline value (simple; good for local tests and CI)

Let the chart render a `Secret` named after the release and wire it into the pod via `envFrom`:

```yaml
secret:
  JWT_SECRET: "<openssl rand -base64 48>"
```

Or at install time:

```bash
helm install librarium . --namespace librarium \
  --set secret.JWT_SECRET="$(openssl rand -base64 48)"
```

Helm owns the Secret and removes it on `helm uninstall`.

#### Path B — externally-managed Secret (recommended for GitOps)

Commit a SealedSecret / ExternalSecret / SOPS-encrypted Secret into your repo, then point the chart at it:

```yaml
secret: {}   # leave empty so the chart does NOT render its own Secret

env:
  JWT_SECRET:
    valueFrom:
      secretKeyRef:
        name: librarium-api-jwt   # your Secret
        key: JWT_SECRET
```

Helm never sees the plaintext value, and the Secret's lifecycle is decoupled from the release (it survives `helm uninstall`).

<details>
<summary>Example: create a SealedSecret</summary>

```bash
kubectl create secret generic librarium-api-jwt \
  --namespace librarium \
  --from-literal=JWT_SECRET="$(openssl rand -base64 48)" \
  --dry-run=client -o yaml \
| kubeseal --scope strict -o yaml > sealed-secret-jwt.yaml
```

Apply `sealed-secret-jwt.yaml` into the namespace (or bundle it into an umbrella chart alongside this one).
</details>

#### What if neither is set?

The chart validates at render time. `helm install` / `helm template` fails with a clear error if `JWT_SECRET` is missing from both paths, and also if `secret.JWT_SECRET` is still the literal placeholder `CHANGE_ME`.

### Postgres (CloudNativePG)

| Key | Default | Notes |
|---|---|---|
| `postgres.create` | `true` | Render a CNPG `Cluster` CR. Set `false` to use an external DB. |
| `postgres.clusterName` | `librarium-db` | Determines the auto-generated secret name (`<clusterName>-app`). |
| `postgres.instances` | `1` | Bump to 3 for HA. |
| `postgres.database` | `librarium` | |
| `postgres.owner` | `librarium` | DB role that owns the database. |
| `postgres.storage.size` | `10Gi` | CNPG's own PVC size. |
| `postgres.storage.storageClass` | *(unset)* | Uses cluster default. |
| `postgres.imageName` | *(unset)* | Leave blank for CNPG's default. |
| `postgres.extraSpec` | `{}` | Merged into the Cluster `spec:` verbatim (backup, resources, monitoring, etc.). |

Advanced CNPG example — nightly backups to S3-compatible storage:

```yaml
postgres:
  extraSpec:
    backup:
      barmanObjectStore:
        destinationPath: s3://my-bucket/librarium
        endpointURL: https://s3.example.com
        s3Credentials:
          accessKeyId:
            name: s3-creds
            key: ACCESS_KEY_ID
          secretAccessKey:
            name: s3-creds
            key: SECRET_ACCESS_KEY
      retentionPolicy: "30d"
```

### External Postgres

Skip CNPG and point at your own DB:

```yaml
postgres:
  create: false

env:
  DATABASE_URL: "postgres://user:pass@db.example.com:5432/librarium?sslmode=require"
```

(Override `env.DATABASE_URL` entirely — the default uses `valueFrom` to read from CNPG's generated secret.)

### Ingress

Disabled by default. Enable and point at your controller:

```yaml
ingress:
  main:
    enabled: true
    ingressClassName: nginx
    hosts:
      - host: librarium-api.example.com
        paths:
          - path: /
            pathType: Prefix
    tls:
      - secretName: librarium-api-tls
        hosts: [librarium-api.example.com]
```

## Chart vs. raw manifests

The raw manifests under [`../kubernetes/`](../kubernetes/) are equivalent for a simple deployment. Use this chart when you want templating, reusable values across environments, or the CNPG integration.
