# GitLab runner

A self-hosted GitLab runner for StageFreight pipelines, on Docker Compose or on Kubernetes.

| Runner | Builds images with | Deploy when |
|---|---|---|
| [Compose — minimal](compose/README.md) | — (DinD only) | the runner never builds images: GitOps reconcile, Ansible |
| [Compose — full](compose/README.md) | BuildKit beside DinD | a Docker host builds container images |
| [Kubernetes](kubernetes/README.md) | a rootless buildkitd, no Docker daemon | jobs run as pods in a cluster |

Every variant runs jobs in the StageFreight image as uid 1000 in group 0, keeps a persistent cache
across jobs, and bounds its own disk.

## Register the runner in GitLab

A runner's identity and policy are set in GitLab at creation, not in the stack. Create it first —
**Settings → CI/CD → Runners → New project runner** — and set:

| Field | Purpose |
|---|---|
| Tags | Pin jobs to the runner — its tags must match a job's `tags:`. |
| Run untagged jobs | Whether the runner accepts jobs that declare no tags. |
| Run on protected branches only | Restrict the runner to protected refs — required for runners holding protected secrets (e.g. an Ansible SSH key) so merge-request pipelines cannot reach them. |

Creation returns a `glrt-…` authentication token — the only secret a runner requires. This is
**separate** from `GITLAB_TOKEN` ([Credentials](../README.md#credentials)): `glrt-…` authorizes
the runner to the instance, `GITLAB_TOKEN` authorizes StageFreight's API calls in the job.

## Cluster authentication (GitOps)

A GitOps pipeline reconciles against a Kubernetes cluster; the runner authenticates through CI
variables only. StageFreight builds a throwaway kubeconfig per job and never writes a kubeconfig
or CA to the host, so any runner variant serves GitOps with these set:

| Variable | Purpose |
|---|---|
| `STAGEFREIGHT_OIDC`, or `<CLUSTER>_TOKEN` | Identity — an OIDC token, or a service-account token |
| `<CLUSTER>_CA_B64`, or `<CLUSTER>_CA_FILE` | Cluster CA — base64 PEM, or a path to one |

`<CLUSTER>` is the env prefix derived from the `gitops.cluster` name. Set them as masked,
protected CI variables and declare the cluster in the `gitops:` block — see
[GitOps config](../../../reference/Config.md#config-gitops).
