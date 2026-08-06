# Reconcile box: cluster identity + health (design plan)

## Problem

The gitops reconcile output box only labels itself "Reconcile". With a single
cluster that's tolerable; with multiple clusters reconciling to one narration
surface it's ambiguous — you cannot tell WHICH cluster the results belong to.
The box must prove its subject.

## Shipped now (bare minimum)

The Reconcile box carries canonical identity rows, sourced best-effort so a
query failure degrades gracefully to the config-derived name:

```
── Reconcile ──
│ cluster  dungeon · v1.35.6 · 10 nodes
│ backend  flux
│ auth     oidc
│ [1/12] …
```

- `cluster` — name (config), k8s server version + node count (`kubectl
  version -o json` / `get nodes`, distro-agnostic: same for kubeadm/k3s).
- `backend` — the reconcile agent (`flux`; argo/k3s-native forthcoming).
- Plumbed via `LifecyclePlan.Notes` (same path as the existing `auth` row);
  gathered in `clusterIdentity()` (src/gitops/flux.go).

Also shipped: the multi-line flux-error leak is fixed — a failed `flux
reconcile` streamed `►/✔/◎/✗` progress and only line one carried the box
prefix; now the message is distilled to its meaningful line (`cleanFluxError`)
and the renderer prefixes every line defensively.

## Full vision (follow-up)

- **Cluster age** and richer identity (cluster UID / API endpoint host).
- **Health snapshot**: cpu allocatable/used, mem allocatable/used, pod count /
  capacity — the "is this cluster healthy" glance.
- **Backend-agnostic fact interface**: each reconcile backend (flux, argo,
  k3s-native) returns the SAME identity+health fact shape, so the box renders
  uniformly regardless of gitops tool or distro. Define a small
  `ClusterFacts` producer the backend implements, rather than flux-specific
  scraping.
- **Multi-cluster narration**: when N clusters reconcile, one identity block
  per cluster above its own results — the guarantee that "who got reconciled"
  is never ambiguous. Ties to the S1_E2 `infrastructure:` k8s-cluster identity
  entity: the cluster the unit targets is the identity the box renders.

## Not doing (yet)

Heavy per-reconcile health polling — keep the gather to a couple of cheap
kubectl calls; a full metrics pull belongs behind an opt-in, not on every
reconcile.
