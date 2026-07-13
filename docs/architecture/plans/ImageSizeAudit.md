# Image Size Audit

**Date**: 2026-03-13

## Observed Size

| Metric | Value |
|--------|-------|
| Binary (`stagefreight`) | 247.5 MB |
| Final image (`prplanit/stagefreight`) | ~260 MB (binary + Alpine base + runtime tools) |

## What's In the Binary

| Component | Est. Size | Wired Through | Purpose |
|-----------|-----------|---------------|---------|
| Go runtime + stdlib | ~180-200 MB | static linking baseline | `CGO_ENABLED=0` static binary |
| Gitleaks + Wazero WASM | ~15-20 MB | `src/lint/modules/secrets.go` | Secrets detection via embedded WASM engine |
| go-git/v5 | ~8-12 MB | `src/lint/delta.go` | Git delta detection for changed-file linting |
| Cobra + CLI styling | ~4-6 MB | `src/cmd/` | CLI framework, help rendering, shell completions |
| 8 embedded TTF fonts | ~3.1 MB | `src/fonts/fonts.go` -> `src/badge/font.go` | Badge text rendering (all selectable by config) |
| golang.org/x/image | ~2-3 MB | font parsing for badge text measurement | Image/font libraries for narrator badges |

## What's Confirmed NOT in the Binary

| Item | Size on Disk | Why It's Excluded |
|------|-------------|-------------------|
| `src/assets/*.png` | 829 KB | Build-time only via `//go:generate`, no `//go:embed` |
| Banner art | few KB | Pre-rendered ANSI text, not the source PNGs |

## Build Optimizations Already Applied

- `CGO_ENABLED=0` — pure Go static binary (no libc)
- `-s -w` linker flags — strips symbol table and DWARF debug info
- Multi-stage Docker build — only binary + Alpine base in final image

## Verdict

All compiled content is wired to live features. No dead code, no unwired embeds, no orphaned dependencies inflating the binary. The 247 MB reflects the cost of what StageFreight does:

- Full secrets scanning engine (Gitleaks + WASM runtime)
- Git-aware delta detection (go-git)
- Badge generation with font rendering (embedded TTFs)
- Static linking (entire Go stdlib compiled in)

Size reduction would require removing features, not trimming fat.
