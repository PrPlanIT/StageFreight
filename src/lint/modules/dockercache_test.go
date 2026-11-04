package modules

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func cacheFindings(t *testing.T, dockerfile string, opts map[string]any) []lint.Finding {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &dockerCacheModule{volatile: defaultVolatileArgs}
	if opts != nil {
		if err := m.Configure(opts); err != nil {
			t.Fatal(err)
		}
	}
	f, err := m.Check(context.Background(), lint.FileInfo{Path: "Dockerfile", AbsPath: path})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func rules(fs []lint.Finding) map[string]int {
	out := map[string]int{}
	for _, f := range fs {
		out[f.RuleID+"@"+strconv.Itoa(f.Line)]++
	}
	return out
}

func TestDockerCache(t *testing.T) {
	cases := []struct {
		name, dockerfile string
		want             []string // RuleID@line
	}{
		{
			name: "source before install",
			dockerfile: `FROM docker.io/library/node:22-alpine
WORKDIR /app
COPY . .
RUN --mount=type=cache,target=/root/.npm npm ci
`,
			want: []string{"source-before-install@4"},
		},
		{
			name: "manifests first is fine",
			dockerfile: `FROM docker.io/library/node:22-alpine
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY . .
RUN --mount=type=cache,target=/root/.npm npm run build
`,
		},
		{
			name: "no cache mount",
			dockerfile: `FROM docker.io/library/golang:1.26-alpine
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /app ./cmd/app
`,
			want: []string{"no-cache-mount@3", "no-cache-mount@5"},
		},
		{
			name: "heredoc install after COPY .",
			dockerfile: `FROM docker.io/library/python:3.13-alpine
COPY . .
RUN --mount=type=cache,target=/root/.cache/pip <<EOF
pip install -r requirements.txt
EOF
`,
			want: []string{"source-before-install@3"},
		},
		{
			name: "COPY --from isn't the build context",
			dockerfile: `FROM docker.io/library/golang:1.26-alpine AS build
FROM docker.io/library/alpine:3.24
COPY --from=build . .
RUN --mount=type=cache,target=/root/.cache/pip pip install -r requirements.txt
`,
		},
		{
			name: "per-build ARG early",
			dockerfile: `FROM docker.io/library/alpine:3.24
ARG VERSION
RUN apk add --no-cache curl
RUN echo building
LABEL org.opencontainers.image.version=$VERSION
`,
			want: []string{"volatile-arg-early@2"},
		},
		{
			name: "per-build ARG used straight away",
			dockerfile: `FROM docker.io/library/alpine:3.24
RUN apk add --no-cache curl
ARG VERSION
RUN echo "${VERSION}" > /version
`,
		},
		{
			name: "ordinary ARG is never flagged",
			dockerfile: `FROM docker.io/library/alpine:3.24
ARG TARGETARCH
RUN apk add --no-cache curl
RUN echo $TARGETARCH
`,
		},
		{
			name: "per-build ARG never used",
			dockerfile: `FROM docker.io/library/alpine:3.24
ARG GIT_SHA
RUN apk add --no-cache curl
`,
			want: []string{"volatile-arg-early@2"},
		},
		{
			name: "each stage stands alone",
			dockerfile: `FROM docker.io/library/alpine:3.24 AS a
COPY . .
FROM docker.io/library/node:22-alpine
RUN --mount=type=cache,target=/root/.npm npm ci
`,
		},
		{
			name: "JSON form",
			dockerfile: `FROM docker.io/library/golang:1.26-alpine
RUN ["go", "build", "./..."]
`,
			want: []string{"no-cache-mount@2"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rules(cacheFindings(t, c.dockerfile, nil))
			want := map[string]int{}
			for _, w := range c.want {
				want[w]++
			}
			if len(got) != len(want) {
				t.Fatalf("findings %v, want %v", got, want)
			}
			for k := range want {
				if got[k] != want[k] {
					t.Errorf("findings %v, want %v", got, want)
				}
			}
		})
	}
}

// StageFreight's own Dockerfile copies manifests first, mounts its caches and declares
// its build ARGs late; it is the reference and must be clean.
func TestDockerCache_OwnDockerfileClean(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "Dockerfile"))
	if err != nil {
		t.Skip("repository Dockerfile not available")
	}
	if fs := cacheFindings(t, string(data), nil); len(fs) != 0 {
		t.Errorf("StageFreight's own Dockerfile has findings: %+v", fs)
	}
}

func TestDockerCache_Configure(t *testing.T) {
	fs := cacheFindings(t, "FROM docker.io/library/alpine:3.24\nARG RELEASE_TRAIN\nRUN true\n", map[string]any{
		"severity": "warning", "volatile_args": []string{"RELEASE_.*"},
	})
	if len(fs) != 1 || fs[0].Severity != lint.SeverityWarning || fs[0].RuleID != "volatile-arg-early" {
		t.Errorf("custom volatile arg at warning: %+v", fs)
	}
	m := &dockerCacheModule{}
	if err := m.Configure(map[string]any{"severity": "critical"}); err == nil {
		t.Error("advice can't be critical: it never fails a build")
	}
}
