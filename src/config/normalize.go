package config

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Normalize resolves all {var:...} templates throughout the entire config.
// Walks every string in the config graph recursively — structs, maps, slices,
// interfaces. No field enumeration. No partial coverage.
//
// Called once after load+validate, before any consumer reads the config.
func Normalize(cfg *Config) error {
	// Signing: alias canonicalization (keyless→oidc, yubikey→hardware) + legacy
	// profile synthesis are independent of {var:} templating and must run even
	// when no vars are defined — so they precede the vars short-circuit.
	cfg.SigningSetup.Profiles = NormalizeSigning(cfg.SigningSetup.Profiles)

	// The supply-chain cooldown is OWNED by dependency.min_release_age but consumed
	// by the freshness discovery resolver (which powers both the freshness lint
	// module AND dependency updates). Propagate the owned value into the freshness
	// module options — its historical home — so every consumer reads ONE value.
	// Like Signing above, this runs before the vars short-circuit.
	propagateCooldown(cfg)

	// Derive badge output paths from scribe.store + id (so a badge needs no output:
	// line). Independent of {var:} templating — runs before the vars short-circuit.
	cfg.ApplyStencilStoreDefaults()

	if len(cfg.Vars) > 0 {
		// Guard: vars must not contain nested templates (single-pass only).
		for k, v := range cfg.Vars {
			if strings.Contains(v, "{var:") {
				return fmt.Errorf("var %q contains nested {var:} template — not allowed", k)
			}
		}
		resolveValue(reflect.ValueOf(cfg), cfg.Vars)
	}

	// Identity pass: {org.*}/{orgs.*}/{metadata.*}/{slug}/{path.*} are pure functions
	// of the loaded config, so they resolve HERE — one model for every consumer (push
	// refs, forge API, pages, badges, scribe). Runs after {var:} so var-composed
	// identity inputs are concrete first. The governance payload is skipped (opaque —
	// it describes the satellite, whose identity resolves at ITS load).
	expandIdentity(cfg)
	return nil
}

// walkStrings rewrites every settable string in the config graph through fn, skipping
// the governance section (opaque satellite payload). Same traversal shape as
// resolveValue, generalized over the substitution.
func walkStrings(cfg *Config, fn func(string) string) {
	walkValue(reflect.ValueOf(cfg), fn, true)
}

// walkValue mirrors resolveValue, generalized over the substitution fn.
// skipGovernance elides the root Config's Governance field.
func walkValue(v reflect.Value, fn func(string) string, skipGovernance bool) {
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			walkValue(v.Elem(), fn, skipGovernance)
		}
	case reflect.Struct:
		isRoot := skipGovernance && v.Type() == reflect.TypeOf(Config{})
		for i := 0; i < v.NumField(); i++ {
			if isRoot && v.Type().Field(i).Name == "Governance" {
				continue // opaque satellite payload
			}
			if f := v.Field(i); f.CanSet() {
				walkValue(f, fn, skipGovernance)
			}
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(fn(v.String()))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			walkValue(v.Index(i), fn, skipGovernance)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		for _, key := range v.MapKeys() {
			v.SetMapIndex(key, walkAny(v.MapIndex(key), fn))
		}
	case reflect.Interface:
		if !v.IsNil() {
			if resolved := walkAny(v.Elem(), fn); v.CanSet() {
				v.Set(resolved)
			}
		}
	}
}

// walkAny mirrors resolveAny for the generalized walker: map/interface values are not
// settable in place, so rebuild them through fn.
func walkAny(v reflect.Value, fn func(string) string) reflect.Value {
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return v
		}
		return walkAny(v.Elem(), fn)
	}
	switch v.Kind() {
	case reflect.String:
		return reflect.ValueOf(fn(v.String()))
	case reflect.Map:
		newMap := reflect.MakeMap(v.Type())
		for _, key := range v.MapKeys() {
			newMap.SetMapIndex(key, walkAny(v.MapIndex(key), fn))
		}
		return newMap
	case reflect.Slice:
		newSlice := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			newSlice.Index(i).Set(walkAny(v.Index(i), fn))
		}
		return newSlice
	case reflect.Struct:
		cp := reflect.New(v.Type()).Elem()
		cp.Set(v)
		for i := 0; i < cp.NumField(); i++ {
			if f := cp.Field(i); f.CanSet() {
				walkValue(f, fn, false)
			}
		}
		return cp
	default:
		return v
	}
}

