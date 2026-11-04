# GitLab runner on Kubernetes

StageFreight on GitLab's Kubernetes executor, with no Docker daemon: non-root job pods with a
read-only root filesystem, a shared cache volume, and a rootless buildkitd for image builds.

| File | Provides |
|---|---|
| [`values.yaml`](values.yaml) | the runner, for the `gitlab/gitlab-runner` Helm chart: job pod security, cache and BuildKit mounts, per-job scratch volume |
| [`kustomization.yaml`](kustomization.yaml) | everything beside the runner, applied with `kubectl apply -k` |
| [`serviceaccount.yaml`](serviceaccount.yaml) | the job pods' identity, with no API access |
| [`cache.yaml`](cache.yaml) | the shared cache, a ReadWriteMany volume mounted at `/stagefreight` |
| [`buildkitd.yaml`](buildkitd.yaml) · [`buildkitd.toml`](buildkitd.toml) | rootless buildkitd, its build cache volume and GC budget |
| [`buildkitd-certificates.yaml`](buildkitd-certificates.yaml) | BuildKit's own mTLS PKI, from cert-manager |

## Before you begin

- [cert-manager](https://cert-manager.io/) is installed in the cluster.
- A ReadWriteMany storage class (CephFS, NFS, …) for the cache, and a ReadWriteOnce class for
  buildkitd and job scratch.
- Nodes allow user namespaces for rootless buildkitd. `buildkitd.yaml` runs it Unconfined as
  BuildKit's example does; a cluster enforcing restricted Pod Security needs a namespace exemption
  or Localhost seccomp/AppArmor profiles that grant only namespace creation.
- A [registered runner](../README.md#register-the-runner-in-gitlab) and its `glrt-…` token.

## Deploy

1. Create the namespace and the runner token Secret:
   ```bash
   kubectl create namespace gitlab-runner
   kubectl -n gitlab-runner create secret generic gitlab-runner \
     --from-literal=runner-token=RUNNER_TOKEN \
     --from-literal=runner-registration-token=
   ```
   `RUNNER_TOKEN` is the `glrt-…` token. The chart mounts both keys; the legacy registration
   token stays empty.
2. Set the storage classes: `storageClassName` in `cache.yaml` and the `buildkitd-state` claim in
   `buildkitd.yaml`, and `jobScratch.storageClass` in `values.yaml`. To use another namespace,
   change it in `kustomization.yaml` and in the `buildkitd` certificate's DNS names.
3. Apply the cache, buildkitd and its certificates:
   ```bash
   kubectl apply -k .
   ```
4. Install the runner, with `gitlabUrl` in `values.yaml` set to your instance:
   ```bash
   helm repo add gitlab https://charts.gitlab.io
   helm install gitlab-runner gitlab/gitlab-runner -n gitlab-runner -f values.yaml
   ```

## Verify

1. buildkitd is ready and its certificates are issued:
   ```bash
   kubectl -n gitlab-runner get pods,certificates
   ```
   `buildkitd-0` is `1/1 Running`, and `buildkit-ca`, `buildkitd` and `buildkit-client` are
   `True`.
2. The runner shows as online under **Settings → CI/CD → Runners**.
3. Run a pipeline. No `cache root … can't write it` warning appears in the job log.

## Without a daemon

Everything runs as it does on a Docker runner:
- Images build into the content store and publish promotes, signs and attests them.
- A `kind: command` (or builder-image) step runs inside the job when its image is the CI image, and as a BuildKit build otherwise.
- A crucible build verifies both passes from OCI layouts.
- Ansible converge runs natively when the job image ships `ansible-playbook` and `ssh-agent`; otherwise reconcile is refused at preflight.

Two things need a daemon and are skipped or refused without one: `provider: local` registries,
and Compose `docker_local` targets.

The job pods need enough memory for the audition's test run; the Executor panel reports headroom
under the job's own memory limit. A job may run as another uid (`run_as_user`, `run_as_group`):
any uid works, as long as `fs_group` gives it write access to the cache.
