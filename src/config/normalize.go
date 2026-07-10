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
	cfg.Signing = NormalizeSigning(cfg.Signing)

	// The supply-chain cooldown is OWNED by dependency.min_release_age but consumed
	// by the freshness discovery resolver (which powers both the freshness lint
	// module AND dependency updates). Propagate the owned value into the freshness
	// module options — its historical home — so every consumer reads ONE value.
	// Like Signing above, this runs before the vars short-circuit.
	propagateCooldown(cfg)

	if len(cfg.Vars) == 0 {
		return nil
	}

	// Guard: vars must not contain nested templates (single-pass only).
	for k, v := range cfg.Vars {
		if strings.Contains(v, "{var:") {
			return fmt.Errorf("var %q contains nested {var:} template — not allowed", k)
		}
	}

	resolveValue(reflect.ValueOf(cfg), cfg.Vars)
	return nil
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

// AssertNormalized verifies no unresolved {var:} templates remain in the config.
// Hard failure — not a warning. If this fires, normalization has a bug.
func AssertNormalized(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("normalization assertion failed: could not serialize config: %w", err)
	}
	if strings.Contains(string(data), "{var:") {
		return fmt.Errorf("normalization incomplete: unresolved {var:} template remains in config")
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
