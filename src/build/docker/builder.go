package docker

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// BuilderInfo holds structured builder state for narration.
type BuilderInfo struct {
	Name              string
	Driver            string
	Status            string // "running", "stopped", "not found"
	Action            string // "reused", "created"
	BuildKit          string // version
	Platforms         string
	Endpoint          string
	BootstrapOK       bool
	BootstrapDuration time.Duration
	GCRules           []GCRule
	RawOutput         string // fallback if parsing fails
	ParseFailed       bool
}

// GCRule is a parsed BuildKit garbage collection policy rule.
type GCRule struct {
	Scope        string // "source/cachemount/git", "general cache", etc.
	All          bool
	KeepDuration string
	MaxUsed      string
	Reserved     string
	MinFree      string
}

// EnsureBuilder creates the Docker context and buildx builder from config,
// bootstraps it, and writes builder.json. This is the single authority for
// builder lifecycle — the skeleton is transport only (env vars + DinD service).
func EnsureBuilder(cfg config.BuilderConfig) BuilderInfo {
	return EnsureBuilderWithBackend(cfg, nil)
}

// EnsureBuilderWithBackend creates a builder using the given backend.
// If backend is nil, resolves one automatically.
func EnsureBuilderWithBackend(cfg config.BuilderConfig, backend *Backend) BuilderInfo {
	name := cfg.BuilderName()
	driver := cfg.BuilderDriver()
	info := BuilderInfo{Name: name, Driver: driver}

	// Resolve backend if not provided, respecting config override.
	if backend == nil {
		resolved, err := ResolveBackendWithConfig(BackendCapabilities{Build: true}, cfg.Backend)
		if err != nil {
			info.Status = "no backend available"
			info.RawOutput = err.Error()
			info.ParseFailed = true
			return info
		}
		backend = resolved
	}

	if backend.IsBuildkit() {
		info.Driver = "remote"
		result := ensureRemoteBuilder(name, backend.Endpoint, info)
		if result.BootstrapOK {
			return result
		}
		fmt.Fprintf(os.Stderr, "builder: buildkitd bootstrap failed, trying DinD\n")
		// Fall through to docker-container driver.
	}

	// DinD or local docker — docker-container driver.
	info.Driver = driver

	// DinD or local docker — use docker-container driver with context.
	return ensureDockerContainerBuilder(name, driver, cfg.ContextName(), info)
}

// ensureRemoteBuilder connects to an external buildkitd via the remote driver.
// The daemon is persistent — mount caches survive across builds.
func ensureRemoteBuilder(name, endpoint string, info BuilderInfo) BuilderInfo {
	// Try to reuse existing remote builder.
	bootstrapStart := time.Now()
	bootstrapOut, bootstrapErr := exec.Command("docker", "buildx", "inspect", "--bootstrap", name).CombinedOutput()
	info.BootstrapDuration = time.Since(bootstrapStart)
	info.BootstrapOK = bootstrapErr == nil
	info.RawOutput = string(bootstrapOut)

	if bootstrapErr == nil {
		info.Action = "reused"
		exec.Command("docker", "buildx", "use", name).CombinedOutput()
	} else {
		// Create remote builder pointing at buildkitd.
		// BuildKit has its own PKI — independent from DinD trust domain.
		// Client certs at BUILDKIT_CERT_PATH (default: /buildkit-certs).
		exec.Command("docker", "buildx", "rm", name).CombinedOutput()
		createArgs := []string{"buildx", "create", "--name", name, "--driver", "remote", "--use"}

		bkCertPath := os.Getenv("BUILDKIT_CERT_PATH")
		if bkCertPath == "" {
			bkCertPath = "/buildkit-certs"
		}
		caPath := fmt.Sprintf("%s/ca.pem", bkCertPath)
		certFile := fmt.Sprintf("%s/cert.pem", bkCertPath)
		keyFile := fmt.Sprintf("%s/key.pem", bkCertPath)

		// TLS is mandatory when BUILDKIT_HOST is set. No silent fallback to insecure.
		for _, f := range []string{caPath, certFile, keyFile} {
			if _, err := os.Stat(f); err != nil {
				info.Status = "buildkit TLS misconfigured"
				info.RawOutput = fmt.Sprintf("missing %s — BUILDKIT_HOST requires TLS certs at %s", f, bkCertPath)
				info.ParseFailed = true
				return info
			}
		}
		createArgs = append(createArgs,
			"--driver-opt", "cacert="+caPath,
			"--driver-opt", "cert="+certFile,
			"--driver-opt", "key="+keyFile,
		)
		createArgs = append(createArgs, endpoint)

		if out, err := exec.Command("docker", createArgs...).CombinedOutput(); err != nil {
			info.Status = "remote builder creation failed"
			info.RawOutput = string(out)
			info.ParseFailed = true
			return info
		}
		info.Action = "created"

		bootstrapStart = time.Now()
		bootstrapOut, bootstrapErr = exec.Command("docker", "buildx", "inspect", "--bootstrap", name).CombinedOutput()
		info.BootstrapDuration = time.Since(bootstrapStart)
		info.BootstrapOK = bootstrapErr == nil
		info.RawOutput = string(bootstrapOut)
	}

	info.Endpoint = endpoint
	writeBuilderRecord(name, "remote", info.Action)
	return info
}

