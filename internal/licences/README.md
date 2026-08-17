# Refreshing the component licence map

`GET /api/v1/components` reports every Go module linked into the API binary so
clients can render the notices on their licences page. The module list and the
versions come from the binary's own build info at runtime, so they are never out
of date and nothing has to be maintained for them.

The one part a program cannot work out is the licence. A Go module does not
declare one anywhere machine-readable, so `internal/licences/licences.go` carries
a map from module path to SPDX identifier, read from each module's own `LICENSE`
file.

## When it needs refreshing

`TestEveryRequirementHasALicence` fails and names the module:

```
module github.com/example/thing has no SPDX identifier in spdxByModule.
Read its LICENSE under $(go env GOMODCACHE) and add a row.
```

That test reads `go.mod`, not build info, because a test binary reports zero
dependencies from `debug.ReadBuildInfo` — the call succeeds and `Deps` is empty,
so a test written against build info would pass by checking nothing.

## Reading one licence by hand

For a single new dependency this is usually quicker than the script:

```sh
ls "$(go env GOMODCACHE)/github.com/example/thing@v1.2.3"
head -5 "$(go env GOMODCACHE)/github.com/example/thing@v1.2.3/LICENSE"
```

Two things to watch for:

- **Uppercase in a module path is escaped in the cache.** `github.com/KyleBanks`
  is stored as `github.com/!kyle!banks`. A lookup that ignores this finds
  nothing and looks like a module with no licence.
- **Some modules ship no `LICENSE` file at all.** `github.com/mattn/go-localereader`
  states MIT in its README and nowhere else. Record where you found it in a
  comment above the row, so the next person does not go hunting for a file that
  is not there.

## Regenerating the whole map

After a dependency sweep, this prints a row per linked module:

```sh
go build -o /tmp/lib-api ./cmd/api
MODCACHE=$(go env GOMODCACHE)
go version -m /tmp/lib-api | awk '$1=="dep"{print $2"\t"$3}' | while IFS=$'\t' read -r mod ver; do
  # Escape uppercase the way the module cache does: Foo -> !foo
  esc=$(printf '%s' "$mod" | perl -pe 's/([A-Z])/"!".lc($1)/ge')
  dir="$MODCACHE/$esc@$ver"
  lic=""
  for f in LICENSE LICENSE.md LICENSE.txt COPYING LICENCE; do
    [ -f "$dir/$f" ] && { lic="$dir/$f"; break; }
  done
  if [ -z "$lic" ]; then printf '\t"%s": "CHECK BY HAND",\n' "$mod"; continue; fi
  head -60 "$lic" > /tmp/l.txt
  if   grep -qi "Apache License"                                  /tmp/l.txt; then id="Apache-2.0"
  elif grep -qi "Mozilla Public License"                          /tmp/l.txt; then id="MPL-2.0"
  elif grep -qi "GNU LESSER"                                      /tmp/l.txt; then id="LGPL"
  elif grep -qi "GNU GENERAL"                                     /tmp/l.txt; then id="GPL"
  elif grep -qi "Redistributions of source code must retain"      /tmp/l.txt; then
       if grep -qi "Neither the name" /tmp/l.txt; then id="BSD-3-Clause"; else id="BSD-2-Clause"; fi
  elif grep -qi "Permission is hereby granted, free of charge"    /tmp/l.txt; then id="MIT"
  elif grep -qi "ISC License"                                     /tmp/l.txt; then id="ISC"
  else id="CHECK BY HAND"; fi
  printf '\t"%s": "%s",\n' "$mod" "$id"
done | sort
```

It matches on licence text, so treat it as a first pass rather than an answer:
check every `CHECK BY HAND` row yourself, and spot-check the rest. `gofmt -w`
the file afterwards to align the map.

Note the script lists what the **binary links**, whereas the test checks what
**go.mod requires**. go.mod is the larger set — a couple of modules are required
but never linked — so the test will still ask for rows the script did not print.
That is the safe direction: a spare row costs nothing, a shipped module with no
notice does not.
