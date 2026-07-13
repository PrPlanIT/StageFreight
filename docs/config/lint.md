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
      options:
        cache_ttl: 300
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
      options:
        detect_control_ascii: true
        allow_control_ascii_in_paths: ["src/output/banner_art.go"]
        allow_control_ascii: [27]      # ESC only
```

### Freshness & vulnerabilities

`freshness` checks dependency versions against upstream registries and correlates them
against the OSV database. `vulnerabilities` emits one finding per canonical advisory
(RuleID = the advisory ID, e.g. `GHSA-…`/`CVE-…`), unifying the OSV-API correlation and a
per-lockfile `osv-scanner` pass so the same CVE never surfaces twice — even when an
ecosystem splits its manifest and lockfile into separate files (e.g. npm's `package.json`
vs `package-lock.json`). Vulnerability options are shared with `freshness` unless placed
directly under `vulnerabilities.options`.

A **pinned** `osv-scanner` that fails to resolve hard-fails the gate; an unpinned/unavailable
scanner silently skips the osv-scanner leg (the OSV-API leg still runs).

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
