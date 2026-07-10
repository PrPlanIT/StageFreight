# StageFreight — Linter Configuration

How StageFreight lints your codebase using cache-aware, parallel modules
with delta-only scanning.

> **Reference docs:** [Config Reference — lint](reference/Config.md#config-lint) · [CLI Reference — lint](reference/CLI.md#cli-stagefreight-lint)

---

## Configuration

```yaml
lint:
  level: changed                  # "changed" (delta-only) or "full"
  cache_dir: ""                   # override cache dir (default: XDG)
  exclude:
    - "vendor/**"
    - "*.generated.go"
  modules:
    secrets:
      enabled: true
    tabs:
      enabled: true
    freshness:
      enabled: true
      options:
        cache_ttl: 300
```

---

## Modules

10 lint modules, each independently togglable. Content-only modules produce
deterministic output and are cached forever by content hash. The freshness
and vulnerabilities modules depend on external state (registries, the OSV
database, osv-scanner) and use TTL-based caching.

### Content-Only Modules

| Module | Default | Description |
|--------|---------|-------------|
| `tabs` | enabled | Detects tab characters |
| `secrets` | enabled | Detects committed secrets (gitleaks) |
| `conflicts` | enabled | Detects unresolved merge conflict markers |
| `filesize` | enabled | Detects files exceeding size threshold (default: 500 KB) |
| `linecount` | **disabled** | Detects files exceeding line count threshold (default: 1000) |
| `unicode` | enabled | Detects dangerous Unicode (BiDi overrides, zero-width, control bytes) |
| `yaml` | enabled | Validates YAML syntax |
| `lineendings` | enabled | Detects inconsistent line endings |

### Freshness Module (TTL-Aware)

Checks dependency versions against upstream registries and correlates
against the OSV vulnerability database.

```yaml
    freshness:
      enabled: true
      options:
        cache_ttl: 300            # seconds (default: 5 min)
        sources:
          docker_images: true
          go_modules: true
          # ... all ecosystems enabled by default
        vulnerability:
          enabled: true
          min_severity: "moderate"
```

See the conceptual docs below for full freshness configuration including
severity mapping, package rules, groups, and registry overrides.

### Vulnerabilities Module (TTL-Aware)

Emits one finding per canonical advisory (RuleID = the advisory ID, e.g.
`GHSA-…` / `CVE-…`), unifying two observation sources so the same CVE never
produces two findings:

- **OSV-API correlation** — the vulnerabilities already attached to a
  file's resolved dependencies (the same correlation `freshness` reads for
  its `[N CVE(s)]` annotation).
- **osv-scanner** — a per-file scan of the lockfile itself.

Both legs are reduced to one canonical `Vulnerability` per advisory before
rendering — a CVE surfaced by both sources still yields a single finding.

```yaml
    vulnerabilities:       # "osv" is a deprecated alias for this key
      enabled: true
```

Vulnerability config (`min_severity`, `enabled`, `severity_override`,
ignores, source toggles) is **shared with `freshness`** by default: this
module reads its options from `lint.modules.freshness.options` unless
options are placed directly under `lint.modules.vulnerabilities.options`
(or its `osv` alias), which take precedence when present — letting a
project configure vulnerability correlation independently of freshness.

A **pinned** `osv-scanner` version that fails to resolve hard-fails the
gate; an unpinned/unavailable scanner silently skips the osv-scanner leg
(the OSV-API leg still runs).

---

## Unicode Module

Supply-chain defense against trojan-source attacks, invisible text
obfuscation, and control byte smuggling.

| Category | Config Key | Default | Severity |
|----------|-----------|---------|----------|
| BiDi overrides | `detect_bidi` | `true` | critical |
| Zero-width chars | `detect_zero_width` | `true` | critical |
| ASCII control bytes | `detect_control_ascii` | `true` | warning |
| Tag characters | — | always on | critical |
| Confusable whitespace | — | always on | warning |
| Invalid UTF-8 | — | always on | warning |

Path-scoped allowlists gate only ASCII control bytes:

```yaml
    unicode:
      options:
        detect_control_ascii: true
        allow_control_ascii_in_paths: ["src/output/banner_art.go"]
        allow_control_ascii: [27]    # ESC only
```

---

## Cache TTL Contract

| `CacheTTL()` Return | Engine Behavior | Expiry Logic |
|----------------------|-----------------|--------------|
| `> 0` | Cache with TTL | Expires when `now - CachedAt > TTL` |
| `== 0` | Cache forever | No expiry check |
| `< 0` | Never cache | Skip Get + Put |
| Not implemented | Cache forever | No expiry check |

---

## CLI Commands

See [CLI Reference](reference/CLI.md#cli-stagefreight-lint) for full
flag documentation.

```bash
stagefreight lint [paths...] [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--level` | string | from config | `changed` or `full` |
| `--module` | string slice | all enabled | Run only these modules |
| `--no-module` | string slice | none | Skip these modules |
| `--no-cache` | bool | `false` | Clear cache and rescan |
| `--all` | bool | `false` | Shorthand for `--level full` |

**Precedence**: `--level` flag > `lint.level` config > `"changed"` default.
