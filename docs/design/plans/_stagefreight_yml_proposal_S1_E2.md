# .stagefreight.yml schema refactor — S1_E2 working doc

One grammar for the whole config: **identity keys declare what exists to
connect to** (`forges`, `repos`, `registries`, `infrastructure`), **phase keys
declare typed work units** (`perform`, `publish`), and **the repo tree is the
only catalogue** — config expresses *use*, never re-lists what git already
declares.

## Founding principle this restores: StageFreight has no modes

StageFreight encounters **declarations of intent and executes them**. A
"mode" is an admission the intent model is insufficient — the moment the
tool can say *"that intent doesn't fit the mode,"* the language is
self-breaking: it blames the speaker for the vocabulary's gaps.
`lifecycle.mode` was that admission. In this shape, gitops is not a mode —
it is a mindset expressed as a perform execution kind, and every behavior in
every other job activates from the **existence of declared intent** (a
`fluxcd` unit exists → flux validation exists; a `build-oci` unit exists →
image scanning exists), never from an enum.

On expressiveness: Turing completeness is **not a roadmap destination** —
no feature is ever added *because* it makes config programmable. But it is
not a ceiling either: if the intent model becomes so complete that
computational completeness emerges as a side-effect, that is the model
succeeding, and we bask accordingly. The fence is on purpose, not on power.

---

## Initial sketch (this round's starting point — preserved verbatim)

> The first-draft config that seeded this round of schema proposals. Kept
> as-written; everything below refines it.

```yaml
infrastructure:
    prod:
        kind: ansible-inventory
        auth:
            method: ssh # alt pass w/ auth.pass auth.sudopass or similar
            user: kai
            credentials: DUNGEON              # → DUNGEON_SSH_KEY (PROTECTED forge variable)
        known_hosts: ansible/known_hosts  # committed fleet host keys — verification is always strict
        file: ansible/inventory


    dungeon:
        kind: k8s-cluster
        api: https://172.22.144.105:6443
        auth:
            method: oidc
            audience: stagefreight
        exposure:
            rules:
            - level: internet
                gateways: [phloem-gateway, cell-membrane-gateway]
            - level: intranet
                gateways: [xylem-gateway]
            - level: cluster
                service_types: [ClusterIP]
            - level: intranet
                service_types: [NodePort]




perform:
  stagefreight-oci:
    kind: build-oci
    build_mode: crucible
    platforms: [linux/amd64]


  stagefreight-bin:
    kind: build-bin
    builder: go
    from: ./src/cli
    output: stagefreight
    env: { CGO_ENABLED: "0" }
    # No -tags banner_art: the fancy ASCII banner needs a generated, gitignored
    # file (go generate ./src/output) that the Dockerfile produces but the binary
    # build can't. The stub gives a text banner. Version is embedded via ldflags so
    # downloaded binaries report the real version (not "dev").
    args:
      - "-ldflags"
      - "-s -w -X github.com/PrPlanIT/StageFreight/src/version.Version={version} -X github.com/PrPlanIT/StageFreight/src/version.Commit={sha} -X github.com/PrPlanIT/StageFreight/src/version.BuildDate={date}"
    platforms: [linux/amd64, linux/arm64]

  reference:
    kind: command
    stage:
      from: stagefreight-bin
      as: stagefreight
    command: [./stagefreight, docs, generate, --output-dir, "{output}"]
    outputs:
      - { type: tree, source: docs/assets/modules, worktree: true }


  docs-site:
    kind: command
    image: docker.io/library/python:3.12-slim
    command: "pip install --quiet --root-user-action=ignore mkdocs-material && mkdocs build -f docs/assets/mkdocs/mkdocs.yml --strict --site-dir $(pwd)/site"
    outputs:
      - { type: tree, source: site }

  smoke:
    kind: bash
    image: docker.io/library/python:3.12-slim
    script:
        - |
        "Whatever"

  dungeon:
    kind: fluxcd
    target: dungeon

  dungeon-provision:
    kind: ansible
    image: docker.io/hlhd/ansible:2.20.4-v1
    target: prod
    playbook:
        - ansible/k8s/provision-hosts.yml
    groups: [k8s_worker, k8s_master]
    # converge: true # Idk if we need this
    # dry-run: true # might be nice?
```

---

## The grammar

- `infrastructure:` — **typed identity map** of substrate targets. Kind
  dispatch at the identity layer (`ansible-inventory`, `k8s-cluster`, more
  later), exactly as `forges:` entries carry provider identity. Absorbs
  everything that is *connection identity*: inventory file, SSH auth,
  known_hosts, cluster API endpoint, cluster auth. Perform units reference
  entities via `target:` the way repos reference forges.
- `perform:` — **ordered id → typed work unit map**, same shape `publish:`
  proved. `kind` dispatches the unit (`ansible`, `fluxcd`, `build-oci`,
  `build-bin`, `command`, `bash`, …). Presence in the map IS the decision to
  run it every perform.
