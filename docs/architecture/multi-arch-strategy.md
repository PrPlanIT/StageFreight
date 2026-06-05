# Multi-Arch Strategy — Binaries Now, Multi-Arch Crucible Deferred

> Status: **decision recorded / multi-arch crucible strong-deferred.** Promoted from a working
> plan file so the deferral rationale and observable re-entry triggers survive. The "already
> shipped" items below landed in commit `7440825`. Relates to
> [signing-trust-model.md](signing-trust-model.md).

## Context

StageFreight should gain arm64 reach and dogfood its own binary-archive feature, **without
weakening the crucible reproducibility assurance** that is becoming central to its trust story
(signed releases / signed checksums / publication attestations). Three distinct goals were
separated and they do **not** require the same solution:

1. **Distribution reach** — can users run it on arm64? → satisfied by arm64 *binaries*.
2. **Dogfooding** — does StageFreight exercise its own binary-archive / checksum / release-asset
   machinery? → satisfied by shipping binaries.
3. **Reproducibility assurance** — can we prove what was built? → provided by crucible; *more*
   strategically important than an arm64 container image.

**Decision:** ship multi-arch **binaries** + an amd64 **image** now; **do not** make the docker
image multi-arch now (it conflicts with the current single-arch crucible); **strong-defer**
**multi-arch crucible** as a scoped backlog item (per-arch reproducible + manifest), revisited under
a *dual* trigger — arm64-image demand **or** a trust-model dogfooding milestone (see the
strong-deferral section). This is a sequencing/platform-maturity decision: single-artifact trust
correctness first, multi-artifact composition after.

## Verified findings (traced, not speculative)

- **Perform's published-image build routes through crucible.** `buildRunner` (`ci_runners.go:66`) →
  `docker.Run` (`run.go:35`: `if resolveBuildMode(req) == "crucible" { return runCrucibleMode(req) }`)
  → `runCrucibleMode`. So the published image IS crucible-built for this config.
- **Current crucible is single-arch by construction.** `crucible.go:183` unconditionally overrides
  `builds[i].Platforms = []string{"linux/" + runtime.GOARCH}`. The two-pass build → rebuild →
  verify-identical-digest runs for one native arch; there is **no** manifest-list assembly. So
  `platforms: [linux/amd64, linux/arm64]` on the crucible docker build is silently ignored.
- **Binaries do not touch crucible.** `build/gobuild.go` drives `GOOS`/`GOARCH` cross-compile
  (proven: a linux/arm64 binary built locally in seconds, no emulation).
- Correction to an earlier overreach: reproducibility and multi-arch are **not** fundamentally at
  odds. The *current implementation* is single-arch; reproducible multi-arch is additive (below),
  not a reason to abandon crucible.

## Already shipped (committed `7440825`, on main)

- `kind: binary` build `stagefreight-bin` — `builder: go`, `from: ./src/cli`, `-tags banner_art`,
  `CGO_ENABLED=0`, `platforms: [linux/amd64, linux/arm64]`.
- `kind: binary-archive` target `stagefreight-binaries` — `format: auto` (tar.gz), `checksums: true`
  (→ `SHA256SUMS`).
- Attached to the **GitLab** release (`primary-release` gains `archives: stagefreight-binaries`) and
  a new **GitHub** release (`github-release`, `mirror: github-mirror`, `archives: …`).
- Docker image **stays `linux/amd64`** (crucible intact). The exploratory multi-arch docker edit was
  reverted. **No further config change is needed for this decision.**

## Deferred (STRONG deferral) — backlog: multi-arch crucible

This is a **sequencing decision, not avoidance**: finish the single-artifact trust story before
extending the trust system to *multi-artifact composition*. It is a **platform-maturity** call
("do we extend our trust model to cover multi-arch reproducibility now?"), not a feature one
(arm64 reach is already solved by binaries). Doing it now would force debugging multi-arch
correctness + signing model + publish invariants simultaneously — where systems get fragile.

