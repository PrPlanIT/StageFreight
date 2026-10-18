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
  evidence:
    image:                      # build id
      probe: { package: ansible-core, manager: pip }
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

`on_mismatch: report` (with a required `reason:`) lets a false claim ship and raises
`version-mismatch` instead of refusing — for a lineage shared with upstream, say. The default
is `refuse`.

## Evidence — the image must contain what the tag says

A version that upstream released is still a lie if the image ships something else (an
image-only fix tagged as the next upstream version). So `track` needs **evidence** for every
docker build, checked in publish on the exact bytes about to be distributed, before anything
reaches a registry. There are two kinds:

**A probe** reads the version out of the built image without running it:

| Probe | Reads |
|---|---|
| `label: <key>` | an OCI image label |
| `file: /abs/path` | a file in the image (symlinks followed); `match:` picks the version |
| `package: <name>` + `manager: pip \| dpkg \| apk` | the package database — works on images with no shell |

`match:` is a regex whose `version` group (else first group) is the version; the default takes
the first dotted version. A package manager's own revision (`-r3`, Debian `-1`, epochs) is
removed before comparing. A probe that disagrees with the release tag refuses that image; a
probe that cannot read anything refuses it too — unproven is not proven. Per build,
`on_mismatch: report` + `reason:` tolerates an upstream whose metadata trails its own tag.

**Ancestry** (`version.ancestry: true`, git sources) proves a history fork instead: upstream's
commit for the claimed version must be an ancestor of the commit being released. It is the
right evidence where the build stamps its own version into the binary, which makes a probe
circular.

```yaml
upstream:
  sources:
    zitadel: { git: https://github.com/zitadel/zitadel }
  version: { track: zitadel, ancestry: true }
```

Validation names any docker build that has neither.

## Pins — moving with upstream

A source's `pins` are the places its version is pinned. When upstream releases, the
dependency update moves them — through the same engine, commit and merge-request handling as
every other dependency, honouring `dependency.max_update` and `dependency.min_release_age`:

```yaml
upstream:
  sources:
    core:
      git: https://github.com/ansible/ansible
      scheme: pep440
      pins:
        - { file: Dockerfile, arg: ANSIBLE_CORE_VERSION }   # ARG or ENV
        - { file: versions.yml, path: ansible.core }        # YAML or JSON dotted path
        - { file: apps.txt, marker: true }                  # the line after the marker comment
```

| Pin | Where the version is |
|---|---|
| `arg: NAME` | a Dockerfile `ARG` or `ENV` declaration |
| `path: a.b.0.c` | a scalar in a YAML or JSON file (map keys, sequence indexes) |
| `marker: true` | the line after a comment containing `stagefreight: upstream=<source id>`; `match:` picks the version (default: the first dotted version) |

- **One source, one version.** Every pin of a source must hold the same version. If they
  disagree, none of them moves and the run says which pin holds what.
- **Only the value changes.** Each pin is located to its exact line and column, and the edit
  replaces that value — quotes, comments and a `v` prefix stay as written.
- **A declared pin wins.** A dependency StageFreight would otherwise discover on the same line
  is left to the pin, so one value never has two writers.
- **Cooldown.** With `dependency.min_release_age`, a release is adopted only once it has been
  known for that long. Git tags carry no release time, so StageFreight records when it first
  saw each newer version in `.stagefreight/upstream.lock` (committed, deterministic) and counts
  from there; the bump that adopts a version also clears it from the lock. Without a cooldown
  there is no lock.

## Sync — keeping a fork on top of upstream

`sync` keeps a history fork current: on the default branch, StageFreight joins upstream's
newest release (or a branch head) into our branch and pushes the result, which starts an
ordinary pipeline on it.

```yaml
upstream:
  sources:
    zitadel: { git: https://github.com/zitadel/zitadel }
  version: { track: zitadel, ancestry: true }
  sync:
    from: zitadel
    onto: release            # default; or { branch: main }
    strategy: rebase         # default; merge | mirror
    shadow: upstream         # optional: a branch kept at upstream's synced commit
    on_conflict: mr          # default; fail | notify
```

| `strategy:` | Our branch afterwards |
|---|---|
| `rebase` | our commits replayed on top of upstream's release — rewritten, so the forge must let StageFreight force-push the branch |
| `merge` | a merge commit joining upstream's release; nothing rewritten. The merge commit carries `Upstream-Rev: <sha>` |
| `mirror` | exactly upstream's branch (fast-forward only); a branch that has diverged is kept and reported, never overwritten |

How a sync behaves:

- **Line-level merging, without guessing.** Each change is a three-way merge: changes to
  different lines of a file combine; changes that overlap (or touch) are a conflict unless
  both sides made the identical change. A patch upstream has already taken is dropped from a
  rebase. Replaying the same history onto the same release always produces the same commits.
- **Conflicts change nothing.** `on_conflict: mr` pushes upstream's commit to
  `stagefreight/sync/<source>/<release>` and opens a merge request into our branch listing the
  conflicting paths; `fail` fails the run; `notify` only signals. All three raise
  `sync-conflict`.
- **Never over someone else's push.** Our branch is pushed only if it still points at the
  commit the run started from; otherwise the next run syncs again.
- **A refused force push becomes a merge request** carrying the rebased branch, with what to
  do (allow StageFreight to force-push, or replace the branch with it).
- **A rebase needs straight history.** A branch with merge commits is reported; use
  `strategy: merge`.
- **The dependency update waits.** When a sync pushes, that run leaves the dependency update to
  the new commit's pipeline, so two writers never push onto a branch just rewritten.

With `version.track` on the same source and `release: auto`, the synced commit is released as
the newest upstream release it contains — no pins needed. `version.ancestry: true` then proves
that release on the tag pipeline.

## Release — cutting the tag

Under `track`, `release: auto` (the default) cuts the release itself: after a successful run on
the default branch, when the source's pins name a version upstream has released and no tag of
ours names it yet (or, for a synced fork, when the built commit contains an upstream release
we have not tagged), StageFreight tags the commit it just built with that version (keeping our
`v` convention) and pushes the tag. The release pipeline then checks every guard and evidence
probe like any hand-cut release. `release: manual` leaves tagging to a person
(`stagefreight tag`). A rebuild of an already-released version stays an in-place rebuild — no
new tag. The tag spawns a release pipeline only if it matches a declared `git.tags` release
pattern.

## Signals

What the watcher finds is raised as signals, which notifications select with
[`when.signals`](narration.md):

| Signal | Raised when |
|---|---|
| `updates-available` | the tracked source released a version newer than our newest |
| `version-mismatch` | one of our tags makes a false claim (track: never released; continue: already released), or a tolerated (`report`) guard or probe mismatch shipped |
| `upstream-unreachable` | a source could not be listed |
| `sync-conflict` | a sync could not land: upstream's changes overlap ours, a mirror diverged, or the push was refused |

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
