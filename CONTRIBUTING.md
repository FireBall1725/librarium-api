# Contributing to librarium-api

Thanks for your interest in contributing. This document covers how to submit changes, what's expected in a PR, and the legal terms your contribution is made under.

## Before you start

- Check the open issues — your change may already be in progress or planned differently.
- For anything non-trivial, open an issue to discuss before writing code. This saves everyone time if the scope or approach needs adjustment.
- Self-hosted is the primary deployment target. Don't introduce features that only work in a cloud-hosted context.
- API-first: feature contracts are defined here and consumed by `librarium-web` and `librarium-ios`. Don't push business logic into the clients.

## Development setup

The everyday dev stack lives in the umbrella [`librarium`](https://github.com/fireball1725/librarium) workspace under `local/docker-compose.yml` (api + web + db + mcp). After any code change to `api/`, rebuild and restart:

```bash
cd local && docker compose up -d --build api
```

The standalone `api/docker-compose.yml` exists for api-only deployment scenarios but conflicts on host ports when the `local/` stack is running, so prefer `local/` for everyday development.

For running Go tooling directly (`go test`, `go vet`), install Go 1.25.

## Making changes

- Keep changes focused. One PR = one feature/fix.
- Add or update tests for any behavior change.
- Run `go vet ./...` and `go test ./...` before submitting.
- If you add handler endpoints or change request/response shapes, regenerate swagger: `make swagger`.
- If you add a database column or table, add a migration in `internal/db/migrations/` — never edit a shipped migration.

## Commit messages

Short, imperative, reference the scope: `feat(covers): accept HEIC uploads`, `fix(auth): expire refresh tokens on password change`, `chore(ci): cache go modules`.

## Pull requests

- Rebase on `main` before opening the PR.
- The PR description should explain the *why*, not just the *what* — link to the issue if there is one.
- CI must pass before review.
- Don't hand-edit a `CHANGELOG.md` — release notes are auto-generated from PR titles by the release workflow. Write a clear, descriptive title.

## License

The project is licensed under the **GNU Affero General Public License v3.0 only** ([LICENSE](./LICENSE)). Contributions are accepted under the same license — nothing is assigned to the maintainer and no separate commercial-relicensing grant is involved.

## Sign your commits (DCO)

Every commit in a pull request must carry a `Signed-off-by:` trailer certifying the [Developer Certificate of Origin 1.1](./DCO). It says you have the right to contribute the code and you're fine with it being distributed under the project's license.

To sign off, just pass `-s` to `git commit`:

```bash
git commit -s -m "feat(covers): accept HEIC uploads"
```

That appends a line like this to the commit message, using your `user.name` and `user.email` from git config:

```
Signed-off-by: Jane Contributor <jane@example.com>
```

If you forget on one commit, amend it:

```bash
git commit --amend -s --no-edit
```

If you forget on several, rebase with `--signoff`:

```bash
git rebase --signoff main
```

The [DCO GitHub App](https://github.com/apps/dco) runs on every PR and blocks the merge if any commit is missing a sign-off.

## Code of conduct

Be decent. Assume good faith. Technical disagreements are fine; personal attacks aren't.
