package ansible

import (
	"bytes"
	"context"
	"fmt"
)

// Lint runs ansible-lint over the declared play library where the plays run and
// returns its JSON report. Exit code 2 (violations found) is a successful run.
func Lint(ctx context.Context, image, rootDir string, playbooks []string) ([]byte, error) {
	return lint(ctx, probeSubstrate(), image, rootDir, playbooks)
}

func lint(ctx context.Context, sub substrate, image, rootDir string, playbooks []string) ([]byte, error) {
	rt, err := selectRuntime(sub, "ansible-lint", image)
	if err != nil {
		return nil, err
	}
	args := append([]string{"--format", "json", "--nocolor"}, playbooks...)

	var stdout, stderr bytes.Buffer
	var code int
	switch rt {
	case RuntimeImage:
		code, err = imageRun{
			dockerBin: sub.dockerBin,
			image:     image,
			workspace: rootDir,
			script:    "cd " + containerWorkdir + " && exec " + shellJoin(append([]string{"ansible-lint"}, args...)),
			stdout:    &stdout,
			stderr:    &stderr,
		}.run(ctx)
	case RuntimeNative:
		code, err = nativeRun{
			bin:       sub.look("ansible-lint"),
			args:      args,
			workspace: rootDir,
			stdout:    &stdout,
			stderr:    &stderr,
		}.run(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("ansible-lint (%s): %w", rt, err)
	}
	switch code {
	case 0, 2:
		return stdout.Bytes(), nil
	default:
		return nil, fmt.Errorf("ansible-lint (%s) exited %d: %s", rt, code, firstLine(stderr.String()))
	}
}
