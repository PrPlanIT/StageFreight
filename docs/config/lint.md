# Lint

StageFreight's built-in linters — content hygiene, dependency freshness, secret detection,
merge-conflict markers, Unicode/bidi defenses, file size, and more — run as a pipeline gate.
They are cache-aware and parallel, and by default scan only what changed.

```yaml
lint:
  level: changed              # "changed" (delta-only) or "full"
  exclude:
    - "vendor/**"
    - "*.generated.go"
  modules:
    secrets: { enabled: true }
    freshness:
      enabled: true
      cache_ttl: 300      # module options are inline — no `options:` wrapper
```

`--level` (or `--all` for `full`) overrides `lint.level`, which overrides the `changed`
default. Individual modules can be toggled in config or with `--module` / `--no-module`.

## Modules

Content-only modules produce deterministic output and are cached forever by content hash.
The freshness and vulnerabilities modules depend on external state (registries, the OSV
database) and use TTL-based caching.

| Module | Default | Detects |
|--------|---------|---------|
| `tabs` | enabled | Tab characters |
| `secrets` | enabled | Committed secrets (gitleaks) |
| `conflicts` | enabled | Unresolved merge-conflict markers |
| `filesize` | enabled | Files over a size threshold (default 500 KB) |
| `linecount` | **disabled** | Files over a line-count threshold (default 1000) |
| `links` | **disabled** | Markdown links that don't resolve: internal, anchors, external (see below) |
| `unicode` | enabled | Dangerous Unicode (see below) |
| `yaml` | enabled | YAML syntax errors |
| `lineendings` | enabled | Inconsistent line endings |
| `freshness` | enabled | Out-of-date dependencies + correlated CVEs (TTL-cached) |
| `vulnerabilities` | enabled | Advisories against lockfiles (TTL-cached; `osv` is a deprecated alias) |

### Unicode — supply-chain defense

The `unicode` module defends against trojan-source attacks, invisible-text obfuscation, and
control-byte smuggling. BiDi overrides, zero-width characters, tag characters, confusable
whitespace, and invalid UTF-8 are flagged; ASCII control bytes are gated by a path-scoped
allowlist.

```yaml
    unicode:
      detect_control_ascii: true
      allow_control_ascii_in_paths: ["src/output/banner_art.go"]
      allow_control_ascii: [27]        # ESC only
```

### Freshness & vulnerabilities

`freshness` checks dependency versions against upstream registries and correlates them
against the OSV database. `vulnerabilities` emits one finding per canonical advisory
(RuleID = the advisory ID, e.g. `GHSA-…`/`CVE-…`), unifying the OSV-API correlation and a
per-lockfile `osv-scanner` pass so the same CVE never surfaces twice — even when an
ecosystem splits its manifest and lockfile into separate files (e.g. npm's `package.json`
vs `package-lock.json`). Vulnerability options are shared with `freshness` unless placed
directly under the `vulnerabilities` module.

A **pinned** `osv-scanner` that fails to resolve hard-fails the gate; an unpinned/unavailable
scanner silently skips the osv-scanner leg (the OSV-API leg still runs).

### Links — markdown link integrity

The `links` module checks that every markdown link resolves, so a moved or renamed page can't
silently break the pages that point at it. It is a whole-repo check (a move breaks links in
files that did not themselves change), so it always sees the entire repo — even under
`--level changed`, where other modules scan only the delta, and under `--baseline`, where it is
diffed against the whole base tree so a link this change actually broke still blocks while
pre-existing link debt does not. It is **opt-in**: off unless a config enables it, which keeps
it off large repos that never ask for it.

Three link classes, each with its own severity (`error` blocks, `warn` reports, `off` skips).
Enabling the module is purely local and additive: the two filesystem-only classes default to
`warn` (they report, never break a build), and `external` defaults to `off` so turning the
module on makes no network call. Escalate a class to `error` to make it block; opt `external`
in to `warn`/`error` when you want reachability checked:

| Class | Default | Checks |
|-------|---------|--------|
| `internal` | `warn` | Repo-relative (beside the file) and wiki-root (`/a/b`, resolved against `root` as `<path>.md`) targets exist |
| `anchors` | `warn` | `#section` matches a heading (or explicit `{#id}` / HTML `id`/`name`) in the target page |
| `external` | `off` | `http(s)` URLs are reachable (opt-in — a network fetch per URL) |

```yaml
    links:
      enabled: true
      internal: error           # error | warn | off — escalate the filesystem-certain class
      # anchors rides the warn default (report, never block); external is off unless set
      root: ""                  # wiki root for absolute links (default: repo root)
      include: ["docs/**"]      # optional scope; default is every markdown file
      exclude: ["**/vendor/**"]
      external_skip: ["https://staging.example/*"]   # URL globs left unchecked when external is on
      external_timeout_ms: 5000
```

`mailto:`/`tel:` and other non-fetchable schemes are left alone, and fenced code blocks are
skipped. Internal targets resolve with the usual wiki conventions: a bare path matches
`<path>.md`/`.markdown`, a directory index, or an existing directory. Findings carry a stable
`RuleID` (`unresolved-link` / `unresolved-anchor` / `external-link`) with the link as the
anchor, so `--baseline` flags only newly-introduced breakage and a repo with existing link debt
is not lit up wall-to-wall on day one.

Only `internal` is fully deterministic — a target file either exists or it doesn't — which is why
it is the class worth escalating to `error`. `anchors` depends on heading-slug matching (done
GitHub-style, which a given renderer may compute differently), so it defaults to `warn` and
should be escalated to `error` only once validated against the surface that actually renders the
docs. `external` depends on the network, so it stays `off` until opted in.

## Cache TTL contract

Each module declares its caching behavior, which the engine honors uniformly:

| `CacheTTL()` | Engine behavior | Expiry |
|--------------|-----------------|--------|
| `> 0` | Cache with TTL | Expires when `now - CachedAt > TTL` |
| `== 0` | Cache forever | No expiry check |
| `< 0` | Never cache | Skips Get + Put |
| not implemented | Cache forever | No expiry check |

## Reference

--8<-- "docs/assets/modules/config-reference.md:lint"
