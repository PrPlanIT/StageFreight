# Signing

Supply-chain signing for release artifacts and images. Two concerns kept separate on purpose:
the **operational** switch (`signing.enabled` / `signing.auto_provision` — may StageFreight
sign, and may it provision an identity) and the **trust profiles** (`signing.profiles` — an
id→profile map defining what class of key each profile requires). A publish target references a
profile via `signing_profile: <id>`.

## Signing with Vault or OpenBao

A `kms` profile with a `provider` signs through Vault/OpenBao's transit engine, logging in with
the CI job's OIDC token: no stored secret. The config names refs; CI variables bind them, so one
config serves every group:

```yaml
secrets:
  providers:
    openbao: { kind: vault }
signing:
  profiles:
    release:
      requires: [kms]
      kms: { ref: tenant, provider: openbao, role: cosign }
      enforce: true
```

| CI variable | Example | Sets |
|---|---|---|
| `SF_SECRETS_OPENBAO_ADDR` | `https://secrets.example.com` | the provider's address |
| `SF_SECRETS_OPENBAO_LOGIN` | `gitlab-ci` | the JWT auth mount (default `jwt`) |
| `SF_SECRETS_OPENBAO_ROLE_COSIGN` | `stagefreight-cosign-acme` | the role for `role: cosign` |
| `SF_KMS_TENANT` | `hashivault://acme` | the key for `ref: tenant` |
| `TRANSIT_SECRET_ENGINE_PATH` | `cosign-signer` | the transit mount the key lives in |

`OPENBAO` is the provider id and `COSIGN`/`TENANT` the refs, upper-cased with `-` and `.` as
`_`. The job mints its token with `aud: stagefreight` (the rendered pipeline does this), so the
role must bind that audience. With `enforce: true` a signing failure fails publish; without it the
image ships and the failure is reported.

--8<-- "docs/assets/modules/config-reference.md:signing"
