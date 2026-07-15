# `.stagefreight.yml` shape proposals — scratchpad

Working doc to iterate the config surface (starting with `narrate`) toward something
**tidier and compose-ergonomic without losing any capability**. Nothing here is
committed to the schema yet — it's a design scratchpad. The live `.stagefreight.yml` is
the reference for "what must still be expressible."

## Goal / constraints

1. **Lossless.** Everything the current schema can do must survive: explicit markers,
   `mode` (replace/append/prepend), `inline`, item **order**, multiple regions per file,
   multiple files, per-item overrides, and all item kinds (`badge_ref`, `shield`, `props`,
   `include`, `build-contents`).
2. **Compose-ergonomic.** Reads top-to-bottom, minimal repetition, no boilerplate you
   copy-paste 25 times. Should feel like something you *want* to write.
3. **Minimal relearning.** Keep the vocabulary that already means something (`kind`,
   `ref`, `between`) unless collapsing it is a clear win.

---

## Baseline (today) — the fatigue

Every item re-states the same placement. ~25× this, across 3 regions in one file:

```yaml
narrate:
  patches:
    - file: README.md
      items:
        - id: badge.build
          kind: badge_ref
          ref: build
          placement:
            between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
            mode: replace
            inline: true
        - id: badge.license
          kind: badge_ref
          ref: license
          placement:
            between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]   # ← identical, again
            mode: replace
            inline: true
        # …23 more, the placement copied verbatim each time
```

Full `narrate:` today ≈ **322 lines** (badges defs + patches + commit).

---

## Proposal A — `files:` + `items:` maps, placement hoisted  ← current lean

Placement is a property of the **region**, declared once; items inherit it. Items are a
**map keyed by id** (drops the `id:` field). One `files:` entry per marker region; the
key is just a label.

```yaml
narrate:
  files:
    readme.badges:                                   # label; groups one marker region
      file: README.md
      between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
      mode: replace
      inline: true                                   # region defaults → inherited by items
      items:
        - build:   { kind: badge_ref, ref: build }
        - license: { kind: badge_ref, ref: license }
        - donate:  { kind: shield, shield: "badge/donate-FF5E5B?logo=ko-fi", link: "https://ko-fi.com/…" }

    readme.project:
      file: README.md
      between: ["<!-- sf:project:start -->", "<!-- sf:project:end -->"]
      inline: true                                   # mode:replace is the default
      items:
        - go-report:    { kind: props, type: go-report-card }
        - issues-open:  { kind: props, type: github-issues-open }
        - contributors: { kind: props, type: github-contributors }

    readme.image:
      file: README.md
      between: ["<!-- sf:image:start -->", "<!-- sf:image:end -->"]
      inline: true
      items:
        - docker: { kind: shield, shield: "…" }
        - br                                          # explicit row break, keeps ordering
        - latest: { kind: shield, shield: "…" }
        - size:   { kind: shield, shield: "…", mode: append }   # per-item override still available
```

### What this keeps (the lossless check)
- **Explicit markers** — `between:` is stated, per region (not magic'd from a name).
- **`mode` / `inline`** — region default, **overridable per item** (`size` above).
- **Order + breaks** — item order = row order; `kind: break` is an explicit break.
- **Multi-region / multi-file** — one `files:` entry per region; `file:` can be anything.
- **All kinds** — `kind:` is retained verbatim.

### Redundancies it removes
- Placement stated **once per region** instead of per item (the big win).
- `id:` gone (item key = id).

### Further squeezes (optional, decide below)
- `output` for render badges → `.stagefreight/badges/{id}.svg` (always).
- `ref` for `badge_ref` → **defaults to the item key**, so `build: { kind: badge_ref }`.
- `text` for a render badge → defaults to its key.

---

## Open questions to resolve here

1. **Item ordering vs map.** A YAML map is unordered in most Go parsers, but **badge row
   order matters.** Options: (a) parse `items:` as an *ordered* map (insertion order via
   `yaml.Node`), or (b) make `items:` a list where each entry carries a `ref:`/key. Lean?
   *(Map reads nicer; needs ordered-map parsing to be safe.)*

2. **Multiple regions per file.** Proposal A uses one keyed `files:` entry per region
   (`readme.badges`, `readme.project`, …), so `file: README.md` repeats. Alternatives:
   a `regions:` sub-list under a single `readme:` file entry. Which reads better?

3. **`kind` collapse (props vs badges vs shield).** Keep `kind: badge_ref|shield|props`,
   or infer from a token (`build` = ref, `shield:…`, `prop:…`)? Verified distinction:
   `badge_ref` → local SVG (`src/badge`), `props` → typed external via registry
   (`src/props`), `shield` → raw external. `props` and `shield` are the *same output*
   (external image); `props` is just a *named* shield. Candidate collapse: one item with a
   `source` (`render` | `type` | `shield` | `image`). **Deferred — Proposal A keeps `kind`.**

4. **Non-badge items.** `kind: include` and `kind: build-contents` (docs-fragment
   assembly, e.g. `--8<--`-style CLI/env reference) live in the same `items` list today.
   Do they fit the `files:`/`items:` shape unchanged, or want their own section?

5. **Where do render definitions live?** The `badges:` block (SVG render config: value,
   color, font) is separate from placement. Keep a top-level `badges:` map for render defs
   and let `files:`/`items:` do placement? (Placement referencing render defs by key.)

---

## Scratchpad — iterations

<!-- paste/edit shapes below; we converge here before touching the schema -->
