# Targets

`targets:` declares **what StageFreight does with a build** — push image tags, sync a
registry README, publish a GitLab component, cut a forge release, publish an archive or
package, or deploy a static site. Each entry is a **discriminated union keyed by `kind`**:
the `kind` decides which other keys are valid.

Every target shares an **`id`** (unique) and a **`when:`** routing block (all non-empty
conditions must match — AND logic; see [Patterns & conditions](concepts.md#patterns-conditions)).

## Registry — push image tags

Pushes a build's image to a container registry under one or more `tags:`.

```yaml
targets:
  - id: dockerhub-stable
    kind: registry
    build: myapp                 # references builds[].id
    url: docker.io
    provider: docker             # auto-detected from the URL if omitted
    path: myorg/myapp
    tags:
      - "{version}"
      - "{major}.{minor}"
      - "latest"
    when:
      git_tags: [stable]         # a named matcher from matchers:
      events: [tag]
    credentials: DOCKER          # env-var prefix — see Concepts → Credentials
    retention:
      keep_last: 10
      keep_monthly: 6
```

`provider` is auto-detected from `url` when omitted; the generated reference below lists the
recognized providers (Docker Hub, GHCR, GitLab, Quay, Harbor, JFrog, Gitea, generic OCI).
`credentials`, `tags` template expansion, and `retention` are cross-cutting — see
[Concepts](concepts.md).

## Docker README — sync registry descriptions

Syncs a README to container-registry description fields, with badge injection and link
rewriting.

```yaml
targets:
  - id: dockerhub-readme
    kind: docker-readme
    url: docker.io
    path: myorg/myapp
    credentials: DOCKER
    file: "README.md"
    description: "Short description for Docker Hub"
    link_base: "https://github.com/myorg/myrepo/blob/main"
```

## Release — cut forge releases

Creates a release on the detected forge, with rolling git-tag aliases that track it.

```yaml
targets:
  - id: primary-release
    kind: release
    aliases: ["{version}", "{major}.{minor}", "latest"]
    retention:
      keep_last: 10
      keep_monthly: 6
    when: { git_tags: [stable], events: [tag] }
```

The `aliases` are rolling git tags resolved with the same [template
variables](concepts.md#template-variables) as everything else — `{version}` → `1.2.3`,
`{major}.{minor}` → `1.2`, `latest` → always the newest.

### Mirroring to a remote forge

Provide the remote forge's coordinates and a `sync_*` toggle to mirror releases (release
notes, tags, and scan assets) to a second forge — for example, cutting on GitLab and
mirroring to GitHub. Supported providers: `github`, `gitlab`, `gitea`.

```yaml
targets:
  - id: github-sync
    kind: release
    provider: github
    url: "https://github.com"
    project_id: "myorg/myapp"
    credentials: GITHUB_SYNC     # → GITHUB_SYNC_TOKEN
    aliases: ["{version}"]
    when: { git_tags: [stable], events: [tag] }
    sync_release: true           # mirror release notes and tags
    sync_assets: true            # upload scan artifacts
```

Retention on a mirror target prunes the mirror's releases with the same policy semantics as
the primary — no surprising deletions, and pre-release status is carried across even where
the remote forge has no native pre-release field.

!!! note "CLI"
    Release authoring (`release create`, `release notes`, `release prune`, `release badge`)
    and its flags live in the [CLI Reference](../reference/CLI.md). In CI these run as part
    of the publish phase; you rarely invoke them by hand.

## Other target kinds

`component` (GitLab CI/CD component publish), archive/[package
distribution](package-distribution.md) (`kind: generic-package`), and `pages` (static-site
deploy, e.g. these docs to Cloudflare Pages) are declared the same way — a `kind`, a
`build:` or source, and a `when:`. Their full field sets are in the generated reference
below.

## Reference

The blocks below are **generated from the config source** — for each kind, exactly the
fields it accepts, with each field's meaning, allowed values, and whether it's required.

--8<-- "docs/assets/modules/config-reference.md:targets"
