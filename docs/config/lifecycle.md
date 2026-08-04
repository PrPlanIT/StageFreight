# Lifecycle & Convergence

The single most architecturally significant choice: `lifecycle.mode` selects the phase graph
the pipeline runs — build container images (`image`, the default), validate GitOps manifests
(`gitops`), reconcile governance (`governance`), or drive the docker lifecycle (`docker`).
Each mode has its own config section.

Convergence subsystems COEXIST rather than compete: the perform phase runs every configured
reconciler — a gitops repo keeps `mode: gitops` and adds `ansible:` beside it, and both
converge in the same reconcile pass. Future host/infra backends join this page the same way.

!!! example "Real examples"
    `dungeon` runs `mode: gitops`; `MaintenancePolicy` runs `mode: governance` — see
    [Quick Start](../quickstart.md).

--8<-- "docs/assets/modules/config-reference.md:lifecycle"

--8<-- "docs/assets/modules/config-reference.md:gitops"

--8<-- "docs/assets/modules/config-reference.md:governance"

--8<-- "docs/assets/modules/config-reference.md:docker"

--8<-- "docs/assets/modules/config-reference.md:ansible"

## Ansible host convergence

The `ansible:` subsystem converges **hosts** the way gitops converges clusters: a declared
playbook LIBRARY, executed inside a pinned execution image, on every perform reconcile. Two
lanes, one backend:

- **Converge plays** (`converge: true`) run in CI, in declared order, fail-loud — any failed
  or unreachable host fails the phase; no silent partial converge. Idempotency lives in the
  playbook (per-host "already at the pin → skip"), so every run is safe and steady-state is a
  fast no-op.
- **Runbooks** (`converge: false`) are declared but structurally unreachable from CI — they
  get lint, the pinned image, and `--check` preview, and only execute when a human runs
  `stagefreight ansible run <id>` (`--plan` for preview, `-e key=val` for launch-time vars).

**Execution image.** Playbooks and ansible-lint run inside `ansible.image` — the image owns
the ansible runtime, collections, and connection deps (pywinrm for Windows nodes). Official
default: [`docker.io/hlhd/ansible`](https://hub.docker.com/r/hlhd/ansible); bring your own by
pointing `image:` elsewhere. Pin a version tag — updates ride the deps engine's docker-image
tag-line semantics.

**Trust posture.** The SSH key resolves from `<PREFIX>_SSH_KEY` (prefix from
`ssh.credentials`); store it masked + **protected** so unprotected-ref pipelines never receive
it — they render "skipped — credentials not available", the signature of a correctly-protected
setup. Host-key verification is always strict against the repo-committed `ssh.known_hosts`
(`ssh-keyscan` the fleet once); host trust is auditable in git. The perform job carries a
serialization group (`resource_group` on GitLab) so two pipelines queue rather than race a
cordon/drain.

**Audition lint.** `lint.modules.ansible` runs ansible-lint from the same execution image over
exactly the declared plays — undeclared ansible files stay quiet until they join the library.
Severities are graduated (blocker/critical → warning, rest informational): adopting the module
never hard-fails an audition on style rules.

**Facts.** Converge results record as the `ansible` subsystem: `{ansible.converged}`,
`{ansible.total}`, `{ansible.changed}`, `{ansible.unreachable}` — and the shipped summary
carries `Converged {ansible.converged}/{ansible.total} nodes · {ansible.changed} changed`,
eliding on runs without ansible, exactly like the gitops reconcile line. Failures narrate via
the standard failure facts with no extra wiring.

**Drift cadence.** StageFreight converges when pipelines run; add a forge scheduled pipeline
(e.g. weekly) as drift insurance — steady-state converge is a fast no-op.

**Testing.** Molecule needs no dedicated machinery — run it as a `tool: script` test suite
from the execution image. See [`examples/ansible.yml`](examples/ansible.yml) for the complete
annotated example.
