# AGENTS.md

Guidance for AI coding agents working in `librarium-api`. Humans should read
[CONTRIBUTING.md](./CONTRIBUTING.md) first; this file assumes you have and does
not repeat it.

## What this repo is

The backend for Librarium: a self-hosted, privacy-focused tracker for physical
book, manga, and comic collections. It is the alternative to Libib and similar
cloud catalogue services, not a social reading network.

Librarium is five repos that ship independently:

| Repo | Role |
| --- | --- |
| [`librarium`](https://github.com/FireBall1725/librarium) | Marketing site at librarium.press |
| **`librarium-api`** | **This repo. Go, Postgres, River, OpenAPI** |
| [`librarium-web`](https://github.com/FireBall1725/librarium-web) | React client |
| [`librarium-ios`](https://github.com/FireBall1725/librarium-ios) | SwiftUI client |
| [`librarium-mcp`](https://github.com/FireBall1725/librarium-mcp) | MCP server for Claude Desktop, Cursor, Claude Code |

A release of `librarium-api 26.8.1` implies nothing about the other four. Only
the repo whose feature is shipping bumps.

## Product rules that shape design decisions

- **API-first.** Every feature contract is defined here, then consumed by web,
  iOS, and MCP. Business logic does not go in the clients. If you find yourself
  adding a computed field in React that the API could return, add it to the API.
- **Self-hosted is canon.** There is no paid tier and nothing is held back for
  one. Both editions are free: self-hosted (AGPL 3.0, shipping today) and Lite
  (iOS local-only with iCloud sync). Do not add a feature that only works in a
  cloud-hosted context, and do not add a licence check.
- **FRBR data model.** `books` is the abstract work; `book_editions` is the
  physical manifestation. New metadata goes on whichever layer it belongs to
  conceptually. `publisher` is per-edition. `original_title` is per-work.
- **Multi-server clients.** One client can talk to several Librarium instances
  at once. Never assume the caller only knows about this server.
- **Telemetry is opt-in and off by default.** If you touch anything that
  reports usage, it stays behind an explicit toggle.

## Stack

Go 1.25, Postgres 16, [River](https://riverqueue.com) for background jobs,
`pgx/v5` for database access, stdlib `net/http` for routing (`ServeMux` with
Go 1.22 patterns and `r.PathValue`), `golang-migrate` for schema, `swag` for
OpenAPI generation. No web framework, no ORM.

## Layout

```
cmd/api/main.go          wiring: config, pool, River workers, job registry, HTTP server
internal/
  api/router.go          every route in one file, plus the middleware chain
  api/handlers/          HTTP layer. Decode, validate, call a service, respond
  api/middleware/        auth, rate limiting, request logging
  api/respond/           JSON and error response helpers. Use these, not http.Error
  service/               business logic. Handlers call these; workers call these
  repository/            SQL. One file per aggregate, hand-written queries
  models/                domain structs shared across layers
  db/migrations/         numbered golang-migrate pairs, 39 and counting
  jobs/                  the unified job framework: kind registry and cron scheduler
  workers/               River workers for import, enrichment, AI suggestions
  providers/             book metadata sources: Open Library, Google Books, ISBNdb,
                         Hardcover, ISFDB, MangaDex
  ai/                    AI provider registry: Anthropic, OpenAI, Ollama
  auth/                  JWT issuing and verification, PAT handling
docs/                    generated OpenAPI. Never hand-edit
```

The dependency direction is one way: `handlers` → `service` → `repository`. A
repository never imports a service. A handler never writes SQL.

## Build and test

The full local stack (api, web, db, mcp) lives in the umbrella
[`librarium`](https://github.com/FireBall1725/librarium) workspace under
`local/docker-compose.yml`. After any change here, rebuild the running process:

```bash
cd local && docker compose up -d --build api
docker compose logs -f api
```

Before opening a PR, run what CI runs:

```bash
go build ./...
go vet ./...
gofmt -l .              # must print nothing
go test -race -count=1 ./...
golangci-lint run       # config in .golangci.yml
```

CI also runs `editorconfig-checker` and a Docker build. The jobs live in
[FireBall1725/workflows](https://github.com/FireBall1725/workflows), shared
across every Go repo here, so a CI change belongs there rather than in this
repo's `ci.yml`.

Tests are plain `testing` package, no suite framework, and most run without a
database. If a change needs a live Postgres to test, say so in the PR rather
than adding a container dependency to the default `go test` path.

## Things that will bite you

- **Never edit a shipped migration.** Add a new numbered pair in
  `internal/db/migrations/`. Both `.up.sql` and `.down.sql`, and the down has to
  actually reverse the up.
- **A migration has to roll back in full.** golang-migrate sends each file as
  one simple query and Postgres runs that in an implicit transaction, which is
  what lets `Migrate` retry a failed migration instead of leaving a dirty flag
  for someone to clear by hand. `CREATE INDEX CONCURRENTLY`, `VACUUM` and
  explicit `BEGIN`/`COMMIT` break that, and `TestEveryMigrationIsAtomic` fails
  if one appears.
- **Never hand-edit the version.** `internal/version/version.go` reports
  `0.0.0-dev` and the release workflow injects the real `YY.M.R` via ldflags.
  A version bump in a feature diff will be asked out of the PR.
- **Never edit `CHANGELOG.md`.** Release notes are generated from PR titles, so
  the title is the deliverable. Write it as the line you want users to read.
- **Regenerate OpenAPI when routes change.** `make docs` runs `swag init`.
  Handler doc comments are the source; `docs/` is output.
- **Truncating a seeded table poisons the test database.** The seed migration
  runs once. If a test wipes a table the seed populated, every later test in
  that run sees an empty table.
- **A nil slice marshals to `null`, not `[]`.** The React client crashes on
  `null` where it expects an array. Initialise slices you return as
  `make([]T, 0)`.
- **`strings.ToLower` is not safe for unit symbols.** It folds `Ω` to `ω`,
  which breaks lookups against a table keyed on the uppercase form.
- Row scanning is hand-written and often duplicated across `Get`, `List`, and
  search paths in the same repository file. Changing a `SELECT` means checking
  every scan site in that file, not just the one you came for.

## Conventions

- Every file starts with the SPDX header and copyright line already used
  throughout the repo. Copy the form from a neighbouring file.
- Comments explain why, not what. The existing code is heavy on rationale
  comments above non-obvious decisions; match that density rather than
  narrating each statement.
- Errors wrap with context: `fmt.Errorf("fetching job: %w", err)`.
- Repository methods return `ErrNotFound` for a missing row. Handlers map it to
  404. Do not leak `pgx.ErrNoRows` past the repository.
- Log with `slog`, structured key/value pairs, no `fmt.Println`.
- Commit messages are short and imperative with a scope:
  `fix(auth): expire refresh tokens on password change`.
- Every commit needs a DCO sign-off (`git commit -s`). The DCO app blocks the
  merge without it.
