# Signing

Supply-chain signing for release artifacts and images. Two separate concerns, kept separate
on purpose: the **operational** switch (`signing` — may StageFreight sign, and may it
provision an identity) and the **trust profiles** (`signing_profiles` — what class of key a
target must sign under). Targets reference a profile via `signing_profile: <id>`.

--8<-- "docs/modules/config-reference.md:signing"

--8<-- "docs/modules/config-reference.md:signing_profiles"
