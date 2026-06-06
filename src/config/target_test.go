package config

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestTargetConfig_ChannelFieldsRoundTrip pins the release-channel fields —
// `tag:` (immutable identity pattern) and `prerelease:` — as recognized by the
// STRICT (KnownFields) decoder that config.Load uses, and confirms they survive
// a YAML round-trip. The strict decode is the meaningful assertion: it proves the
// fields are part of the schema (an older binary without them would error here —
// the dogfood gotcha for configs that adopt `tag:`/`prerelease:`).
func TestTargetConfig_ChannelFieldsRoundTrip(t *testing.T) {
	const y = `
id: dev-channel
kind: release
tag: "dev-{sha:8}"
aliases: ["latest-dev"]
prerelease: true
`
	var got TargetConfig
	dec := yaml.NewDecoder(bytes.NewReader([]byte(y)))
	dec.KnownFields(true) // mirror config.Load strictness — unknown fields must error
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("strict decode of release target with tag/prerelease: %v", err)
	}
	if got.Tag != "dev-{sha:8}" {
		t.Errorf("Tag = %q, want %q", got.Tag, "dev-{sha:8}")
	}
	if !got.Prerelease {
		t.Error("Prerelease = false, want true")
	}
	if len(got.Aliases) != 1 || got.Aliases[0] != "latest-dev" {
		t.Errorf("Aliases = %v, want [latest-dev]", got.Aliases)
	}

	out, err := yaml.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(out, []byte("dev-{sha:8}")) || !bytes.Contains(out, []byte("prerelease: true")) {
		t.Errorf("round-trip lost channel fields:\n%s", out)
	}
}
