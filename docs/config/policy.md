# Policy

The cross-cutting rules that shape *when* and *how* the pipeline acts — event routing,
version identity, release/security behavior, commit and tag authoring, toolchain pins, and
publish manifests. These are the knobs you reach for once the basics work.

A few of these keys carry behavior worth explaining before the generated field reference;
the rest are documented inline in the reference blocks below.

## Matchers — named routing

`matchers:` defines named patterns (`stable`, `edge`, …) that targets reference from
`when.git_tags` / `when.branches`, so routing lives in one place. The matching syntax
itself — regex, negation, literal, first-match-wins — is shared with every conditional field
and documented under [Concepts → Patterns & conditions](concepts.md#patterns-conditions).

## Security scanning

`security:` scans built images for vulnerabilities, generates SBOMs, and embeds the results
in release notes. Both scanners default on but still require their binary in `PATH`.

```yaml
security:
  enabled: true
  scanners:
    trivy: true                  # container image scan (default: true)
    grype: true                  # container image scan, Anchore (default: true)
  sbom: true                     # generate a CycloneDX SBOM via Syft
  fail_on_critical: false        # exit non-zero on critical vulns
  output_dir: ".stagefreight/security"
  release_detail: counts         # default detail level in release notes
```

**Detail levels** control how much lands in release notes: `none`, `counts` (e.g. "0
critical, 2 high"), `detailed` (counts + affected packages), or `full` (a table with CVE
IDs, severity, and descriptions). `release_detail_rules` override the level per tag/branch
(top-down, first match wins — the same [condition
syntax](concepts.md#patterns-conditions) as everywhere else):

```yaml
  release_detail_rules:
    - tag: "^v\\d+\\.\\d+\\.\\d+$"    # stable releases → full
      detail: "full"
    - branch: "^main$"                # main → detailed
      detail: "detailed"
    - detail: "counts"                # catch-all
```

Precedence: CLI `--security-detail` > first matching rule > `release_detail`.

A scan writes `results.json` (Trivy JSON), `results.sarif` (for GitLab/GitHub security
dashboards), `sbom.json` (CycloneDX, when `sbom: true`), and `summary.md` into `output_dir`.
These become release assets — see [Targets → Release](targets.md#release-cut-forge-releases).

## Reference

Each key's generated reference follows.

--8<-- "docs/assets/modules/config-reference.md:matchers"

--8<-- "docs/assets/modules/config-reference.md:versioning"

--8<-- "docs/assets/modules/config-reference.md:ci"

--8<-- "docs/assets/modules/config-reference.md:commit"

--8<-- "docs/assets/modules/config-reference.md:dependency"

--8<-- "docs/assets/modules/config-reference.md:release"

--8<-- "docs/assets/modules/config-reference.md:security"

--8<-- "docs/assets/modules/config-reference.md:manifest"

--8<-- "docs/assets/modules/config-reference.md:toolchains"

--8<-- "docs/assets/modules/config-reference.md:glossary"

--8<-- "docs/assets/modules/config-reference.md:presentation"

--8<-- "docs/assets/modules/config-reference.md:tag"
