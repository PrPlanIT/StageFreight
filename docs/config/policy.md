# Policy

The cross-cutting rules that shape *when* and *how* the pipeline acts — event routing,
version identity, release/security behavior, commit and tag authoring, toolchain pins, and
publish manifests. These are the knobs you reach for once the basics work.

A few of these keys carry behavior worth explaining before the generated field reference;
the rest are documented inline in the reference blocks below.

## Git — ref interpretation & versioning

`git:` interprets the current ref into named **branch** and **tag** patterns and the versions
they imply. `git.branches` and `git.tags` define named patterns (`main`, `stable`, …) that
targets reference from `when.branches` / `when.git_tags`, so routing lives in one place;
`git.versioning` derives `{version}` from those patterns (base tag source + format). The
matching syntax itself — regex, negation, literal — is shared with every conditional field and
documented under [Concepts → Patterns & conditions](concepts.md#patterns-conditions).

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
These become release assets — see [Publish → Release](publish.md#release-cut-forge-releases).

## Reference

Each key's generated reference follows.

--8<-- "docs/assets/modules/config-reference.md:git"

--8<-- "docs/assets/modules/config-reference.md:ci"

--8<-- "docs/assets/modules/config-reference.md:commit"

--8<-- "docs/assets/modules/config-reference.md:tagging"

--8<-- "docs/assets/modules/config-reference.md:dependency"

--8<-- "docs/assets/modules/config-reference.md:release"

--8<-- "docs/assets/modules/config-reference.md:security"

--8<-- "docs/assets/modules/config-reference.md:manifest"

--8<-- "docs/assets/modules/config-reference.md:toolchains"

--8<-- "docs/assets/modules/config-reference.md:glossary"
