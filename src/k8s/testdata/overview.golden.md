> Generated from live Kubernetes state
> Cluster: lab

## Apps & Services (2)

✅ 1 healthy · ❌ 1 down

### Kitchen (pantry)

| App | Status | Exposure | Links |
| --- | --- | --- | --- |
| Toaster (2 components) | ✅ | 🌐 toast.example.com | [open](https://toast.example.com) |

### Uncategorized (garage)

| App | Status | Exposure | Links |
| --- | --- | --- | --- |
| Workbench | ❌ | ❓ |  |

<details>
<summary>App details</summary>

**Toaster — 2 components** — Mixed — 2.1.0
- Namespace: pantry
- Replicas: 2/2
- Hosts: toast.example.com
- Gateway: front-door
- Components:
  - toaster (Deployment)
  - toaster-db (StatefulSet)

**Workbench** — Deployment — 
- Namespace: garage
- Replicas: 0/1

</details>

## Platform Components (1)

| Component | Namespace | Status |
| --- | --- | --- |
| Boiler | pipes | ✅ |

<details>
<summary>Platform details</summary>

**Boiler** — DaemonSet — 1.0
- Namespace: pipes
- Replicas: 3/3

</details>

## Unclassified (1)

Not yet placed: declare their namespaces under `gitops.lab.categories` to list them as apps or platform.

| Workload | Namespace | Status |
| --- | --- | --- |
| Shelf | garage | ✅ |

<details>
<summary>Retired / Graveyard (1)</summary>

| Name | Namespace | Reason |
| --- | --- | --- |
| kettle | pantry | missing since 2026-03-30 |

</details>
