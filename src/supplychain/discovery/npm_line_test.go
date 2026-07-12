package discovery

import "testing"

// A dependency whose name also appears as a bare script value (the canonical
// `"dev": "vite"`) must anchor to its devDependencies entry, not the earlier
// script line. Mis-anchoring misreports the finding line AND makes the npm
// auto-bump read a script value as the version spec, skipping the update.
func TestFindLineForJSON_SectionScopedNotScriptValue(t *testing.T) {
	pkg := `{
  "name": "@vdisplay/desktop",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "pack": "electron-builder build --win portable"
  },
  "dependencies": {
    "react": "^18.2.0",
    "react-dom": "^18.2.0"
  },
  "devDependencies": {
    "electron": "31.7.7",
    "vite": "^5.4.1"
  }
}`
	lines := buildLineIndex([]byte(pkg))

	cases := []struct {
		section, key string
		want         int // 1-indexed line
	}{
		{"devDependencies", "vite", 14},     // NOT line 4 ("dev": "vite") or 5 ("build": "vite build")
		{"devDependencies", "electron", 13}, // NOT the "pack" script that mentions electron-builder
		{"dependencies", "react", 9},        // NOT react-dom on line 10
		{"dependencies", "react-dom", 10},
	}
	for _, c := range cases {
		if got := findLineForJSON(lines, c.section, c.key); got != c.want {
			t.Errorf("findLineForJSON(%q, %q) = %d, want %d", c.section, c.key, got, c.want)
		}
	}
}
