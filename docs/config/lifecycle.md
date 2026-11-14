# Lifecycle & Convergence

The single most architecturally significant choice: `lifecycle.mode` selects the phase graph
the pipeline runs — build container images (`image`, the default), validate GitOps manifests
(`gitops`), reconcile governance (`governance`), or drive the docker lifecycle (`docker`).
Each mode has its own config section. Audition is the same in every mode: the gates, then the
dependency update when `dependency:` is enabled.

Convergence subsystems COEXIST rather than compete: the perform phase runs every configured
reconciler — a gitops repo keeps `mode: gitops` and adds `opentofu:` and `ansible:` beside it,
and all of them converge in the same reconcile pass: infrastructure, then hosts, then clusters.

!!! example "Real examples"
    `dungeon` runs `mode: gitops`; `MaintenancePolicy` runs `mode: governance` — see
    [Getting Started](../getting-started.md).

--8<-- "docs/assets/modules/config-reference.md:lifecycle"

--8<-- "docs/assets/modules/config-reference.md:gitops"

--8<-- "docs/assets/modules/config-reference.md:governance"

--8<-- "docs/assets/modules/config-reference.md:docker"

--8<-- "docs/assets/modules/config-reference.md:ansible"

--8<-- "docs/assets/modules/config-reference.md:opentofu"

## Ansible host convergence

The `ansible:` subsystem converges **hosts** the way gitops converges clusters: a declared
playbook LIBRARY, executed in a pinned execution image, on every perform reconcile. Two
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

The image needs a Docker daemon to run in. On a runner without one (a rootless Kubernetes
runner), the plays run with the job's own `ansible-playbook` instead, and the key is held by a
private `ssh-agent` rather than written to disk — so that job's image must ship ansible, its
collections, `ssh-agent` and `ssh-add`. The results box names which runtime ran (`image` or
`native`). With neither a daemon nor ansible in the job, reconcile refuses before it starts.

**Trust posture.** The SSH key resolves from `<PREFIX>_SSH_KEY` (raw PEM) or
`<PREFIX>_SSH_KEY_B64` (base64 — the single-line form a masked variable can hold, mirroring
`<NAME>_CA_B64`); store it masked + **protected** so unprotected-ref pipelines never receive
it — they render "skipped — credentials not available", the signature of a correctly-protected
setup. Host-key verification is always strict against the repo-committed `ssh.known_hosts`
(`ssh-keyscan` the fleet once); host trust is auditable in git. The perform job carries a
serialization group (`resource_group` on GitLab) so two pipelines queue rather than race a
cordon/drain.

**Audition lint.** `lint.modules.ansible` runs ansible-lint where the plays run (the execution
image, or the job's own `ansible-lint` without a daemon) over exactly the declared plays —
undeclared ansible files stay quiet until they join the library. Severities are graduated
(blocker/critical → warning, rest informational): adopting the module never hard-fails an
audition on style rules. When ansible-lint can't run at all, the audition shows a warning
naming the unlinted plays and why.

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

## OpenTofu infrastructure convergence

The `opentofu:` subsystem converges **infrastructure**: a declared library of root modules,
run by OpenTofu or Terraform (`backend: opentofu | terraform`), on every perform reconcile.
The binary resolves from `toolchains.want.opentofu` or `toolchains.want.terraform` and is
verified against the vendor's published SHA256SUMS.

- **Converge roots** (`converge: true`) run in CI, in declared order, before ansible and
  gitops: `init`, `workspace select -or-create` when `workspace:` is set, `plan` to a saved
  file with each `var_files` entry, then `apply` of exactly that saved plan. A plan with no
  changes skips the apply. Any failed step fails the root; `required: false` makes it advisory.
- **Runbooks** (`converge: false`) only run from `stagefreight opentofu run <id>`
  (`--plan` for a plan without apply).
- `stagefreight reconcile --dry-run` and `opentofu run --plan` plan without applying.

**Credentials.** Every `<PREFIX>_<NAME>` variable reaches the binary as `<NAME>`
(`credentials: oci` turns `OCI_TF_VAR_tenancy_ocid` into `TF_VAR_tenancy_ocid`). Store them
masked and **protected**: unprotected-ref pipelines render "skipped — credentials not
available".

**Checkout stays clean.** Each root's `.terraform/` lives in a per-run directory, and `init`
runs with `-lockfile=readonly`: commit each root's `.terraform.lock.hcl`.

**Audition lint.** `lint.modules.opentofu` runs `fmt -check` over every `.tf`, `.tfvars` and
`.tftest.hcl` file and `validate` over each declared root (initialized without its state
backend). Validate errors are warnings, the rest informational, as with ansible.

**Facts.** Results record as the `opentofu` subsystem: `{opentofu.total}`,
`{opentofu.applied}`, `{opentofu.add}`, `{opentofu.change}`, `{opentofu.destroy}`,
`{opentofu.failed}`. See [`examples/opentofu.yml`](examples/opentofu.yml).