// ensureDockerContainerBuilder creates a builder using the docker-container driver.
// Used when BUILDKIT_HOST is not set (DinD or local docker).
func ensureDockerContainerBuilder(name, driver, ctxName string, info BuilderInfo) BuilderInfo {
	dockerHost := os.Getenv("DOCKER_HOST")
	certPath := os.Getenv("DOCKER_CERT_PATH")
	if (dockerHost == "") != (certPath == "") {
		info.Status = "partial remote docker configuration"
		info.RawOutput = fmt.Sprintf("DOCKER_HOST=%q DOCKER_CERT_PATH=%q — need both for remote TLS or neither for local docker", dockerHost, certPath)
		info.ParseFailed = true
		return info
	}
	useContext := dockerHost != "" && certPath != ""

	if useContext {
		exec.Command("docker", "context", "rm", ctxName).CombinedOutput()
		contextArg := fmt.Sprintf("host=%s,ca=%s/ca.pem,cert=%s/cert.pem,key=%s/key.pem",
			dockerHost, certPath, certPath, certPath)
		if out, err := exec.Command("docker", "context", "create", ctxName,
			"--docker", contextArg).CombinedOutput(); err != nil {
			info.Status = "context creation failed"
			info.RawOutput = string(out)
			info.ParseFailed = true
			return info
		}
	} else {
		fmt.Fprintf(os.Stderr, "builder: using default docker context (no DOCKER_HOST/DOCKER_CERT_PATH)\n")
	}

	// Try to reuse existing builder — mount caches persist inside BuildKit container.
	bootstrapStart := time.Now()
	bootstrapOut, bootstrapErr := exec.Command("docker", "buildx", "inspect", "--bootstrap", name).CombinedOutput()
	info.BootstrapDuration = time.Since(bootstrapStart)
	info.BootstrapOK = bootstrapErr == nil
	info.RawOutput = string(bootstrapOut)

	if bootstrapErr == nil {
		info.Action = "reused"
		exec.Command("docker", "buildx", "use", name).CombinedOutput()
	} else {
		exec.Command("docker", "buildx", "rm", name).CombinedOutput()
		createArgs := []string{"buildx", "create", "--name", name, "--driver", driver, "--use"}
		if useContext {
			createArgs = append(createArgs, ctxName)
		}
		if out, err := exec.Command("docker", createArgs...).CombinedOutput(); err != nil {
			info.Status = "builder creation failed"
			info.RawOutput = string(out)
			info.ParseFailed = true
			return info
		}
		info.Action = "recreated"

		bootstrapStart = time.Now()
		bootstrapOut, bootstrapErr = exec.Command("docker", "buildx", "inspect", "--bootstrap", name).CombinedOutput()
		info.BootstrapDuration = time.Since(bootstrapStart)
		info.BootstrapOK = bootstrapErr == nil
		info.RawOutput = string(bootstrapOut)
	}

	// Write builder.json — engine is the authority, not shell glue.
	writeBuilderRecord(name, driver, info.Action)

	return info
}

