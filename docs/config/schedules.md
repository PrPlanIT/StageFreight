# Schedules

`schedules:` declares the cadences on which StageFreight runs by itself — with nobody pushing.
A scheduled run watches upstreams, bumps dependencies and syncs forks; whatever it commits
starts an ordinary pipeline that builds and publishes it under every guard. That is how forks
and quiet projects stay current while everyone sleeps.

```yaml
schedules:
  nightly:        { cron: 'H 4 * * *', reason: upstream watch + deps }
  weekly-rebuild: { cron: 'H H * * 1', build: always, reason: base image refresh }
```

## What a scheduled run builds

| `build:` | The scheduled run itself |
|---|---|
| `changes` (default) | builds nothing. Its changes — a bump, a sync — are built and published by the pipeline their push starts, so a night with nothing new publishes nothing and mints no new digests. |
| `always` | rebuilds and republishes the current commit — a base-image refresh — like a push would. Narrow which targets publish with `when.schedules`. |

A run fired by a schedule StageFreight did not declare (one made by hand in the forge) builds,
as before.

## The cron contract

One expression has to mean the same thing on every forge, so the grammar is the portable
floor they all honour:

- **5 fields, UTC:** minute hour day-of-month month day-of-week.
- **At most hourly:** the minute is a single value.
- **Plain cron only:** `*`, numbers, ranges `a-b`, steps `/n`, lists `a,b`. No names
  (`MON`), no `@daily`.
- **`H`** in the minute or hour field is a value hashed from the repo and the schedule id.
  It is stable — the same repo always renders the same time — and spreads a fleet sharing
  one cadence across the hour instead of firing every repo at `:00`.

Schedules that resolve to the same cron fire one run carrying all of their ids.

## How each forge gets them

| Forge | Where the schedule lives |
|---|---|
| GitHub · Gitea · Forgejo | `on: schedule` in the rendered workflow (`stagefreight ci render`) |
| Azure DevOps | `schedules:` in the rendered pipeline, on the primary repo's default branch, `always: true` |
| GitLab | pipeline schedules, reconciled through the API on every default-branch run |

Rendered forges pick schedules up the way they pick up any CI change; governance re-renders
every satellite, so a fleet-wide cadence is one catalog edit.

On GitLab, StageFreight manages only the schedules whose description starts with
`stagefreight:`; anyone else's are never touched. It creates, updates and prunes its own,
runs them on the default branch in UTC, and makes the forge token's user their owner — a
GitLab schedule runs as its owner and stops when that user loses access, so a project access
token (a bot that belongs to the project) is the right identity. It uses the same credentials
as every other forge mutation (`GITLAB_TOKEN`); a token that cannot manage schedules is a
warning plus the `schedule-inactive` signal, never a failed pipeline.

## Which schedule fired

Every scheduled run carries the ids that fired it (`SF_CI_SCHEDULE`). `when.schedules`
selects work by id, through the same `when:` grammar as everything else:

```yaml
publish:
  base-refresh:
    kind: registry
    when:
      schedules: [weekly-rebuild]
```

`events: [schedule]` matches any scheduled run; `schedules:` names which.

## Signals

| Signal | Raised when |
|---|---|
| `schedule-inactive` | a declared schedule was found deactivated on the forge (and reactivated), or the forge's schedules could not be managed |

--8<-- "docs/assets/modules/config-reference.md:schedules"
