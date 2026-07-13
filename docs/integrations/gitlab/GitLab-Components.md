# GitLab Components

StageFreight can publish a project to the **GitLab CI/CD Catalog** as a reusable CI
component — parsing the component spec, generating input documentation, registering
the catalog version on release, and linking the catalog page from release notes.

This is a **supported capability for your projects**. StageFreight itself, however,
no longer ships *as* a GitLab component — see the rationale below.

## Why StageFreight no longer ships itself as a component

This is a decision about StageFreight's *own* repository, not about the feature:

- **Config-surface duplication.** A driver-component's `inputs:` are a hand-maintained,
  1:1 replica of `.stagefreight.yml` that drifts on every change — a second,
  GitLab-specific copy of the same truth. That runs against StageFreight's
  one-config-many-forges design; we don't want to maintain a vendor-specific mirror of
  our own config.
- **Semantic-version coupling.** Being a CI/CD Catalog resource forces **semver release
  tags** (the Catalog requires `vMAJOR.MINOR.PATCH`). StageFreight's per-push dev binary
  channel publishes `dev-{sha8}` / `latest-dev`, which are not semver — so the Catalog
  constraint actively broke the dev release channel. We use **`stagefreight ci render`**
  (one config, every forge) for our own adoption instead.

If you publish *other* projects as components, none of this applies — the capability is
unchanged.

## Using the capability in your project

### 1. Declare a `gitlab-component` target

```yaml
targets:
  - id: my-component
    kind: gitlab-component
    spec_files: ["templates/my-component.yml"]
    catalog: true        # register the version in the GitLab CI/CD Catalog on release
    when: { events: [tag] }
```

`spec_files` lists the component spec file(s) to publish. `catalog: true` registers the
released version in the Catalog (and requires a **semver** release tag — see the
limitation below).

### 2. Generate input documentation

```bash
stagefreight component docs --readme README.md --commit
```

Parses the component spec and injects an inputs table into your README (or any file),
so the documented inputs never drift from the spec.

### 3. Or embed component docs via the narrator

```yaml
narrator:
  - file: "docs/MyComponent.md"
    items:
      - id: component-docs
        kind: component
        spec: "templates/my-component.yml"
        placement:
          between: ["<!-- sf:component:start -->", "<!-- sf:component:end -->"]
          mode: replace
```

## Limitation: catalog links on the release upload path

When a `gitlab-component` target with `catalog: true` exists in your config, the release
upload path **auto-generates a GitLab Catalog link** into the release notes (toggleable
with `release.catalog_links` / `--catalog-links`). Two consequences:

- Releases for a catalog project must use **semver tags** — non-semver tags
  (e.g. `dev-{sha8}`, `latest-dev`) are rejected by the Catalog. For non-semver binary
  distribution on a catalog project, use a binary distribution channel rather than a
  release (see release channels / package distribution).
- If you do *not* want catalog links injected, set `release.catalog_links: false`.

## Example

See [`HomeLabHD/ansible`](https://gitlab.prplanit.com/HomeLabHD/ansible) for a real
project that publishes a reusable GitLab CI component with StageFreight via
`kind: gitlab-component`.
