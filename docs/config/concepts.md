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
targets:
  - id: harbor-dev
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
# Shorthand — keep the last 10
retention: 10

# Full policy
retention:
  keep_last: 3
  keep_daily: 7
  keep_weekly: 4
  keep_monthly: 6
  keep_yearly: 2
  protect: ["latest"]          # never deleted, regardless of the policies
```

Which remote tags are *candidates* for retention is derived from the target's `tags:`
patterns — StageFreight only ever prunes tags it recognizes as its own.

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

Named routing patterns (referenced by `when.git_tags: [stable]`) are defined once under
`matchers:` and reused across targets — see [Policy](policy.md).