- The **Outcomes box** is a literal rendering of this map: one row per unit,
  in execution order.

## Universal unit fields (every kind)

- `required:` — **crucial, per-unit** (the house `*bool` convention,
  "failure is hard pipeline fail"). Defaults per kind deserve thought:
  mutating kinds (ansible, fluxcd) default true; advisory/preview units opt
  down. Continue-then-fail stays the phase doctrine: a failed unit never
  starves later units of execution; required-ness only decides the exit.
- **Ordering — explicit control, not just map order.** Declared order is the
  default sequence; `needs: [<unit-id>]` (and `stage.from`, which implies a
  need) declares dependency so any organization can be expressed —
  parallel-able groups, diamonds, strict chains. A unit needing a
  later-declared unit is a validation error until/unless the executor goes
  graph-scheduled. Open: does v1 execute the graph concurrently or
  sequentially-in-topological-order? (Sequential first is fine; the shape is
  what matters.)
- `dry-run:` — **kept in spec** even if not v1-implemented: a permanent
  plan-only unit (drift preview without apply). Distinct from the global
  `--dry-run` flag.
- `image:` — per-unit execution image. This is the same *trust-evaluated
  runtime* idea as toolchains, expressed as a container: `command`/`bash`
  units already take one, ansible units take the execution image, crucible
  builds are built ON one. Presets default it per kind so repos only pin
  versions. (Longer arc: unify image-pinning trust with the toolchain
  ladder — same provenance vocabulary, different substrate.)

## Kind notes

- `ansible` — `target:` names an `ansible-inventory` entity; `playbook:`
  is a repo path (list = ordered plays in one unit); `limit`/`groups`
  narrows. **No `converge:` flag** — presence is convergence. **No playbook
  library/catalogue**: runbooks need zero declaration; `stagefreight ansible
  run <path>` reuses the entity's connection identity, and the play file's
  own `hosts:` declares where it lands. The repo is the catalogue.
- `fluxcd` — names the actual technology (leaves room for an argo kind
  without resurrecting a mode enum). `target:` names a `k8s-cluster` entity.
- `build-oci` / `build-bin` / `command` / `bash` — today's `builds:` kinds
  folded in as perform units, bodies carried over. `bash` = script list,
  `command` = argv (+`stage:` artifact staging between units).

## Infrastructure entity notes

- `exposure:` (k8s-cluster) — already consumed by docs today; the entity is
  where it lives (moving it to stencil config would be more awkward, not
  less). Carried in the entity schema as-is.
- `auth:` — `method: ssh|password` (inventory), `method: oidc|token`
  (cluster), `credentials:` env-prefix indirection unchanged. **Shape not
  final** — the sketch riffed on Semaphore; the doc needs the env-var
  derivation rules written as the contract before freeze.

## Secrets backend (vault / openbao) — NEW, this round

The `credentials: <PREFIX>` indirection is good and stays. What becomes
pluggable is the **source** that materializes those values:

```yaml
secrets:                       # working name — could be auth:, vault:, …
  backend: openbao             # env (today, default) | vault | openbao | …
  address: https://vault.example
  auth:
    method: env                # bootstrap cred for the backend itself comes
    credentials: VAULT         # from env/CI vars — locally or in CI
  mounts:
    - path: operationtimecapsule/ci
```

- **Don't reinvent**: resolution keeps the `<PREFIX>_*` vocabulary; a vault
  backend maps prefix → secret path/keys instead of prefix → env vars.
- **Precedence**: which side wins when both env/forge AND the backend hold a
  value — explicit config (`prefer: env|backend`), not guesswork.
- **Sync or dumb**: should SF ever *write*/sync between forge vars and the
  backend, or stay a pure reader? Lean: dumb reader first — sync is a
  separate (governance-flavored) feature with its own authority questions.
- Rationale for now: mature enough project; kills per-forge CI-var sprawl;
  local runs and external runners auth the same way (one bootstrap var in,
  everything else from the backend).

## Escape hatches: the universal onramp

`command`/`bash` as ordinary, orderable perform units is the feature that
lets "normal CI brain" users land without ceremony — plausibly the fastest
run-my-command-to-working-CI on the market: paste the script into the yml
(or point at a file in the repo) and it runs, ordered, with required/
dry-run/image semantics for free. No mental model demanded up front. And it
is an **adoption funnel, not a ghetto**: users start with bash, then trade
hatches for typed kinds one at a time as the intent vocabulary earns it.
Under the no-modes principle a bash unit is still a declaration of intent —
just the least-modeled one we accept.

### Phase-floating escape hatches

