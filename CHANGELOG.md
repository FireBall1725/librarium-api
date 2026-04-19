# Changelog

All notable changes to **librarium-api** are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## Versioning

This project uses **`YY.MM.revision`** (e.g. `26.4.0`, `26.4.1`):

- `YY` — two-digit release year.
- `MM` — release month, *not* zero-padded.
- `revision` — feature counter within the month, starting at `0`. Resets to `0` when the month rolls over.
- `-dev` suffix marks local unshipped builds; never released.

Versions `0.1.0` → `0.13.0` predate this scheme. `26.4.0` is the first release cut under the new format and the first release of `librarium-api` as an independent repository.

## [26.4.0] — Initial independent release

First release of `librarium-api` as a standalone repository under the `YY.MM.revision` versioning scheme. Feature parity with the pre-split workspace as of April 2026 — see the archived workspace changelog for the full history up to this point.

### Added

- README, CONTRIBUTING, CI and release workflows, and this CHANGELOG.
- GitHub Pages deployment for Swagger/Redoc API reference on each release.
- Helm chart (`deploy/helm/librarium-api`) with optional CloudNativePG cluster provisioning.

### Changed

- Build timestamps use the host timezone (via `/etc/localtime` bind mount in `docker-compose.yml`) so `BuildVersion` is human-readable wherever you run it.
