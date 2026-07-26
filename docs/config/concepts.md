# Cross-Cutting Concepts

A handful of ideas show up in almost every section of `.stagefreight.yml` — template
variables, credential resolution, retention policies, and the pattern/condition syntax. They
are documented **once here** and referenced from the feature pages, so the behavior is
identical everywhere it appears.

---

## Template variables

Any text field that renders content — badge values, tag/alias templates, `text` items, link
URLs — expands these tokens. Expansion happens at run time against the resolved version and
git state.

| Template | Description |
|----------|-------------|
| `{version}` | Full semantic version (e.g. `1.2.3`) |
| `{major}`, `{minor}`, `{patch}` | Semver components |
| `{base}` | Base version without pre-release/build metadata |
| `{sha}`, `{sha:N}` | Commit SHA (default 7 chars, or `N`) |
| `{branch}` | Current branch name |
| `{var:name}` | User-defined variable from the top-level `vars:` map |
| `{env:VAR}` | Environment variable value |
| `{date}`, `{datetime}`, `{timestamp}` | UTC date formats |
| `{date:FORMAT}` | Custom Go time layout |
| `{commit.date}` | HEAD commit date |
| `{project.name}` | Repo name from the git remote |
| `{project.url}` | Repo URL (SSH→HTTPS normalized) |
| `{project.license}` | SPDX identifier from the `LICENSE` file |
| `{docker.pulls}`, `{docker.stars}` | Docker Hub stats |

---

## Credential resolution

Credential fields (e.g. a registry or release target's `credentials:`) never hold a secret.
They name an **environment-variable prefix**; StageFreight resolves the real value from your
CI/CD variables at run time, so nothing sensitive lives in `.stagefreight.yml`.

For a prefix (e.g. `HARBOR`), the username is always `{PREFIX}_USER`, and the secret is the
first non-empty of these suffixes:

| Suffix | Example | Notes |
|--------|---------|-------|
| `_TOKEN` | `HARBOR_TOKEN` | **Preferred.** Scoped, revocable API token. |
| `_PASS` | `HARBOR_PASS` | Accepted; emits a warning recommending `_TOKEN`. |
| `_PASSWORD` | `HARBOR_PASSWORD` | Accepted; emits a warning recommending `_TOKEN`. |

At the protocol level all three are identical — they become the `--password-stdin` value for
`docker login`. The distinction is on the *issuing* side: a password authenticates the
account directly, while a token is issued separately, can be scoped (push-only, no delete),
revoked individually, and attributed in audit logs.

!!! tip "Recommendation"
    Create a robot account or scoped API token with the minimum permissions needed (usually:
    push to one project), store it as `{PREFIX}_TOKEN`, and keep the account password out of
    CI entirely. The `_PASS`/`_PASSWORD` warning is based purely on the matched suffix —
    StageFreight cannot tell a password from a token by value, so rename the variable to
    `_TOKEN` to silence it.

```yaml
publish:
  harbor-dev:
    kind: registry
    build: myapp
    url: cr.example.com
    provider: harbor
    path: myorg/myapp
    tags: ["dev-{sha:8}", "latest-dev"]
    when: { branches: [main], events: [push] }
    credentials: HARBOR        # → HARBOR_USER + HARBOR_TOKEN
```

---

## Retention policies

Registry and release targets accept a `retention:` policy that prunes old tags/releases.
Policies are **additive**, restic-style: a tag survives if **any** rule wants to keep it.

```yaml
# Shorthand — keep the last 10 (per series; see below)
retention: 10

# Full policy
retention:
  keep_last: 3
  keep_daily: 7
  keep_weekly: 4
  keep_monthly: 6
  keep_yearly: 2
  keep_branches: 10            # keep the 10 most-recently-active branches' series
  protect: ["latest"]          # never deleted, regardless of the policies
```

Which remote tags are *candidates* is derived from the target's `tags:` templates —
StageFreight only ever prunes tags it recognizes as its own.

### Retention is per-series, not per-target

Retention evaluates **each series independently**, not across all of a target's tags at
once. A tag's series is `(template, identity)`: its `tags:` template plus the values of
that template's **identity variables** (`{branch}`, `{env}`). `keep_last` and the time
buckets apply *within* each series:

- **Two accumulating templates never share slots.** `tags: ["dev-{sha:8}", "nightly-{sha:8}"]`
  with `keep_last: 6` keeps 6 dev **and** 6 nightly — one never evicts the other.
- **Per-branch series prune independently.** `tags: ["test-{branch}-{sha:8}"]` keeps
  `keep_last` builds for *each* branch — a busy branch never prunes a quiet one's history.
- **Rolling tags are auto-exempt.** A template with no sequence variable (`latest-dev`,
  `latest-test-{branch}`) is a single value overwritten in place — nothing accumulates, so
  it is never pruned. You do **not** need `protect: ["latest-dev"]`.

### `keep_branches` — bound the number of series

`keep_branches: N` keeps only the **N most-recently-active** identity groups per template
(e.g. the 10 most-recent branches) and prunes the rest wholesale. This is what stops
retired branches' tags from accumulating forever. Templates with no identity variable are
unaffected (there is only one series).

### `∞` and wipe semantics

A keep value of `0`, `-1`, or unset all mean **∞ (keep everything)** for that rule — the
Go zero-value is safe, and an all-∞ policy is a graceful no-op. There is **no** value that
wipes a repo; retention never mass-deletes. (Removing all tags of a series is a deliberate,
out-of-band action, never a policy number.)

### `identity:` *(advanced)*

By default `{branch}` and `{env}` partition tags into independent series. If you tag by
another partition dimension — e.g. `img-{region}-{sha:8}` across `us`/`eu`/`asia` — list it
so each region prunes independently instead of competing for the same slots:

```yaml
retention:
  keep_last: 6
  identity: [region]           # region is a partition dimension, not a sequence
```

Most projects never need this — `{branch}`/`{env}` cover the common cases.

---

## Patterns & conditions

The same matching syntax drives `when.branches`, `when.git_tags`, and every `tag:`/`branch:`
conditional rule (security detail rules, and so on).

```yaml
"^main$"              # regex match (the default)
"!^feature/.*"        # negated regex (! prefix)
"main"                # literal match
"!develop"            # negated literal
```

- An **empty** list/field is no filter — it always matches.
- Multiple patterns are evaluated in order; **first match wins**.
- Where a rule has multiple condition fields (e.g. `tag:` **and** `branch:`), **all** set
  fields must match (AND). A rule with no fields set is a catch-all.

Named routing patterns (referenced by `when.git_tags: [stable]` / `when.branches: [main]`) are
defined once under `git.tags` / `git.branches` and reused across publish targets — see
[Policy](policy.md).
