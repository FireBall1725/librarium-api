# Security policy

## Reporting a vulnerability

**Please report security issues privately**, not via public issues.

Use GitHub's private vulnerability reporting on this repo:

→ https://github.com/fireball1725/librarium-api/security/advisories/new

That keeps the report visible only to maintainers until a fix is ready, and gives us a paper trail to coordinate disclosure on.

## What's in scope

Anything that lets an attacker:

- Read or modify another user's data (including across libraries on a multi-tenant instance)
- Bypass authentication, authorization, or the multi-server permission boundary
- Execute code on the API host, or escape the Postgres / River boundaries
- Smuggle data through CSV import, ISBN lookup, or cover fetching

If you're not sure whether something counts, file it — we'd rather see a borderline report than miss a real one.

## What's out of scope

- Issues that require admin access on the same machine to exploit
- Self-XSS that requires the user to paste attacker-supplied JS into their own console
- Findings from automated scanners that aren't reproducible against a real deployment
- DoS via volumetric traffic to a self-hosted instance

## Response

This is a small, self-hosted project run by a single maintainer. Best-effort response targets:

- **Acknowledgement**: within 1 week
- **Initial triage**: within 2 weeks
- **Fix or mitigation plan**: within 4 weeks for high-severity issues

We'll credit you in the release notes when the fix ships, unless you'd prefer to stay anonymous.

## Disclosure

We follow coordinated disclosure. Once a fix is released and operators have had a reasonable window to update (typically 14 days), the underlying issue can be discussed publicly.
