package docker

import (
	"context"
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

// ComputeBundleHash computes a deterministic SHA256 hash of a stack's rendered desired state.
// SOPS-encrypted files are decrypted in-memory before hashing — never persisted.
// Env files are normalized (sorted keys, trimmed whitespace, normalized line endings)
// for deterministic drift detection. Files are sorted before hashing.
//
// Pipeline: IaC → [SOPS decrypt] → normalize → hash → discard
func ComputeBundleHash(stack StackInfo, rootDir string, secrets SecretsProvider) string {
	h := sha256.New()
	stackDir := filepath.Join(rootDir, stack.Path)

	// Build ordered file list with encryption metadata
	type hashFile struct {
		name      string
		encrypted bool
		fullPath  string
	}
	var files []hashFile
	if stack.ComposeFile != "" {
		files = append(files, hashFile{name: stack.ComposeFile, fullPath: filepath.Join(stackDir, stack.ComposeFile)})
	}
	for _, ef := range stack.EnvFiles {
		files = append(files, hashFile{name: ef.Path, encrypted: ef.Encrypted, fullPath: ef.FullPath})
	}
	for _, s := range stack.Scripts {
		files = append(files, hashFile{name: s, fullPath: filepath.Join(stackDir, s)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	for _, f := range files {
		var data []byte
		var err error

		if f.encrypted && secrets != nil {
			// Decrypt in-memory — hash rendered desired state, not transport encoding.
			// Decrypted content is never persisted, logged, or cached.
			data, err = secrets.Decrypt(context.Background(), f.fullPath)
		} else {
			data, err = os.ReadFile(f.fullPath)
		}
		if err != nil {
			continue
		}

		// Normalize env files for deterministic hashing:
		// sort lines, trim whitespace, normalize line endings.
		if isEnvFile(f.name) {
			data = normalizeEnv(data)
		}

		h.Write([]byte(f.name + "\n"))
		h.Write(data)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// normalizeEnv sorts env file lines and normalizes whitespace for deterministic hashing.
// Blank lines and comments are preserved but trimmed. Key=value pairs are sorted.
func normalizeEnv(data []byte) []byte {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	// Separate comments/blanks from key=value pairs
	var kvLines, otherLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			otherLines = append(otherLines, trimmed)
		} else {
			kvLines = append(kvLines, trimmed)
		}
	}
	sort.Strings(kvLines)

	// Rejoin: comments first, then sorted kv pairs
	all := append(otherLines, kvLines...)
	return []byte(strings.Join(all, "\n") + "\n")
}

// isEnvFile returns true if the filename looks like an env file.
func isEnvFile(name string) bool {
	return name == ".env" || strings.HasSuffix(name, ".env")
}
