# Identity & Connectivity

Who you are and what you talk to: template variables, the git hosts (forges) and projects
(repos) StageFreight pushes to, and the container registries it authenticates against.

The reference for each key is generated from the config source below.

--8<-- "docs/assets/modules/config-reference.md:version"

--8<-- "docs/assets/modules/config-reference.md:vars"

--8<-- "docs/assets/modules/config-reference.md:defaults"

--8<-- "docs/assets/modules/config-reference.md:forges"

--8<-- "docs/assets/modules/config-reference.md:repos"

--8<-- "docs/assets/modules/config-reference.md:registries"

## `llms:` — model endpoints

`llms:` is an endpoint library, a sibling of `forges:` and `registries:`: each entry names a
model backend (provider, URL, model, credentials) that AI stencils reference by id — so
endpoints and credentials never leak into composition. `provider: ollama` ships first;
`openai` / `anthropic` / `claude-agent` slot in behind the same shape. See
[Stencils & Scribe](scribe.md) for the `type: llm` stencil that consumes these, and
[Narration & Notifications](narration.md) for dispatching AI output.

--8<-- "docs/assets/modules/config-reference.md:llms"
