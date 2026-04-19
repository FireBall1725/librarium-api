# Changelog

All notable changes to **librarium-api** are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## Versioning

This project uses **`YY.MM.revision`** (e.g. `26.4.0`, `26.4.1`):

- `YY` — two-digit release year.
- `MM` — release month, *not* zero-padded.
- `revision` — feature counter within the month, starting at `0`. Resets to `0` when the month rolls over.
- `-dev` suffix marks local unshipped builds; never released.

Versions `0.1.0` → `0.13.0` predate this scheme. `26.4.0` is the first release cut under the new format and the first release of `librarium-api` as an independent repository.

## [26.4.1] — AI-powered book suggestions

End-to-end suggestions feature that recommends books to buy (not in the library) and books to read next (owned but unread). All AI access is opt-in at both the admin (data-category permissions) and user (master toggle) level — restrictive-wins.

### Added

- Provider abstraction with out-of-the-box Anthropic, OpenAI, and Ollama drivers — admin picks a single active provider via `Connections → AI`. Per-provider config, test button, and masked API key handling mirror the existing metadata-provider pattern.
- Two-layer permission model (`ai:permissions` instance settings + per-user `user_ai_settings.opt_in`) gates what data is sent to the AI: reading history, ratings, favourites, full library, taste profile.
- Per-user taste profile (JSONB on `user_ai_settings`) — genres/themes/formats love/avoid lists, era, favourite authors, hard-nos. Feeds into the prompt alongside behavioural signals (reads, ratings, favourites).
- Scheduled `ai-suggestions` job with enabled/cadence/max-buy/max-read-next/include-taste-profile/user-rate-limit config, admin-wide and per-user `Run now` endpoints, with rate limiting enforced at handler and service layers.
- Suggestions worker: multi-pass generation (candidate → ISBN-grounded enrichment via metadata providers → backfill on rejection) with fuzzy title match to detect hallucinated ISBNs. Cost and token counts recorded per run.
- Persistent block list (`ai_blocked_items`) with book / author scopes — blocks are surfaced as prompt exclusions on future runs.
- User-facing endpoints: `GET/PUT /me/ai-prefs`, `GET/PUT /me/taste-profile`, `GET /me/suggestions`, `PUT /me/suggestions/{id}/status`, `POST /me/suggestions/{id}/block`, `POST /me/suggestions/run`.
- Admin endpoints: `GET/PUT /admin/connections/ai`, `POST /admin/connections/ai/{provider}/test`, `POST /admin/connections/ai/active`, `GET/PUT /admin/connections/ai/permissions`, `GET/PUT /admin/jobs/ai-suggestions`, `POST /admin/jobs/ai-suggestions/run`.
- Migration `000004_ai_suggestions` — new tables: `user_ai_settings`, `ai_suggestion_runs`, `ai_suggestions`, `ai_blocked_items`.

## [26.4.0] — Initial independent release

First release of `librarium-api` as a standalone repository under the `YY.MM.revision` versioning scheme. Feature parity with the pre-split workspace as of April 2026 — see the archived workspace changelog for the full history up to this point.

### Added

- README, CONTRIBUTING, CI and release workflows, and this CHANGELOG.
- GitHub Pages deployment for Swagger/Redoc API reference on each release.
- Helm chart (`deploy/helm/librarium-api`) with optional CloudNativePG cluster provisioning.

### Changed

- Build timestamps use the host timezone (via `/etc/localtime` bind mount in `docker-compose.yml`) so `BuildVersion` is human-readable wherever you run it.
