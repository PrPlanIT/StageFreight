# StageFreight GitOps Stack Audit — Freshness & Vulnerability

> **Status: living design document.** The durable home for how StageFreight audits the *running
> posture* of a GitOps-managed stack — image freshness/liveness and vulnerability exposure — from
> the repository as source of truth. Iterate it *here*. Not yet implemented; slices below.

> **Scope: the deployed image set of a GitOps repo.** This audits *what a stack is running and how
> exposed it is* — distinct from the [gitops-fluxcd-validation](gitops-fluxcd-validation.md) doc
> (which validates that manifests are *correct/reconcilable*). Freshness answers "is this rotting or
> abandoned"; vuln answers "how exposed am I." Where either needs to *gate* a pipeline/merge it hands
> off to the deferred `audition-proofs` framework as one input among many.

> **Conceptual model: settled (this session).** The unit is the **effective deployed image**
> (kustomize-resolved, past base/overlay split-pins). Freshness is measured in **publish time, never
> tag semantics**. Output is per-image evidence consumed by a control plane (dd-ui). **Implementation:
> not started** (Slices 1–3 below).

## Actors (same discipline as gitops validation)

1. **Git** — declares the desired stack. **Source of truth.**
2. **Flux** — custodian of source; converges cluster to Git.
3. **The registry / upstream** — the external reality we measure *against*.
4. **StageFreight** — Actor #4: reads Git's effective image set, measures it against upstream reality,
   and emits evidence. It does **not** mutate the stack; it reports. Remediation (open an MR to bump)
   is a separate, later authority, reusing the forge machinery.

Because Flux is custodian, **repo-mode is authoritative**: the kustomize-resolved image set *is* the
running stack (modulo drift). Cluster-mode (live `kubectl` enumeration) is a later enhancement that
detects drift between declared and actual — an overlay on this, not a replacement.

## The core insight: time, not tags

A tag name carries no staleness signal. `v2.5.6` can be two years dead; `:nightly` can be from this
morning — or six months old. The audit is **tag-blind** and computes two independent time axes per
image from registry publish timestamps:

- **`upstream_idle`** = `now − (upstream's most recent publish of ANY tag)`.
  Answers *"is development even ongoing?"* Large ⇒ the project is cold; no tag scheme would help.
- **`behind_gap`** = `(upstream's latest publish) − (our running image's publish)`.
  Answers *"are we falling behind an active project?"*

Crossing them yields the only verdicts that matter:

| upstream active? | we're behind? | verdict | action |
|:---:|:---:|:--|:--|
| yes | no | ✓ **current** | none |
| yes | yes | ⛔ **rotting** | bump — Renovate usually catches these |
| **no** | — | 🧟 **cold** | abandonment risk — *migrate*, not bump |
| yes | frozen `@digest` | 🔒 **frozen** | drift invisible to Renovate — re-pin/track digest |
| — | — (no timestamp) | ⚠ **undatable** | authed/manual check; never a silent pass |

The value StageFreight adds over Renovate is the **cold** and **frozen** columns — the silent-rot
classes Renovate structurally cannot surface (it reports a frozen `:latest@digest` and an
abandoned-`:2`-line as "up to date"). In the dungeon sample, 6 of 9 flagged images fell in those two
columns — i.e., Renovate would *never* have raised them.

## Presentation (paper mock — repo-resolved, illustrative dates)

```
── Stack Freshness ─────────────────────────────── gitops: dungeon @ 463e1b3c ──
   source: repo (Flux-authoritative) · 147 images resolved from kustomize overlays
   metric: publish time, not tag  ·  thresholds: behind>180d · upstream-cold>540d

  ⛔ ROTTING — upstream active, we are behind                                 3
     minio/minio:RELEASE.2025-09-07…   ours 298d │ upstream 12d → 286d behind
     photoprism/photoprism:251130      ours 214d │ upstream 17d → 197d behind
     mealie:nightly                    ours 179d │ nightly pushed 1d → 6mo-old "nightly"
  🧟 UPSTREAM COLD — no publish in >540d — is dev ongoing?                    3
     organizr:latest@sha256…           upstream last push 973d → dead ~2.7y
     requarks/wiki:2                   :2 line last push ~23mo → 2.x EOL; v3 = migration
     mpolden/echoip:latest@sha256…     upstream ~4y → dead-but-stable
  🔒 FROZEN DIGEST — :latest@sha, latest moved on (Renovate-blind)           3
     guacamole/guacamole               digest 2024-02 │ latest 2026-06 → 16mo drift
  ⚠ UNDATABLE — registry gave no timestamp                                   2
  ✓ CURRENT — within 180d of upstream                                      136

   9 of 147 need eyes; 6 Renovate will NEVER surface (cold + frozen).
```

## Reuse (why this is glue, not a rebuild)

- **Image resolution** — reuse the fluxcd YAML parsing from the gitops validator to produce the
  kustomize-resolved effective image set (real deployed tag per app, not the base split-pin).
- **Freshness probe** — a clean, domain-neutral `registry` read component: `image ref → published-at
  of our tag/digest` + `published-at of upstream's newest`. Partially exists in
  `src/lint/modules/freshness/http.go`; keep it as its own component, *not* piled onto the
  refactor-damaged gomod/Dockerfile resolvers.
- **Vulnerability batch** — reuse the scan engines already wired in `src/toolchain` (trivy, grype,
  syft, osv-scanner) and the `security` command; generalize from "the one image we built" to a
  **batch over the resolved image set**.
- **Evidence + surface** — emit per-image evidence via the audition-proof/evidence pattern; dd-ui
  reads it as a control plane (it does not import StageFreight packages — the boundary is the
  evidence artifact).

## Slices (each CI-dogfoodable; no local Go toolchain → verify via `latest-dev`)

- **Slice 1 — freshness scan.** `registry` timestamp component + gitops effective-image resolver →
  per-image freshness evidence with the two-axis verdict. *Delivers the dungeon rot answer as a real
  feature.*
- **Slice 2 — batch vulnerability.** Generalize the wired trivy/grype/osv path to the resolved image
  set → per-image CVE-by-severity evidence.
- **Slice 3 — fuse into a stack-health audit.** One artifact = freshness verdict + vuln posture per
  image → "how bad off is my stack." Optional audition-proof gating (via the deferred framework).

## Open questions

- Registry timestamp coverage: Docker Hub (`hub.docker.com/v2`) and Quay have clean per-tag dates;
  ghcr/OCI need token + config-blob `created`; authed/private registries (nvcr, self-managed GitLab)
  may be **undatable** without creds → first-class `undatable` verdict, never a silent pass.
- Frozen-digest dating: our digest's publish time needs a digest→date lookup (match digest in the tag
  list, or the manifest config `created`).
- Remediation authority (open a bump MR for `rotting`/`frozen`) — deferred; reuses forge machinery,
  and must not double-manage with Renovate (own the ecosystem or defer to it, never both).
