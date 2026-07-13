# StageFreight — Security Scanning Configuration

How StageFreight scans images for vulnerabilities, generates SBOMs, and
embeds security information in release notes.

> **Reference docs:** [Config Reference — security](reference/Config.md#config-security) · [CLI Reference — security](reference/CLI.md#cli-stagefreight-security)

---

## Configuration

```yaml
security:
  enabled: true
  scanners:
    trivy: true                    # run Trivy image scan (default: true)
    grype: true                    # run Grype image scan (default: true)
  sbom: true                      # generate SBOM via Syft (default: true)
  fail_on_critical: false          # exit non-zero on critical vulns
  output_dir: ".stagefreight/security"
  release_detail: counts           # default detail level
  overwhelm_message: ["…maybe start here:"]
  overwhelm_link: ""               # URL for overwhelm message
```

---

## Scanners

Two vulnerability scanners are supported:

| Scanner | Default | Description |
|---------|---------|-------------|
| Trivy | enabled | Container image vulnerability scanning |
| Grype | enabled | Container image vulnerability scanning (Anchore) |

Both default to enabled. Scanners still require their binary in PATH.
Toggle individually:

```yaml
scanners:
  trivy: true
  grype: false    # disable Grype
```

---

## Detail Levels

Controls how much security information is embedded in release notes.

| Level | Description |
|-------|-------------|
| `none` | No security info in release notes |
| `counts` | Vulnerability count summary (e.g., "0 critical, 2 high") |
| `detailed` | Count summary with affected package list |
| `full` | Full vulnerability table with CVE IDs, severity, and descriptions |

### Conditional Detail Rules

Override detail level based on tag/branch patterns. Evaluated top-down,
first match wins.

```yaml
  release_detail_rules:
    - tag: "^v\\d+\\.\\d+\\.\\d+$"    # stable releases → full detail
      detail: "full"

    - branch: "^main$"                 # main branch → detailed
      detail: "detailed"

    - detail: "counts"                 # catch-all
```

**Precedence**: CLI `--security-detail` flag > first matching rule > `release_detail` default.

---

## Condition Primitive

The universal conditional rule used across StageFreight for tag/branch matching.

```yaml
tag: "^v\\d+\\.\\d+\\.\\d+$"     # regex match (default)
branch: "!^feature/.*"            # negated regex (! prefix)
```

- Multiple fields set: AND — all must match.
- No fields set: catch-all (always matches).
- Rules evaluated top-down, first match wins.

---

## Scan Artifacts

After a scan, the output directory contains:

| File | Format | Description |
|------|--------|-------------|
| `results.json` | Trivy JSON | Raw vulnerability scan results |
| `results.sarif` | SARIF | For GitLab/GitHub security dashboard integration |
| `sbom.json` | CycloneDX | Software Bill of Materials (when `sbom: true`) |
| `summary.md` | Markdown | Human-readable summary at configured detail level |

---

## CLI Commands

See [CLI Reference](reference/CLI.md#cli-stagefreight-security) for full
flag documentation.

```bash
stagefreight security scan --image "myorg/myapp:latest" --output .stagefreight/security/
```