Because every phase key is the same typed-unit map, the portable kinds
(`command`, `bash`) are valid wherever a phase accepts units — an
`audition:` unit whose failure rejects the candidate and prints its stdout,
a `review:` unit producing advisory output, a `publish:` unit running a
bespoke distribution step. Users patch what they need into each job without
us modeling their world first. Phase semantics stay the phase's: audition
units gate candidacy, review units default advisory, publish units default
required. (Schema consequence to design: `audition:`/`review:` become
user-declarable unit maps at all — today those phases carry no user work.)

### Structured-output ingestion (last mile, LOW priority)

A declared mapping from an escape hatch's stdout to structured facts/
findings (regex/jsonpath → `{domain.key}` facts, finding rows) would let
hatches participate in narration and gating like first-class kinds — the
final bridge from "runs my script" to "speaks the model." Written down
deliberately; implement only if a shape appears that is trivial AND obvious
(the stencil bar). Until then hatch output is opaque: exit code + stdout.

### Versatility doctrine

With hatches in every phase, ordering control, and FOSS forkability, the
coverage claim is total: if StageFreight can't express your desire, you can
script it in a unit, ship your own bin, or fork and PR the feature —
exclusion is a choice, not a limitation.

## Deprecations this reshape retires

- `lifecycle.mode` — emergent from declared units (capability derivation and
  the (mode, name) backend registry re-key on unit kind).
- `ansible.playbooks` id-map + `converge:` flag + `PlaybookByID` — replaced
  by perform units + path-based `ansible run`.
- `gitops.cluster` — identity moves to `infrastructure:`; flux options
  (what remains of `gitops:`) fold into the fluxcd unit/preset.
- `builds:` — folds into `perform:` as build-* kinds.
- `governance:` — **dissolves into publish** (proposal, this round):
  governance is preset/config sync to a fleet of repos, and that is publish
  by definition — pipeline-produced content (rendered doctrine) distributed
  to declared targets (repo groups). One publish unit pairs one
  preset/config shape with its target repos; a control repo is just a repo
  with a wide publish block. Runs in any repo, no special treatment or
  modality. The read side (evaluate — drift detection, fail loud) splits
  out as an audition/review advisory unit under the phase-floating grammar,
  which is cleaner than today's evaluate-vs-apply flag inside one
  subsystem. Kills the fourth hardcoded branch in the perform runner AND
  the "clusters"-that-aren't-clusters vocabulary collision outright —
  cohorts become the unit's target group references into `repos:`/`forges:`
  (identity graph, again). Open: kind name (`doctrine`? `config-sync`?),
  and whether per-push deps-dry-run behavior inside reconciliation keeps
  its current trigger semantics under publish cadence.

## Coupling map — where moved keys silently deactivate behavior

The CI skeleton is universal, so the 5-job graph is untouched — this
reshape only changes dispatch inside the binary. That means migration bugs
here don't fail loudly: a missed re-key makes behavior **silently stop
happening**. Every item below is a required migration step with its failure
mode named:

1. **Audition capability derivation** — today keyed on `lifecycle.mode`
   (gitops → flux graph validation feeding perform's skip-invalid; docker →
   build validation). Re-keys to kind presence: `fluxcd` unit → flux
   validation, `build-*` → build caps, `ansible` → ansible-lint. Needs an
   explicit kind→capability table. Missed: candidates stop being validated
   and perform inherits unvalidated state.
2. **Publish/release unit-id references** — release/publish targets name
   build outputs by id. Migration preserves ids 1:1
   (`builds.<id>` → `perform.<id>`), so references survive unedited.
   Missed: releases silently ship without their artifacts.
3. **Deps engine walkers** — tag-line update paths point at where images
   live in config (`ci.image`, build images; ansible.image binding was
   already a known follow-up). Re-point at `perform.<id>.image` for every
   kind. Missed: image bumps silently stop landing anywhere.
4. **Presets / stencil defaults / governance overlay bodies** — all written
   against current key paths; presets resolve into the schema, governance
   `stagefreight:` doctrine bodies on the 13 downstream repos hand-migrate
   once, sequenced with the image two-step. Missed: preset resolution
   half-applies; governed repos drift from doctrine.
5. **Narrate fact domains** — subsystem names currently equal kinds 1:1
   (`build.*`, `ansible.*`, `reconcile.*`). Domains re-key as per-kind
   aggregates so every shipped union-body line keeps resolving; per-unit
   facts remain an open design question. Missed: summary/notification lines
   silently elide.

## Migration / landing (sketch)

1. `infrastructure:` lands additive (entities + `target:` resolution;
   existing keys keep working, migrate.go rewrites).
2. `perform:` reshape with migrations from `builds:`/`ansible:`/`lifecycle.mode`.
3. Deprecation window, then removal pre-v1 — the frozen v1 shape is THIS
   one, not the current smear.
4. Image-first two-step per schema change; 13 downstream repos migrate via
   reconciliation.