**Why this is the real boundary:** #6 is the **phase transition where StageFreight stops being a
build system and becomes a distributed verification system** — extending the verification model from
*single-artifact correctness* to *composed-artifact correctness* (manifest = artifact, per-arch
verify, aggregation invariants, cross-arch consistency). That shift is non-linear: every build
becomes multi-node reasoning, every artifact a composed object, every signature dependent on
aggregation correctness. The base layer (single-artifact truth) must be production-stable *before*
generalizing truth composition — otherwise you build a trust system that is correct in theory but
unstable in practice.

**Roadmap position (do these in order; #6 before #7):**
1. Binary archives ✓ 2. SHA256SUMS ✓ 3. Signed checksums 4. Release-verification UX
5. Provenance / attestations 6. **Multi-arch crucible** 7. Multi-arch published image.

**Triggers (revisit when EITHER fires — both stated OBSERVABLY so #6 can't drift into "someday"):**
- **User-demand trigger (observable):** a concrete request to *run the StageFreight container* on
  arm64 — a tracked issue / user ask for RPi, ARM servers, Apple-Silicon linux VMs, or ARM k8s nodes.
  Binaries already cover arm64 *execution*, so this is the weaker leg.
- **Trust-model-milestone trigger (made observable, not a vibe):** fires when ALL hold —
  (a) roadmap #3–#5 are **shipped and production-stable** — signed checksums verify on real releases,
  release-verification UX exists, provenance/attestations publish *and* verify;
  (b) single-arch crucible has passed its reproducibility verdict across **≥ N consecutive stable
  releases with zero crucible failures** (pick N at the time — base layer proven in production, not
  just in theory);
  (c) the *declared* next dogfooding goal becomes "prove **every** artifact class — including the
  composed/multi-arch image — is reproducibly built and verified."
  When (a)+(b)+(c) are all true, #6 is the next scheduled phase transition, independent of any user
  ask. Until then it stays parked — but parked against checkable conditions, not sentiment.

**Reproducibility-preserving design (NOT "drop crucible"):**
- `runCrucibleMode` (`crucible.go`): replace the `:183` single-arch override with a **per-platform
  loop** — two-pass build→rebuild→verify *independently per arch*, storing per-arch digests + provenance.
- Build strategy: single-arch `--load`+push → per-arch **OCI layout / `--push`** (multi-arch can't
  `--load`; `plan.go:76-109` already encodes the distinction).
- **Manifest-as-artifact shift:** assemble an OCI manifest list from the verified per-arch digests;
  the published artifact becomes the *manifest* (images = components), which it then signs. New
  invariant: "this multi-arch *set* is reproducible," layered on per-arch reproducibility.
- **arm64 build strategy is the cost driver** (decide at implementation time): QEMU/binfmt (simplest,
  slow, can be flaky for reproducibility) vs. remote native builders (cleanest correctness, more
  infra). "Skip arm64 verification" is unacceptable — it defeats the point.

**Honest effort:** ~3–10 engineering days depending on the build-strategy choice and infra maturity
— more than a config change, less than a redesign. The real cost isn't LOC; it's **multiplying what
crucible must prove** (N per-arch loops + aggregation + manifest correctness) → larger failure
surface, more CI time, distributed-build edge cases. That cost is precisely why it sequences *after*
single-artifact trust is solid.

## Verification (of what already shipped — first stable tag after `7440825`)

- Binary build runs `dist/linux-amd64/stagefreight` + `dist/linux-arm64/stagefreight`; archives as
  `stagefreight-{version}-{os}-{arch}.tar.gz`; generates `SHA256SUMS`.
- Archives + `SHA256SUMS` attach to the **GitLab** release.
- **Watch the GitHub release (riskiest first-run unknown):** the `github-release` target uploads
  *assets* to GitHub, which needs a working **`GITHUB` CI token with release scope**. The mirror
  previously only *projected notes* (no assets), so this is the first real asset upload to GitHub —
  if the token is missing/under-scoped, that target fails (isolated to the github-release step).
- The `SHA256SUMS` produced here is exactly the artifact the signing plan's Commit 2 would sign
  (see [signing-trust-model.md](signing-trust-model.md)).
