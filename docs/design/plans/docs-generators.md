# Plan: docs generators — a generic container-run documentation primitive

> Status: design (reviewed). **Reframed** from a "reference docs feature" into a reusable
> container-run generator primitive — reference docs are its first *use*, not the feature.
> Supersedes the StageFreight-only `reference_docs` generator (defaulted off in `641278f`).

## The reframe

Not "reference docs." The primitive is: **run a command in a container, capture one or
more output directories.** That single primitive serves reference docs *and* OpenAPI/
protobuf docs, ERDs, mermaid/diagram rendering, changelog synthesis, SDK docs — anything
that is "run a tool, produce files." StageFreight orchestrates; the project decides how.

> Instead of adding a feature called *reference docs*, we add a reusable primitive that
> happens to solve reference docs first, and naturally supports many future documentation
> generators with no further architectural changes.

## Config contract (flat — no `container:` nesting level)

Image/command live **directly** on the generator (the whole generator *is* a container
command — no wrapper key). `outputs` maps produced paths → repo destinations; the project
owns the destination, StageFreight never hardcodes one.

```yaml
docs:
  commands:                          # a list of container-run doc generators
    - image: docker.io/library/rust:1.83
      command: [cargo, doc, --no-deps]
      workdir: .                     # optional (default: repo root)
      env: { RUSTDOCFLAGS: "-D warnings" }   # optional
      forward_env: [DOCS_TOKEN]      # optional — CI secrets passed by value
      outputs:
        - source: target/doc         # what the tool produced
          destination: docs/reference  # where it lands in the repo — the project decides
```

- **No hardcoded destination.** The generator produces `source`; the project chooses
  `destination` (`docs/api`, `website/reference`, `docs/generated/api`, …). Different
  concerns, kept separate.
- **No language inference.** `image` + `command` is the entire abstraction. `cargo doc`,
  `./scripts/build-reference.sh`, `make docs`, `typedoc`, `sphinx`, `mkdocs`, a custom
  binary — all identical to StageFreight. No heuristic matrix, no per-language special
  cases. (Explicitly rejected: inferring a generator from the build's language — that path
  becomes an unmaintainable matrix the moment mdBook/Hugo/Antora/Docusaurus show up.)
- **Multiple generators**, each with **multiple outputs**, all supported.

## Lifecycle: Perform (a build artifact, not a narration artifact)

Reference docs have every property of a binary/OCI artifact — need toolchains, take
minutes, must be reproducible, cached, and **reviewable before publication**. So they're a
Perform output, and Narrate stays pure (it never *creates* artifacts):

```
Perform    run generator(s) in a container → capture output tree(s) → ManagedRoot transport
   ↓
Review     inspect / approve the docs artifact before it is committed or published
   ↓
Narrate    commit each tree into its destination (existing docs.commit) — creates nothing
   ↓
Publish    unaware — a pages / release / artifact target MAY consume the tree, separately
```

## The engine knows nothing about docs tooling

The engine runs a command in a container and captures directories — full stop. It never
knows Cobra/cargo/typedoc. Reuses the existing `ContainerMeta`
(`src/build/engines/container_meta.go`: `Image`/`Command`/`WorkDir`/`Env`/`Artifact`/
`ForwardEnv`); each `outputs[].source` is a captured `Artifact` tree; `destination` is
applied at Narrate.

## StageFreight dogfoods — delete "SF reference docs" from the engine

The engine must not contain the notion of StageFreight's own docs. The reflection
implementation (Cobra tree → `cli-reference.md`, Go structs → `config-reference.md`) stays
**inside the SF CLI** as `stagefreight docs generate`. SF's config then declares a normal
generator like anyone else:

```yaml
docs:
  commands:
    - command: [stagefreight, docs, generate, --output, docs/generated]
      outputs:
        - source: docs/generated
          destination: docs/reference
```

The hardcoded `gen.ReferenceDocs → RunDocsGenerate(rootCmd)` branch
(`src/cli/cmd/ci_runners.go`) is **removed** from the generic docs runner. SF becomes just
another project. (`image` omitted ⇒ default to `ci.image`, which is how SF runs its own
CLI.)

## Security: same trust model as builds — not a new surface

Running an arbitrary command for docs is the **identical** primitive as the build engine
(arbitrary command, isolated container). It is not an escalation:

- the command comes from the repo's own **version-controlled `.stagefreight.yml`** — a
  malicious docs command means the repo is already compromised (same as a malicious build);
- it runs in a **container** (isolation), like every build step;
- the **Review** phase can inspect the produced docs artifact before Narrate/Publish.

Thinking about it isn't overreacting — but it's covered by the controls builds already
rely on, with no additional exposure.

## fairer-pages: docs embedded in the image

Because generation is perform-time and yields a **tree**, a later Perform build step (the
image build) can `COPY` the docs into the image — exactly fairer-pages' "ships docs served
within the container." Ordering: docs-generator step → image build that consumes the tree.
Supported by perform-time placement; no coupling required.

## Pages: out of scope (deliberately unmentioned in P1)

A generator produces a tree. Where it goes — Cloudflare/GitHub Pages, a git commit, a
release asset — is a **publish** concern the generator never knows. If someone later wants
to publish docs, they point a `pages`/release target at the tree. Not a P1 concern.

## Implementation surface (reuse-first)

- **Config** — add `docs.commands []DocsCommand`:
  `{Image, Command []string, WorkDir, Env map, ForwardEnv []string, Outputs []{Source,
  Destination}}`. Leave the legacy `docs.generators.{badges,narrator,docker_readme}` bools
  as-is for now (separate concern). New key `docs.commands` avoids colliding with the
  existing `docs.generators` struct.
- **Perform** — for each command, synthesize a `ContainerMeta` step and run via
  `containerEngine.ExecuteStep`; capture each `output.source` as a tree; archive into
  `.stagefreight/` (reuse `CreateArchive`/`expandSource` — the ManagedRoot transport
  invariant, identical to `kind: pages`).
- **Narrate** — resolve the transported tree(s), extract each to its `destination`, and let
  the existing `docs.commit` add + commit them. Auto-add destinations to `docs.commit.add`.
- **Retire** the `RunDocsGenerate(rootCmd)` default path; keep it only as SF's internal
  `stagefreight docs generate`.
- **Validation** — `command` non-empty; ≥1 output; `source`/`destination` repo-relative and
  zip-slip-safe; `image` defaults to `ci.image`.

## Phasing

- **P1** — the generic `docs.commands` primitive end-to-end: container run →
  source→destination outputs → perform capture → ManagedRoot transport → narrate commit;
  SF dogfoods; retire the reflection default path. Validation + tests. (The command escape
  hatch makes every project capable on day one.)
- **P2** — ergonomics: output globs, per-generator cache keys, parallel generators.
- **P3** — optionally unify badges/narrator/docker_readme as `docs.commands` entries (one
  generator model for everything), if that consolidation proves worthwhile.

## Open questions (narrowed)

1. **Config key** — `docs.commands` (proposed, additive, no collision) vs eventually
   migrating `docs.generators` into a unified list that also absorbs badges/narrator
   (P3-ish)?
2. **`kind:` field** — the proposal drops it (there's one primitive: a container command).
   Keep a `kind:` for forward-compat with non-container generators, or stay minimal?
