# Workflow

`workflow:` declares how code travels into a repo: which branches exist, how a change lands on
each, how it moves between them, and where release tags are cut. StageFreight enforces what you
declare and imposes no branching strategy of its own. Without a `workflow:` block nothing about
branches is governed.

```yaml
git:
  branches:
    main: ^main$
    feature: ^feature/
    release: ^release-\d+\.\d+$

workflow:
  branches:
    main:
      match: main
      landing: merge_request
      review: { approvals: 1, author_cannot_approve: true }
      checks: { required: [audition, perform, review], up_to_date: true }
      merge: squash
    feature:
      match: feature
      lifetime: short
      max_age: 14d
    release:
      match: release
      lifetime: on_demand
      created_from: tag
      landing: merge_request
  promote:
    land:     { from: feature, to: main, method: merge_request }
    backport: { from: main, to: release, method: cherry_pick }
  release:
    from:
      main:    [prerelease, major, minor, patch]
      release: [prerelease, patch]
```

That is trunk-based development: `main` is always releasable and takes changes through merge
requests, feature branches are short-lived, and a release line is cut from a tag only when an
older version needs a patch. Fixes land on `main` first and are cherry-picked back.

## Branch classes

Each class names a [`git.branches`](policy.md) matcher; a branch belongs to the first class
whose matcher selects it, and a branch no class selects is ungoverned.

| Field | Values |
|---|---|
| `match` | the `git.branches` matcher (required) |
| `lifetime` | `long` (default) · `short` · `on_demand` |
| `max_age` | short-lived only: how long a branch may live, e.g. `14d` |
| `created_from` | on-demand only: the class it is cut from, or `tag` |
| `landing` | `direct` (pushed to) · `merge_request` (a pull request on GitHub) |
| `enforcement` | `active` (default) · `evaluate` (report, never refuse) · `off` |
| `review` · `checks` · `merge` · `queue` · `protect` · `fork_changes` · `bypass` | merge-request and push rules for the forge |

`checks.required` names the phases a merge waits on: `audition`, `perform`, `review`. Every
`bypass` entry needs a `reason`, because each one weakens the rules.

## Promotions and releases

A promotion moves a change between two classes by `merge_request`, `fast_forward`,
`cherry_pick` or `tag`. A promotion with a `schedule:` (a [schedules](schedules.md) id) runs
on that cadence instead of per change — a release train.

`stagefreight promote [<promotion>]` carries the current branch's work along a promotion: the
one out of its class, or the one named. `merge_request` pushes the branch and opens a merge
request into the target branch, listing the commits it carries; `fast_forward` advances the
target branch to your HEAD. The target is the default branch when it belongs to the target
class; otherwise name it with `--to`. `cherry_pick` and `tag` promotions are carried out by
hand for now.

`release.from` lists, per class, the kinds of release tag its branches may carry: `prerelease`,
`major`, `minor`, `patch`. The kind comes from the version: `v1.0.0` is major, `v0.16.0`
minor, `v0.15.1` patch, `v0.16.0-rc.1` a prerelease.

Other models are the same fields arranged differently:

| Model | Shape |
|---|---|
| GitHub flow | `main` + short-lived branches, no release lines |
| Gitflow | long-lived `develop` and `main`; releases and hotfixes merge into both; tags only on `main` |
| Release train | `master → beta → stable` by `fast_forward` on a schedule; tags only on `stable` |
| Environment branches | `main → pre-production → production` by `fast_forward` |

## Role Mapping

#### Who's this "Talent"?
*It's probably you.* 😜

| Role | May | GitLab | GitHub | Gitea · Forgejo |
|---|---|---|---|---|
| **Producer** | cut releases · override the workflow | Owner | Admin | Owner · admin collaborator |
| **Director** | push straight to a long-lived branch · orchestrate promotion | Maintainer | Maintain | — |
| **Talent** | push working branches · open merge requests | Developer | Write | Write |
| **Audience** | watch · comment · triage; no push | Reporter · Guest | Triage · Read | Read |

Your access on the forge sets the highest role you can act as. Everyone, person or agent,
acts as **talent** by default; `--role <role>` on `commit`, `push` and `tag` acts as another,
never above what your access grants. Reaching for a higher role is therefore a deliberate act,
and an agent working as talent can't land on a protected branch or cut a release. When your
access can't be read (offline, no forge token), `--role` is unbounded and StageFreight says
so; the forge still enforces for real when you push.

## What StageFreight enforces

On your machine, `stagefreight commit`, `push` and `tag` hold you to the declared workflow
before anything leaves it:

- A commit made on, or pushed to, a `landing: merge_request` branch is refused, with the branch
  to work on instead.
- A push needs at least talent, and a push straight to a long-lived branch that takes direct
  pushes needs a director.
- A release tag needs a producer, on a commit an allowed class contains.
- A producer's `--maintainer-override` proceeds anyway and says so in the output. The forge
  still enforces its own rules.
- Under `enforcement: evaluate` the landing and release findings are reported and nothing is
  refused.

In CI the local check stands aside: the forge and the pipeline are the authority there.
`review`, `checks`, `merge`, `queue`, `protect`, `fork_changes` and `bypass` are validated
today and applied to the forge's branch protection as that support lands.

--8<-- "docs/assets/modules/config-reference.md:workflow"
