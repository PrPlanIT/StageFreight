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
  fail_on: critical              # the gate: critical | high | medium | low | off (default off)
  output: ".stagefreight/security"
  release_detail: counts         # default detail level in release notes
```

**The gate** blocks what you can act on and reports the rest. A finding at or above `fail_on`
fails review, and publish withholds the build; the commit itself always lands. Excused, and
still listed in every report:

- an on-record `exceptions:` entry, with a `reason` and an optional `expires` after which it
  gates again;
- a vulnerability proven unreachable (`unreachable_vulns: pass`, the default);
- a vulnerability with no published fix (`unfixed_vulns: pass`, the default), which gates as
  soon as a fix exists.

`fail_on_rules` set the threshold per run with a [`when:`](concepts.md#patterns-conditions),
first match wins. Enforce on releases and report on dev builds, so a newly disclosed
vulnerability never blocks the build that carries its fix:

```yaml
  fail_on: off
  fail_on_rules:
    - when: { git_tags: [stable, prerelease], events: [tag] }
      fail_on: high
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
