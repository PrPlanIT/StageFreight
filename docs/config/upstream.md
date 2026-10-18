# Upstream

`upstream:` declares the upstreams a repo repackages or forks. StageFreight lists every
declared source on each run, reports what it found, and refuses to release a version whose
claim about upstream is false — before anything is built.

```yaml
upstream:
  sources:
    core:
      git: https://github.com/ansible/ansible
      scheme: pep440
      select:
        exclude: ['^v1\.']
  version:
    track: core
    since: v2.21.5
```

## Sources

`sources` is an id → source map. Each source names exactly one kind:

| Kind | Value | Listed with |
|---|---|---|
| `git` | repository URL | `git ls-remote` |
| `image` | image repository, no tag or digest | the OCI registry API |

Listing is anonymous: an upstream is someone else's public project.

`select` picks releases out of everything the source tags, in this order:

1. `tags` — a regex choosing candidates. The version is its named group `version`, else its
   first group, else the whole tag. Default: tags that start with a dotted version (`1.2`,
   `v11.4`, `2.21.5`), `v`-prefixed or not — commit-SHA tags are not candidates.
2. `exclude` — regexes; a matching tag is never a release. Exclusion wins.
3. `rewrite` — `{from, to}` regex replacements applied to the extracted version, in order
   (`{from: '_', to: '.'}` reads `release_1_2_3` as `1.2.3`).
4. The version is parsed with the source's `scheme`. `prereleases` — `false` by default —
   decides whether the scheme's prereleases count as releases.

A candidate whose version does not parse is ignored **and reported** (`{upstream.unparsed}`),
never sorted as a string.

### Schemes

Versions are only ever compared within one scheme, so a source names the one its upstream
uses:

| `scheme:` | Reads | Prereleases |
|---|---|---|
| `semver` (default) | `1.2.3`, `v1.2.3-rc.1`; a missing minor or patch is zero (`11.4` = `11.4.0`) | a hyphen suffix |
| `pep440` | Python versions: `2.22.0b1`, `1.0rc1`, `1.0.post1`, `1.0.dev3`, `1!2.0` | `a`/`b`/`rc` and `.dev` |
| `loose` | 1–6 dotted numbers (`0.6.7.1`, `2026.7.0`); a trailing letter is a *later* release (`1.6.60a` > `1.6.60`) | a hyphen suffix |

Our release tags are read with the claimed source's scheme, after the `v` prefix and any
revision suffix (`-3`, `-r2`) are removed.

## Version — what our release tags claim

| `version:` | Our tag claims | A release is refused when |
|---|---|---|
| `track: <source>` | upstream's version | upstream never released that version |
| `continue: <source>` | our own lineage in upstream's numbering | upstream has released that version |
| `own: {}` (default) | our own lineage | never |

Our tag's version is read with the `v` prefix and any revision suffix (`-3`, `-r2`) removed,
using the claimed source's scheme, so `v2.21.5-2` claims `2.21.5`. `since` is the cutover: tags before it are not judged, so
changing mode never reinterprets old releases.

The check fails closed: if the claimed source cannot be listed, a release that needs it does
not ship. Any other listing failure is a warning.

## Signals

What the watcher finds is raised as signals, which notifications select with
[`when.signals`](narration.md):

| Signal | Raised when |
|---|---|
| `updates-available` | the tracked source released a version newer than our newest |
| `version-mismatch` | one of our tags makes a false claim (track: never released; continue: already released) |
| `upstream-unreachable` | a source could not be listed |

```yaml
notifications:
  upstream:
    provider: ntfy
    url: https://ntfy.example.com/forks
    body: "{project}: upstream {upstream.latest}, ours {upstream.ours}"
    when:
      signals: [updates-available, version-mismatch]
```

--8<-- "docs/assets/modules/config-reference.md:upstream"
