package dependency

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// jsRequirers reads a lockfile and returns, for name, each package that requires it
// with the range it asks for ("query-string@9.3.1 wants ^0.4.1") — what an operator needs
// to decide a held remediation. pnpm records resolved versions rather than ranges, so its
// entries name the version required. Unreadable lockfiles yield nothing.
func jsRequirers(lockPath, manager, name string) []string {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return nil
	}
	var out []string
	switch manager {
	case jsManagerNpm:
		var lock struct {
			Packages map[string]struct {
				Version              string            `json:"version"`
				Dependencies         map[string]string `json:"dependencies"`
				OptionalDependencies map[string]string `json:"optionalDependencies"`
				PeerDependencies     map[string]string `json:"peerDependencies"`
			} `json:"packages"`
		}
		if json.Unmarshal(data, &lock) != nil {
			return nil
		}
		for key, p := range lock.Packages {
			for _, deps := range []map[string]string{p.Dependencies, p.OptionalDependencies, p.PeerDependencies} {
				if r, ok := deps[name]; ok {
					out = append(out, requirer(npmLockName(key), p.Version, r))
				}
			}
		}
	case jsManagerYarn:
		var lock map[string]struct {
			Version      string            `yaml:"version"`
			Dependencies map[string]string `yaml:"dependencies"`
		}
		if yaml.Unmarshal(data, &lock) != nil {
			return nil
		}
		for descriptor, p := range lock {
			if r, ok := p.Dependencies[name]; ok {
				out = append(out, requirer(yarnDescriptorName(descriptor), p.Version, strings.TrimPrefix(r, "npm:")))
			}
		}
	case jsManagerPnpm:
		var lock struct {
			Snapshots map[string]struct {
				Dependencies map[string]string `yaml:"dependencies"`
			} `yaml:"snapshots"`
		}
		if yaml.Unmarshal(data, &lock) != nil {
			return nil
		}
		for key, p := range lock.Snapshots {
			if r, ok := p.Dependencies[name]; ok {
				id, _, _ := strings.Cut(key, "(")
				out = append(out, id+" wants "+stripPeerSuffix(r))
			}
		}
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

func requirer(name, version, wants string) string {
	who := name
	if who == "" {
		who = "the project"
	} else if version != "" {
		who += "@" + version
	}
	return who + " wants " + wants
}

// npmLockName turns a package-lock key ("node_modules/a/node_modules/@s/b") into the
// package name ("@s/b"); the root entry ("") is the project itself.
func npmLockName(key string) string {
	const p = "node_modules/"
	if i := strings.LastIndex(key, p); i >= 0 {
		return key[i+len(p):]
	}
	return ""
}

// yarnDescriptorName returns the package name of a yarn.lock entry key
// ("@s/b@npm:^1, @s/b@npm:^2" → "@s/b"); the project's own workspace entry is "".
func yarnDescriptorName(descriptor string) string {
	first, _, _ := strings.Cut(descriptor, ",")
	first = strings.TrimSpace(first)
	at := strings.Index(first[1:], "@")
	if at < 0 {
		return first
	}
	name := first[:at+1]
	if strings.Contains(first[at+1:], "@workspace:") {
		return ""
	}
	return name
}

func stripPeerSuffix(v string) string {
	v, _, _ = strings.Cut(v, "(")
	return v
}

func dedupeStrings(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}
