// Package cliflag provides self-describing CLI flag types. An EnumValue carries its own
// set of allowed values, so (1) the CLI validates input at parse time — an out-of-set
// value is rejected with a clear message instead of silently accepted — and (2) the docs
// generator reads the allowed set directly, rendering the real options instead of hoping
// the usage string mentions them. One source of truth for --help and the reference docs.
package cliflag

import (
	"fmt"
	"strings"

	"github.com/spf13/pflag"
)

// EnumValue is a pflag.Value backed by a fixed set of allowed strings.
type EnumValue struct {
	target  *string
	allowed []string
}

// NewEnum stores def into target and returns an EnumValue constrained to allowed.
// An empty def (a common "fall back to config" sentinel) is permitted and left as-is.
func NewEnum(target *string, allowed []string, def string) *EnumValue {
	*target = def
	return &EnumValue{target: target, allowed: allowed}
}

func (e *EnumValue) String() string { return *e.target }

// Set validates v against the allowed set (empty is always accepted as "unset").
func (e *EnumValue) Set(v string) error {
	if v == "" {
		*e.target = v
		return nil
	}
	for _, a := range e.allowed {
		if v == a {
			*e.target = v
			return nil
		}
	}
	return fmt.Errorf("must be one of: %s", strings.Join(e.allowed, ", "))
}

// Type reports "string" so the value reads as an ordinary string flag everywhere.
func (e *EnumValue) Type() string { return "string" }

// Allowed returns the permitted values, for documentation.
func (e *EnumValue) Allowed() []string { return e.allowed }

// OptionsSuffix is the " (one of: …)" hint appended to an enum flag's usage so `--help`
// lists the allowed values inline. It is shared with the docs generator, which strips it
// back off (the reference renders the values in a dedicated Options column instead, so
// keeping it in the description too would be redundant). Empty for a non-enum.
func OptionsSuffix(allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	return " (one of: " + strings.Join(allowed, ", ") + ")"
}

// EnumVar registers an enum-constrained string flag on fs. The usage should be plain
// prose (no value list) — the allowed values are appended for --help and surfaced
// separately in the docs.
func EnumVar(fs *pflag.FlagSet, target *string, name string, allowed []string, def, usage string) {
	fs.Var(NewEnum(target, allowed, def), name, strings.TrimRight(strings.TrimSpace(usage), ".")+OptionsSuffix(allowed))
}
