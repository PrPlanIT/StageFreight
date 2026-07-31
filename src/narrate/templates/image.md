# {project.name} — {ci.status}
**{ci.modality}** · {ci.ref} · `{ci.sha}`{#if has_version} · {version}{/if}{#if has_pipeline} · [pipeline]({ci.pipeline_url}){/if}

## Outcome
{#if failed}{ci.status_verb} at **{ci.failure.subsystem}** — {ci.failure.reason}{#else}{ci.status_verb}{ci.ship_apex}.{/if}

## Changes {changelog.range}
{changelog}

{#if failed}
## What broke
{failures}
{#else}
## Phases
{acts}
{/if}

## Artifacts & release
{shipped}

{#if has_housekeeping}
## Housekeeping
{coda}
{/if}
