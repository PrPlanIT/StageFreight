# Philosophy

StageFreight exists to replace the pile of bespoke CI scripts every project accretes — the
brittle `bash` that builds the image, the second script that tags it, the copy-pasted release
job, the cron that prunes old artifacts — with a single declarative runtime driven by one
file.

## One file is the source of truth

A project's entire lifecycle — build, sign, release, publish, retain — is declared in
`.stagefreight.yml`. There is no second config surface: no per-command flags to memorize, no
scattered dotfiles, no pipeline YAML to hand-maintain. The CI pipeline itself is *rendered*
from that one file and committed as a generated artifact. What the file says is what runs.

## The tool is the steward, not a library of snippets

StageFreight owns the build-and-release process end to end so a project never has to. Rather
than shipping examples to copy, it standardizes the whole lifecycle behind declared intent:
state what should happen and under which conditions, and the runtime carries it out the same
way on every forge and every registry.

## Every capability is universal

Features are built generically — useful to any project, never hardcoded to one. A retention
policy, a credential prefix, a routing condition, a target kind: each means the same thing
everywhere it appears, so learning one corner of the config teaches the rest. A new
capability is added to the shared model, not bolted onto a single caller.

## Fix the engine, not the symptom

When something doesn't fit, the answer is to build the abstraction properly, not to add a
workaround or gate off the feature. This is enforced structurally: a set of
[hard invariants](architecture/invariants.md) exist in code before they are written down, and
the pipeline is designed so a routing or safety fix lands in one place and holds everywhere.

## It proves itself in the open

StageFreight builds, scans, documents, and releases StageFreight — the binary, these docs,
and this site are all produced by its own pipeline. The [screenshots](screenshots.md) are
real runs, not mockups, on GitLab and GitHub alike. A tool that governs other projects'
releases should be held to the standard it enforces.

## Honest about what ships

Documentation describes what works today. Designs that are still aspirations live in the
repository's planning notes, not in the reference — so the line between a shipped capability
and a future one is never blurred.
