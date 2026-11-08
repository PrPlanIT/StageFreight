package docker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

func TestStageDockerfile_ExcludesStageFreight(t *testing.T) {
	cases := []struct {
		name      string
		specific  string // <Dockerfile>.dockerignore, "" = absent
		context   string // <context>/.dockerignore, "" = absent
		wantRules string
	}{
		{"no ignore files", "", "", "\n.stagefreight\n"},
		{"context ignore", "", "node_modules\n", "node_modules\n\n.stagefreight\n"},
		{"dockerfile ignore wins", "dist\n", "node_modules\n", "dist\n\n.stagefreight\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.TempDir()
			df := filepath.Join(ctx, "apps", "api", "Dockerfile")
			if err := os.MkdirAll(filepath.Dir(df), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(df, []byte("FROM scratch\nCOPY . /ctx\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.specific != "" {
				if err := os.WriteFile(df+".dockerignore", []byte(tc.specific), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.context != "" {
				if err := os.WriteFile(filepath.Join(ctx, ".dockerignore"), []byte(tc.context), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadDir(ctx)

			staged, cleanup, err := stageDockerfile(build.BuildStep{Context: ctx, Dockerfile: df})
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(staged) != "Dockerfile" || filepath.Dir(staged) == filepath.Dir(df) {
				t.Fatalf("staged at %s, want a Dockerfile outside the repo", staged)
			}
			body, _ := os.ReadFile(staged)
			if string(body) != "FROM scratch\nCOPY . /ctx\n" {
				t.Fatalf("staged Dockerfile = %q", body)
			}
			rules, _ := os.ReadFile(staged + ".dockerignore")
			if string(rules) != tc.wantRules {
				t.Fatalf("ignore rules = %q, want %q", rules, tc.wantRules)
			}
			after, _ := os.ReadDir(ctx)
			if len(after) != len(before) {
				t.Fatalf("repo checkout changed: %d entries before, %d after", len(before), len(after))
			}

			cleanup()
			if _, err := os.Stat(filepath.Dir(staged)); !os.IsNotExist(err) {
				t.Fatalf("staging dir not removed: %v", err)
			}
		})
	}
}

func TestStageDockerfile_DefaultsToContextDockerfile(t *testing.T) {
	ctx := t.TempDir()
	if err := os.WriteFile(filepath.Join(ctx, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged, cleanup, err := stageDockerfile(build.BuildStep{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if body, _ := os.ReadFile(staged); string(body) != "FROM scratch\n" {
		t.Fatalf("staged Dockerfile = %q", body)
	}
}

func TestStageDockerfile_MissingDockerfileFails(t *testing.T) {
	if _, _, err := stageDockerfile(build.BuildStep{Context: t.TempDir()}); err == nil {
		t.Fatal("want an error for a missing Dockerfile")
	}
}