// propagateCooldown copies the dependency-owned min_release_age into the
// freshness lint module's options (its historical home + the resolver's read
// path), so freshness recommendations match what deps will apply. Dependency's
// value wins; when unset, any existing freshness-options value stands for
// back-compat.
func propagateCooldown(cfg *Config) {
	mra := strings.TrimSpace(cfg.Dependency.MinReleaseAge)
	if mra == "" {
		return
	}
	if cfg.Lint.Modules == nil {
		cfg.Lint.Modules = map[string]ModuleConfig{}
	}
	fm := cfg.Lint.Modules["freshness"]
	if fm.Options == nil {
		fm.Options = map[string]any{}
	}
	fm.Options["min_release_age"] = mra
	cfg.Lint.Modules["freshness"] = fm
}

// resolveValue is the single recursive traversal engine. Visits every reachable
// value in the config graph and resolves {var:} templates in all strings.
func resolveValue(v reflect.Value, vars map[string]string) {
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			resolveValue(v.Elem(), vars)
		}

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.CanSet() {
				resolveValue(field, vars)
			}
		}

	case reflect.String:
		if v.CanSet() {
			s := v.String()
			if strings.Contains(s, "{var:") {
				v.SetString(resolveTemplateVars(s, vars))
			}
		}

	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			resolveValue(v.Index(i), vars)
		}

	case reflect.Map:
		if v.IsNil() {
			return
		}
		for _, key := range v.MapKeys() {
			elem := v.MapIndex(key)
			// Map values aren't directly settable — resolve and replace.
			resolved := resolveAny(elem, vars)
			v.SetMapIndex(key, resolved)
		}

	case reflect.Interface:
		if !v.IsNil() {
			resolved := resolveAny(v.Elem(), vars)
			if v.CanSet() {
				v.Set(resolved)
			}
		}
	}
}

// resolveAny resolves {var:} in any reflect.Value and returns the resolved value.
// Used for map values and interface values that can't be set in-place.
func resolveAny(v reflect.Value, vars map[string]string) reflect.Value {
	// Unwrap interface.
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return v
		}
		inner := resolveAny(v.Elem(), vars)
		return inner
	}

	switch v.Kind() {
	case reflect.String:
		s := v.String()
		if strings.Contains(s, "{var:") {
			return reflect.ValueOf(resolveTemplateVars(s, vars))
		}
		return v

	case reflect.Map:
		// Rebuild map with resolved values.
		newMap := reflect.MakeMap(v.Type())
		for _, key := range v.MapKeys() {
			elem := v.MapIndex(key)
			resolved := resolveAny(elem, vars)
			newMap.SetMapIndex(key, resolved)
		}
		return newMap

	case reflect.Slice:
		newSlice := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			resolved := resolveAny(v.Index(i), vars)
			newSlice.Index(i).Set(resolved)
		}
		return newSlice

	case reflect.Struct:
		// Copy struct and resolve fields.
		cp := reflect.New(v.Type()).Elem()
		cp.Set(v)
		for i := 0; i < cp.NumField(); i++ {
			field := cp.Field(i)
			if field.CanSet() {
				resolveValue(field, vars)
			}
		}
		return cp

	default:
		return v
	}
}

// AssertNormalized verifies no unresolved {var:} templates remain in the config, and
// that no identity token ({org*}/{orgs.*}/{metadata.*}/{path.*}/{slug}) survived the
// identity pass outside the (opaque) governance payload — a survivor means a typo'd
// alias/field or a missing orgs/metadata declaration, and failing loudly here beats a
// malformed push ref or a forge 404 later. Hard failure — not a warning.
func AssertNormalized(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("normalization assertion failed: could not serialize config: %w", err)
	}
	if strings.Contains(string(data), "{var:") {
		return fmt.Errorf("normalization incomplete: unresolved {var:} template remains in config")
	}

	// Identity check on a governance-zeroed copy: the payload legitimately carries
	// satellite-destined tokens.
	noGov := *cfg
	noGov.Governance = GovernanceConfig{}
	data, err = yaml.Marshal(&noGov)
	if err != nil {
		return fmt.Errorf("normalization assertion failed: could not serialize config: %w", err)
	}
	s := string(data)
	for _, fam := range identityFamilies {
		if idx := strings.Index(s, fam); idx >= 0 {
			end := strings.IndexByte(s[idx:], '}')
			tok := fam
			if end >= 0 {
				tok = s[idx : idx+end+1]
			}
			return fmt.Errorf("normalization incomplete: unresolved identity token %q — check orgs:/metadata: declarations and alias spelling (identity resolves from them at load)", tok)
		}
	}
	return nil
}

// resolveTemplateVars replaces StageFreight {var:name} template placeholders
// using values from vars. Single-pass only; no recursion or nesting.
func resolveTemplateVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{var:"+k+"}", v)
	}
	return s
}
