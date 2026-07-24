package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// MigrateToLatest takes raw YAML data and migrates it to the current schema version.
// Returns the migrated YAML bytes ready for writing.
//
// Migration chain:
//
//	version 1 → current (no-op, already latest)
//
// Future schema changes should add migration steps here (e.g., v1→v2).
// The old pre-version config was an unversioned alpha that never earned a schema
// number — it is not convertible and is not supported by this migration path.
func MigrateToLatest(data []byte) ([]byte, error) {
	ver, err := peekVersion(data)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	switch ver {
	case 1:
		// Already at the latest schema version — nothing to do.
		return data, nil
	case 0:
		return nil, fmt.Errorf("migrate: config has no version field; the pre-version config format is not supported — please rewrite your config using version: 1")
	default:
		return nil, fmt.Errorf("migrate: unknown config version %d (latest supported: 1)", ver)
	}
}

// peekVersion extracts the version field from raw YAML without full parsing.
// Returns 0 if no version field is present.
func peekVersion(data []byte) (int, error) {
	// Quick parse — only need the version field.
	var probe struct {
		Version int `yaml:"version"`
	}

	// Use a lenient decoder (no KnownFields) since we only care about version.
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return 0, fmt.Errorf("reading version: %w", err)
	}

	return probe.Version, nil
}
