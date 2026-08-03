## 📦 stagefreight — `v0.7.0`
> **Release type:** latest • **Commit:** `abcd1234`

**Security:** 🛡️ ✅ **Passed** — no vulnerabilities

## Image Availability

| Registry | Image | Tags |
|----------|-------|------|
| [Docker Hub](https://hub.docker.com/r/prplanit/stagefreight) | `docker.io/prplanit/stagefreight` | [`v0.7.0`](https://hub.docker.com/r/prplanit/stagefreight/tags?name=v0.7.0) `latest` |
| Harbor | `cr.pcfae.com/prplanit/stagefreight` | `v0.7.0` |

<details>
<summary>Digest pull commands & supply chain artifacts</summary>

**docker.io/prplanit/stagefreight**
```
docker pull docker.io/prplanit/stagefreight@sha256:75225fc
```
- SBOM: `stagefreight-0.7.0.sbom.spdx.json`
- Signature: `stagefreight-0.7.0.sig`

</details>

## Downloads

| Platform | File | Size | SHA-256 |
|----------|------|------|---------|
| `linux/amd64` | `stagefreight-0.7.0-linux-amd64.tar.gz` | 33.0 MB | `aaaabbbbcccc…` |
| `linux/arm64` | `stagefreight-0.7.0-linux-arm64.tar.gz` | 29.8 MB | `111122223333…` |

<details>
<summary>Full checksums</summary>

```
aaaabbbbccccddddeeeeffff00001111222233334444555566667777888899990  stagefreight-0.7.0-linux-amd64.tar.gz
1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff  stagefreight-0.7.0-linux-arm64.tar.gz
```
</details>

## Verification

| Property | Value |
|---|---|
| Signing tier | Tier-0 (persistent software key) |
| Trust domain | prplanit |
| Public key fingerprint | `SHA256:abcdef012345` |
| Transparency log | no |
| Signer continuity | stable |
| Human authorization required | yes |
| Non-exportable key | yes |

Verify the release checksums against the published public key:

```
cosign verify-blob \
  --key cosign.pub \
  --signature SHA256SUMS.sig \
  --insecure-ignore-tlog=true \
  SHA256SUMS
```

Pin the key by its fingerprint `SHA256:abcdef012345` — it is stable across releases; see `SECURITY.md` for the canonical trust anchor.

## Highlights
- Ships the stencil engine
- Also fixes badges

## Notable Changes

#### Breaking Changes
- **stencils**: shared text-composition library (kai)

#### Bug Fixes
- **narrate**: line elision (kai) ×2

#### Documentation
- refresh generated docs {and} badges

## Security

No blocking vulnerabilities.

<details>
<summary>Scan detail</summary>

- CVE-2026-0001 (low)
</details>
---

<details>
<summary>Full changelog</summary>

- [`abc1234`] shared text-composition library (kai)
- [`def5678`] line elision (kai)
- [`aaa1111`] line elision (kai)
- [`bbb2222`] refresh generated docs {and} badges

</details>
