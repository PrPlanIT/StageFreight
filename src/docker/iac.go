package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScanIaC walks the IaC directory and discovers all compose stacks.
// Directory convention: <iac_path>/<scope>/<stack>/
// DD-UI proven discovery patterns carried forward.
func ScanIaC(rootDir, iacPath string, knownHosts map[string]bool) ([]StackInfo, error) {
	base := filepath.Join(rootDir, iacPath)

	fi, err := os.Stat(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // nothing to scan
		}
		return nil, fmt.Errorf("stat iac path %s: %w", base, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", base)
	}

	var stacks []StackInfo

	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || !d.IsDir() {
			return nil
		}

		rel, _ := filepath.Rel(base, p)
		parts := strings.Split(filepath.ToSlash(rel), "/")

		// Only process stack directories: <scope>/<stack>
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == "." {
			return nil
		}

		scopeName := parts[0]
		stackName := parts[1]

		// Determine scope kind
		scopeKind := "group"
		if knownHosts[scopeName] {
			scopeKind = "host"
		}

		// Detect compose file
		composeFile := findComposeFile(p)
		deployKind := "unmanaged"
		if composeFile != "" {
			deployKind = "compose"
		}
		if hasScripts(p) && composeFile == "" {
			deployKind = "script"
		}

		// Detect env files
		envFiles := discoverEnvFiles(p)

		// Detect scripts
		scripts := discoverScripts(p)

		// Skip empty directories (no IaC content)
		if composeFile == "" && len(envFiles) == 0 && len(scripts) == 0 {
			return fs.SkipDir
		}

		stacks = append(stacks, StackInfo{
			Scope:       scopeName,
			ScopeKind:   scopeKind,
			Name:        stackName,
			Path:        filepath.ToSlash(filepath.Join(iacPath, scopeName, stackName)),
			ComposeFile: composeFile,
			EnvFiles:    envFiles,
			Scripts:     scripts,
			DeployKind:  deployKind,
		})

		return fs.SkipDir // don't recurse into stack subdirs
	})

	if err != nil {
		return nil, fmt.Errorf("walking iac directory: %w", err)
	}

	sort.Slice(stacks, func(i, j int) bool {
		if stacks[i].Scope != stacks[j].Scope {
			return stacks[i].Scope < stacks[j].Scope
		}
		return stacks[i].Name < stacks[j].Name
	})

	return stacks, nil
}

// findComposeFile returns the detected compose filename in a stack directory.
func findComposeFile(dir string) string {
	candidates := []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}
	for _, c := range candidates {
		if fi, err := os.Stat(filepath.Join(dir, c)); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// discoverEnvFiles finds .env and *_secret.env files in a stack directory.
func discoverEnvFiles(dir string) []EnvFile {
	var files []EnvFile
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == ".env" || strings.HasSuffix(name, ".env") {
			encrypted := strings.Contains(name, "_secret") || strings.Contains(name, "_private")
			files = append(files, EnvFile{
				Path:      name,
				FullPath:  filepath.Join(dir, name),
				Encrypted: encrypted,
			})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files
}

// discoverScripts finds deploy lifecycle scripts in a stack directory.
func discoverScripts(dir string) []string {
	scriptNames := []string{"pre.sh", "deploy.sh", "post.sh"}
	var found []string
	for _, s := range scriptNames {
		if fi, err := os.Stat(filepath.Join(dir, s)); err == nil && !fi.IsDir() {
			found = append(found, s)
		}
	}
	return found
}

func hasScripts(dir string) bool {
	for _, s := range []string{"pre.sh", "deploy.sh", "post.sh"} {
		if fi, err := os.Stat(filepath.Join(dir, s)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// ComputeBundleHash computes a deterministic SHA256 hash of all files in a stack.
// Files are sorted before hashing for determinism.
func ComputeBundleHash(stack StackInfo, rootDir string) string {
	h := sha256.New()
	stackDir := filepath.Join(rootDir, stack.Path)

	// Collect all relevant files, sorted
	var files []string
	if stack.ComposeFile != "" {
		files = append(files, stack.ComposeFile)
	}
	for _, ef := range stack.EnvFiles {
		files = append(files, ef.Path)
	}
	files = append(files, stack.Scripts...)
	sort.Strings(files)

	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(stackDir, f))
		if err != nil {
			continue
		}
		h.Write([]byte(f + "\n"))
		h.Write(data)
	}

	return hex.EncodeToString(h.Sum(nil))
}
