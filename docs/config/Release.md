# StageFreight — Release Configuration

How StageFreight creates releases, manages rolling tags, and syncs across
forges using `targets:` entries with `kind: release`.

> **Reference docs:** [Config Reference — targets](reference/Config.md#config-targets) · [CLI Reference — release](reference/CLI.md#cli-stagefreight-release)

---

## Release Target

A release target creates forge releases with rolling git tag aliases.

```yaml
targets:
  - id: primary-release
    kind: release
    aliases: ["{version}", "{major}.{minor}", "latest"]
    when: { git_tags: [stable], events: [tag] }
```

### Remote Release Sync

To mirror releases to a remote forge, provide all four remote fields:

```yaml
targets:
  - id: github-sync
    kind: release
    provider: github
    url: "https://github.com"
    project_id: "myorg/myapp"
    credentials: GITHUB_SYNC   # → GITHUB_SYNC_TOKEN
    aliases: ["{version}"]
    when: { git_tags: [stable], events: [tag] }
    sync_release: true          # sync release notes and tags
    sync_assets: true           # upload scan artifacts
```

Supported providers for release: `github`, `gitlab`, `gitea`.

---

## Rolling Tag Aliases

The `aliases` field defines rolling git tags that track releases:

```yaml
aliases:
  - "{version}"          # 1.2.3
  - "{major}.{minor}"    # 1.2
  - "latest"             # always points to newest
```

Tags are resolved against version info using the same
[template variables](Narrator.md#template-variables) as other config fields.

---

## Release Retention

Apply retention policies to automatically prune old releases:

```yaml
targets:
  - id: primary-release
    kind: release
    retention:
      keep_last: 10
      keep_monthly: 6
```

See [Docker — Retention Policy](Docker.md#retention-policy) for the
full policy syntax.

---

## CLI Commands

See [CLI Reference](reference/CLI.md#cli-stagefreight-release) for full
flag documentation.

### `release create`

Create a release on the detected forge with auto-generated or provided
release notes. Uploads assets, adds registry links, creates rolling tags,
syncs to targets, and applies retention.

```bash
stagefreight release create --tag "$CI_COMMIT_TAG" \
  --security-summary .stagefreight/security/ \
  --registry-links --catalog-links
```

### `release badge`

Generate and commit a release status badge SVG via the forge API.

```bash
stagefreight release badge
```

### `release notes`

Generate markdown release notes from conventional commits between two refs.

```bash
stagefreight release notes --from v1.0.0 --to HEAD -o notes.md
```

### `release prune`

Delete old releases using the configured retention policy.

```bash
stagefreight release prune --dry-run
```

---

## Release Create Flow

1. Detect version from git
2. Generate or load release notes (conventional commits)
3. Create release on detected forge (GitLab, GitHub, Gitea)
4. Upload asset files (SARIF, SBOM, etc.)
5. Add registry image links (one per configured registry target)
6. Add GitLab Catalog link (if applicable)
7. Create rolling tags from `aliases` templates
8. Sync release to remote release targets
9. Apply retention policy (auto-prune old releases)
