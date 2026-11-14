package opentofu

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// FmtCheck runs `fmt -check -list=true` over files (repo-relative) and returns the
// files that are not in canonical format.
func FmtCheck(ctx context.Context, tool string, want map[string]config.ToolConstraint, rootDir string, files []string) ([]string, error) {
	res, err := resolveTool(ctx, rootDir, tool, want)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	args := append([]string{"fmt", "-check", "-list=true"}, files...)
	code, err := execRun(ctx, res.Path, rootDir, os.Environ(), args, &out)
	if err != nil {
		return nil, err
	}
	// fmt exits 3 when a file needs formatting; any other nonzero is a failure to run.
	if code != 0 && code != 3 {
		return nil, fmt.Errorf("fmt exited %d: %s", code, tail(out.String(), 500))
	}
	var unformatted []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			unformatted = append(unformatted, line)
		}
	}
	return unformatted, nil
}

// Validate initializes rootPath without its state backend and runs `validate -json`,
// returning the JSON. The data directory is temporary, keeping .terraform/ out of the
// checkout; the dependency lock file is read, never written.
func Validate(ctx context.Context, tool string, want map[string]config.ToolConstraint, rootDir, rootPath string) ([]byte, error) {
	res, err := resolveTool(ctx, rootDir, tool, want)
	if err != nil {
		return nil, err
	}
	dataDir, err := os.MkdirTemp("", "sf-opentofu-validate-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dataDir)

	dir := filepath.Join(rootDir, rootPath)
	env := append(os.Environ(), "TF_DATA_DIR="+dataDir, "TF_IN_AUTOMATION=1", "TF_INPUT=0")
	var initOut bytes.Buffer
	code, err := execRun(ctx, res.Path, dir, env, []string{"init", "-backend=false", "-input=false", "-lockfile=readonly"}, &initOut)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("init exited %d: %s", code, tail(initOut.String(), 500))
	}
	var out bytes.Buffer
	if _, err := execRun(ctx, res.Path, dir, env, []string{"validate", "-json"}, &out); err != nil {
		return nil, err
	}
	// validate exits nonzero when the configuration is invalid; the JSON carries why.
	return out.Bytes(), nil
}
