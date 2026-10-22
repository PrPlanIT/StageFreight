# StageFreight — To-Do / Banked Ideas

This file holds ideas that are NOT committed to the current roadmap. Items
here are candidates for future phases. Banked ≠ planned — each item needs
its own design pass before implementation begins.

## Branch promotion governance (banked idea — naming provisional)

StageFreight could grow a first-class notion of branch-to-branch promotion —
a way to enforce "main is protected; promotion happens through review/merge
from lanes" without leaking that concern into targets or versioning.

### Proposed shape (tentative — section name TBD, `promotion` is a placeholder)

```yaml
promotion:
  mode: gated
  protected_branch: main

  lanes:
    - id: development
      match: develop
      publish:
        registry_tags: ["dev-{sha:8}", "latest-dev"]

    - id: release-candidate
      match: release
      publish:
        registry_tags: ["rc-{sha:8}"]

    - id: production
      match: main
      source_lanes: [development, release-candidate]
      require:
        - ci_green
        - review_approved
      action:
        merge_only: true
        direct_commits: false
```

### Core invariants for this future feature

- **Promotion decides whether code may advance. Versioning decides how it
  identifies itself.** Never mix the two.
- Tag classification (`versioning.tag_sources`) and promotion lanes are
  orthogonal. A lane does not define tag eligibility; branch rules do.
- Matchers stay the primitive for matching branches; lanes reference matchers.
- `targets` remain pure behavior. Lanes may *reference* targets but never
  replace them.

### Why this sits cleanly on top of the Phase 3 versioning model

Phase 3 established three independent axes:

| Axis      | System     | Owns                           |
|-----------|------------|--------------------------------|
| Identity  | versioning | tag_sources, base_from, format |
| Behavior  | targets    | build, push, release           |
| Authority | promotion  | advancement, gating (future)   |

Promotion slots in as the third axis. It does not read from or mutate the
other two. A promotion layer can be added without touching `gitver`,
`versioning`, or `targets` — only the runtime entry point learns to check
promotion gates before executing behavior.

### Open questions before promoting this from "banked" to "designed"

- Is `promotion` the right top-level name? Alternatives: `lanes`, `flow`,
  `governance`, `advance`. Decide only when the feature is seriously scoped.
- How does this compose with the forge accessories sync idea (mirrors)?
- Do lanes carry their own matchers, or reference a global `matchers:` section?
- Is CI-green / review-approved enforcement in scope for StageFreight, or
  delegated to the forge?

### Rule: do not implement until after v0.6.0

Phase 3 locks versioning. Phase 4 locks matchers. Phase 5 locks repo branches.
Promotion is strictly post-v0.6.0 territory. Shipping it earlier risks
conflating authority with identity — exactly what the current phase structure
prevents.

## Pipfile / Poetry dependency apply

pip `requirements.txt` exact pins auto-update today, and the interpreter is provisionable
(`src/toolchain/python.go`, python-build-standalone, checksum-verified). Pipfile and Poetry
apply are not built; they need live iteration against real pipenv/poetry and are not to
be built blind.

Shared foundation (build once):
1. A hardened runner for python3/pip/pipenv/poetry from the provisioned tree — scrubbed
   environment and pinned `PATH`, mirroring the node runner in `dependency/npm_runner.go`.
2. Provisioning the sub-tool through the provisioned pip, cached.
3. An operator-preserving TOML line edit (same discipline as Cargo/pip).

- **Pipfile** — discovery exists (`parsePipfile`, `[packages]`); `apply_pip.go` currently
  skips it. Edit the `[packages]` line, then `pipenv lock` in the Pipfile directory without
  creating a virtualenv. All-or-nothing: an edit without a lock sync leaves a stale
  `Pipfile.lock` and is refused.
- **Poetry** — needs `pyproject.toml` discovery first (`[tool.poetry.dependencies]`,
  `[tool.poetry.group.*.dependencies]`), then edit + `poetry lock` without re-resolving
  unrelated packages (`--no-update` semantics changed in Poetry 2.0) and with
  `POETRY_VIRTUALENVS_CREATE=false`.

**Decision required first:** Python lock resolution cannot match npm's
`--package-lock-only --ignore-scripts`. pip, pipenv, and poetry may build sdists (run
`setup.py` / a PEP 517 backend) while resolving wheel-less packages, which executes package
code. Either accept that, mitigated by the scrubbed environment and `min_release_age`, or
restrict to wheels only and skip packages that need an sdist build.

## Release-notes URLs derived from host

Release-notes links (`registry.ResolvedRegistryTarget.RepoURL` / `TagURL` in
`src/registry/urls.go`) switch on a `Provider` token that `providerFromHost`
(`src/cli/cmd/release_create.go`) re-derives from the host, because `published.json`
deliberately does not record provider. That classification is a redundant middle layer
where vocabulary mismatches and new-registry gaps surface. When this path is next touched,
build the links directly from `(host, path, tag)` (e.g. `docker.io` → `hub.docker.com/…`)
and drop `providerFromHost` from the release path. Do not instead re-read provider from
config — that re-couples release rendering to config, against the manifest contract.