// ResolveBuilderInfo inspects the active builder and returns structured facts.
// Read-only — does NOT create or bootstrap. Call EnsureBuilder first.
func ResolveBuilderInfo(info BuilderInfo) BuilderInfo {
	name := info.Name
	if name == "" || name == "(default)" {
		// Fallback: read from builder.json if EnsureBuilder wasn't called.
		if recordBytes, err := os.ReadFile(".stagefreight/runtime/docker/builder.json"); err == nil {
			var record struct {
				Name   string `json:"name"`
				Action string `json:"action"`
			}
			if json.Unmarshal(recordBytes, &record) == nil {
				name = record.Name
				if info.Action == "" {
					info.Action = record.Action
				}
			}
		}
	}
	if name == "" {
		name = "(default)"
	}
	info.Name = name

	// Inspect for structured facts (no bootstrap — that's EnsureBuilder's job).
	out, err := exec.Command("docker", "buildx", "inspect", name).CombinedOutput()
	if err != nil {
		info.Status = "not found"
		info.ParseFailed = true
		return info
	}

	text := string(out)
	var currentRule *GCRule
	foundDriver := false

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Driver:"):
			info.Driver = strings.TrimSpace(strings.TrimPrefix(line, "Driver:"))
			foundDriver = true
		case strings.HasPrefix(line, "Status:"):
			info.Status = strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
		case strings.HasPrefix(line, "BuildKit version:"):
			info.BuildKit = strings.TrimSpace(strings.TrimPrefix(line, "BuildKit version:"))
		case strings.HasPrefix(line, "Platforms:"):
			info.Platforms = strings.TrimSpace(strings.TrimPrefix(line, "Platforms:"))
		case strings.HasPrefix(line, "Endpoint:"):
			info.Endpoint = strings.TrimSpace(strings.TrimPrefix(line, "Endpoint:"))
		case strings.HasPrefix(line, "GC Policy rule#"):
			if currentRule != nil {
				info.GCRules = append(info.GCRules, *currentRule)
			}
			currentRule = &GCRule{}
		case currentRule != nil && strings.HasPrefix(line, "All:"):
			currentRule.All = strings.TrimSpace(strings.TrimPrefix(line, "All:")) == "true"
		case currentRule != nil && strings.HasPrefix(line, "Filters:"):
			currentRule.Scope = strings.TrimSpace(strings.TrimPrefix(line, "Filters:"))
		case currentRule != nil && strings.HasPrefix(line, "Keep Duration:"):
			currentRule.KeepDuration = strings.TrimSpace(strings.TrimPrefix(line, "Keep Duration:"))
		case currentRule != nil && strings.HasPrefix(line, "Max Used Space:"):
			currentRule.MaxUsed = strings.TrimSpace(strings.TrimPrefix(line, "Max Used Space:"))
		case currentRule != nil && strings.HasPrefix(line, "Reserved Space:"):
			currentRule.Reserved = strings.TrimSpace(strings.TrimPrefix(line, "Reserved Space:"))
		case currentRule != nil && strings.HasPrefix(line, "Min Free Space:"):
			currentRule.MinFree = strings.TrimSpace(strings.TrimPrefix(line, "Min Free Space:"))
		}
	}
	if currentRule != nil {
		info.GCRules = append(info.GCRules, *currentRule)
	}

	// Parse quality check — if critical fields are missing, mark as failed.
	if !foundDriver || info.Status == "" || info.Endpoint == "" {
		info.ParseFailed = true
	}

	return info
}

// RenderBuilderInfo prints structured builder state.
// Falls back to raw output if parsing failed.
func RenderBuilderInfo(w io.Writer, color bool, info BuilderInfo) {
	sec := output.NewSection(w, "Builder", info.BootstrapDuration, color)

	if info.ParseFailed {
		sec.Row("%-14s%s", "status", "parse failed — raw output below")
		if info.RawOutput != "" {
			for _, line := range strings.Split(strings.TrimSpace(info.RawOutput), "\n") {
				sec.Row("  %s", line)
			}
		}
		sec.Close()
		return
	}

	sec.Row("%-14s%s", "builder", info.Name)
	if info.Driver != "" {
		sec.Row("%-14s%s", "driver", info.Driver)
	}
	if info.Endpoint != "" {
		sec.Row("%-14s%s", "endpoint", info.Endpoint)
	}
	sec.Row("%-14s%s", "status", info.Status)
	if info.Action != "" {
		sec.Row("%-14s%s", "action", info.Action)
	}

	// Bootstrap result.
	if info.BootstrapOK {
		sec.Row("%-14s%s %s", "bootstrap", output.StatusIcon("success", color), formatDuration(info.BootstrapDuration))
	} else {
		sec.Row("%-14s%s failed", "bootstrap", output.StatusIcon("failed", color))
	}

	if info.BuildKit != "" {
		sec.Row("%-14s%s", "buildkit", info.BuildKit)
	}
	if info.Platforms != "" {
		sec.Row("%-14s%s", "platforms", info.Platforms)
	}

	// GC policy summary.
	if len(info.GCRules) > 0 {
		sec.Row("")
		sec.Row("gc policy")
		for _, rule := range info.GCRules {
			scope := rule.Scope
			if scope == "" {
				if rule.All {
					scope = "all (fallback)"
				} else {
					scope = "general cache"
				}
			} else {
				scope = strings.ReplaceAll(scope, "type==source.local,type==exec.cachemount,type==source.git.checkout", "source/cachemount/git")
			}
			parts := []string{}
			if rule.KeepDuration != "" {
				parts = append(parts, fmt.Sprintf("keep %s", rule.KeepDuration))
			}
			if rule.MaxUsed != "" {
				parts = append(parts, fmt.Sprintf("max %s", rule.MaxUsed))
			}
			if rule.MinFree != "" {
				parts = append(parts, fmt.Sprintf("min free %s", rule.MinFree))
			}
			sec.Row("  %-34s %s", scope, strings.Join(parts, "  "))
		}
	}

	sec.Close()
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// writeBuilderRecord writes the authoritative builder.json.
// Engine-owned — not shell glue.
func writeBuilderRecord(name, driver, action string) {
	dir := filepath.Join(".stagefreight", "runtime", "docker")
	os.MkdirAll(dir, 0o755)

	record := struct {
		Name    string `json:"name"`
		Action  string `json:"action"`
		Driver  string `json:"driver"`
	}{
		Name:   name,
		Action: action,
		Driver: driver,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(dir, "builder.json"), append(data, '\n'), 0o644)
}
