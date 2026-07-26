# Signing

Supply-chain signing for release artifacts and images. Two concerns kept separate on purpose:
the **operational** switch (`signing.enabled` / `signing.auto_provision` — may StageFreight
sign, and may it provision an identity) and the **trust profiles** (`signing.profiles` — an
id→profile map defining what class of key each profile requires). A publish target references a
profile via `signing_profile: <id>`.

--8<-- "docs/assets/modules/config-reference.md:signing"
