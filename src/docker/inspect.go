package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// inspectLocalContainers runs docker inspect on local container IDs and
// normalizes into a StackInspection.
func inspectLocalContainers(ctx context.Context, t *LocalTransport, project string, ids []string) (StackInspection, error) {
	if len(ids) == 0 {
		return StackInspection{Project: project}, nil
	}

	args := append([]string{"inspect", "--format", "{{json .}}"}, ids...)
	r := t.execLocal(ctx, "docker", args...)
	if !r.Success {
		return StackInspection{}, fmt.Errorf("docker inspect: %s", strings.TrimSpace(r.Stderr))
	}

	return parseInspectOutput(project, r.Stdout)
}

// inspectRemoteContainers runs docker inspect on remote container IDs via SSH
// and normalizes into a StackInspection.
func inspectRemoteContainers(ctx context.Context, s *SSHTransport, project string, ids []string) (StackInspection, error) {
	if len(ids) == 0 {
		return StackInspection{Project: project}, nil
	}

	args := append([]string{"inspect", "--format", "{{json .}}"}, ids...)
	r := s.sshExecResult(ctx, "docker", args...)
	if !r.Success {
		return StackInspection{}, fmt.Errorf("docker inspect: %s", strings.TrimSpace(r.Stderr))
	}

	return parseInspectOutput(project, r.Stdout)
}

// dockerInspectResult is the minimal subset of docker inspect JSON we parse.
// Only the fields needed for Tier 2 drift — never parse the full blob.
type dockerInspectResult struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status   string `json:"Status"`
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
	} `json:"State"`
	Image string `json:"Image"` // image ID (sha256:...)
}

// parseInspectOutput parses docker inspect JSON output (one object per line)
// and normalizes into StackInspection.
func parseInspectOutput(project, output string) (StackInspection, error) {
	inspection := StackInspection{Project: project}

	// docker inspect with multiple IDs outputs a JSON array.
	var results []dockerInspectResult
	if err := json.Unmarshal([]byte(output), &results); err != nil {
		// Try line-by-line parsing as fallback.
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var r dockerInspectResult
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				continue
			}
			results = append(results, r)
		}
	}

	for _, r := range results {
		labels := r.Config.Labels

		// Extract Compose labels — the declared/runtime bridge.
		service := labels["com.docker.compose.service"]
		configHash := labels["com.docker.compose.config-hash"]

		inspection.Services = append(inspection.Services, ServiceRuntimeState{
			Service:     service,
			ConfigHash:  configHash,
			Image:       r.Config.Image,
			Running:     r.State.Running,
			State:       r.State.Status,
			ContainerID: r.ID,
		})
	}

	return inspection, nil
}
