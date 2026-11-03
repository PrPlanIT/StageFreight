package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/runner"
)

// versionScript is a stand-in binary: `version` prints ver, anything else fails.
func versionScript(ver string) string {
	return "#!/bin/sh\n[ \"$1\" = version ] && echo '" + ver + "' && exit 0\nexit 2\n"
}

// crucibleImage builds an image holding body at path (mode 0755), labelled with labels.
func crucibleImage(t *testing.T, path, body string, labels map[string]string) v1.Image {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if path != "" {
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf.Bytes())), nil })
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}
	cf, _ := img.ConfigFile()
	cf = cf.DeepCopy()
	cf.Config.Labels = labels
	img, err = mutate.ConfigFile(img, cf)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// writeLayout writes images as an OCI layout index, optionally with attestation manifests.
func writeLayout(t *testing.T, imgs []v1.Image, attestation bool) string {
	t.Helper()
	dir := t.TempDir()
	var adds []mutate.IndexAddendum
	for _, img := range imgs {
		adds = append(adds, mutate.IndexAddendum{Add: img, Descriptor: v1.Descriptor{
			Platform: &v1.Platform{OS: "linux", Architecture: "amd64"},
		}})
	}
	if attestation {
		adds = append(adds, mutate.IndexAddendum{
			Add:        crucibleImage(t, "", "", nil),
			Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"}},
		})
	}
	if _, err := layout.Write(dir, mutate.AppendManifests(empty.Index, adds...)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func planLabels(hash string) map[string]string {
	return map[string]string{build.LabelPlanHash: hash}
}

func check(v *CrucibleVerification, name string) CrucibleCheck {
	for _, c := range append(append([]CrucibleCheck{}, v.ArtifactChecks...), v.ExecutionChecks...) {
		if c.Name == name {
			return c
		}
	}
	return CrucibleCheck{}
}

func TestVerifyCrucible_FromLayouts(t *testing.T) {
	ctx := context.Background()
	same := crucibleImage(t, crucibleBinaryPath, versionScript("stagefreight 1.2.3"), planLabels("plan-a"))

	t.Run("identical passes are reproducible", func(t *testing.T) {
		v, err := VerifyCrucible(ctx, writeLayout(t, []v1.Image{same}, true), writeLayout(t, []v1.Image{same}, true), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"binary hash", "version", "image digest", "build graph", "env fingerprint"} {
			if got := check(v, name).Status; got != "match" {
				t.Errorf("%s = %q, want match", name, got)
			}
		}
		if v.TrustLevel != build.TrustReproducible || v.HasHardFailure() {
			t.Fatalf("trust = %q (hard failure %v), want reproducible", v.TrustLevel, v.HasHardFailure())
		}
		if got := check(v, "version").Detail; got != "stagefreight 1.2.3" {
			t.Fatalf("version detail = %q — the extracted binary must actually run", got)
		}
	})

	t.Run("binary differs with same version and graph is consistent", func(t *testing.T) {
		other := crucibleImage(t, crucibleBinaryPath, versionScript("stagefreight 1.2.3")+"# rebuilt\n", planLabels("plan-a"))
		v, err := VerifyCrucible(ctx, writeLayout(t, []v1.Image{same}, false), writeLayout(t, []v1.Image{other}, false), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if check(v, "binary hash").Status != "differs" || v.TrustLevel != build.TrustConsistent || v.HasHardFailure() {
			t.Fatalf("binary=%q trust=%q hard=%v, want differs/consistent/false",
				check(v, "binary hash").Status, v.TrustLevel, v.HasHardFailure())
		}
	})

	t.Run("version differs is a hard failure", func(t *testing.T) {
		other := crucibleImage(t, crucibleBinaryPath, versionScript("stagefreight 9.9.9"), planLabels("plan-a"))
		v, err := VerifyCrucible(ctx, writeLayout(t, []v1.Image{same}, false), writeLayout(t, []v1.Image{other}, false), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if !v.HasHardFailure() || v.TrustLevel != build.TrustViable {
			t.Fatalf("version mismatch must hard-fail; trust=%q hard=%v", v.TrustLevel, v.HasHardFailure())
		}
	})

	t.Run("missing plan label is a hard failure", func(t *testing.T) {
		unlabelled := crucibleImage(t, crucibleBinaryPath, versionScript("stagefreight 1.2.3"), nil)
		v, err := VerifyCrucible(ctx, writeLayout(t, []v1.Image{same}, false), writeLayout(t, []v1.Image{unlabelled}, false), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if c := check(v, "build graph"); c.Status != "missing" || !c.IsHardFailure() {
			t.Fatalf("build graph = %+v, want a missing hard failure", c)
		}
	})

	// Every unreadable artifact is an error.
	failures := map[string]string{
		"no binary in the image": writeLayout(t, []v1.Image{crucibleImage(t, "usr/bin/other", "x", planLabels("plan-a"))}, false),
		"binary does not run":    writeLayout(t, []v1.Image{crucibleImage(t, crucibleBinaryPath, "#!/bin/sh\nexit 1\n", planLabels("plan-a"))}, false),
		"two runnable images":    writeLayout(t, []v1.Image{same, crucibleImage(t, crucibleBinaryPath, versionScript("x"), nil)}, false),
		"not a layout":           t.TempDir(),
	}
	for name, bad := range failures {
		t.Run(name+" is an error", func(t *testing.T) {
			if _, err := VerifyCrucible(ctx, writeLayout(t, []v1.Image{same}, false), bad, t.TempDir()); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// Verification reaches the same verdict with or without a Docker daemon and never runs `docker`.
func TestVerifyCrucible_NoDaemonOnEitherRunnerKind(t *testing.T) {
	img := crucibleImage(t, crucibleBinaryPath, versionScript("stagefreight 1.2.3"), planLabels("plan-a"))
	l1, l2 := writeLayout(t, []v1.Image{img}, true), writeLayout(t, []v1.Image{img}, true)

	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "docker-called")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	runnerKinds := map[string]map[string]string{
		"docker daemon":   {"DOCKER_HOST": "unix:///var/run/docker.sock", "BUILDKIT_HOST": ""},
		"remote buildkit": {"DOCKER_HOST": "", "BUILDKIT_HOST": "tcp://buildkitd:1234"},
	}
	var verdicts []*CrucibleVerification
	for kind, env := range runnerKinds {
		t.Run(kind, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			v, err := VerifyCrucible(context.Background(), l1, l2, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			verdicts = append(verdicts, v)
		})
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("verification invoked docker")
	}
	if len(verdicts) != 2 || !reflect.DeepEqual(verdicts[0], verdicts[1]) {
		t.Fatalf("verdicts differ between runner kinds: %+v", verdicts)
	}
}

func TestVerifyCrucibleSteps(t *testing.T) {
	ctx := context.Background()
	good := crucibleImage(t, crucibleBinaryPath, versionScript("stagefreight 1.2.3"), planLabels("plan-a"))
	rebuilt := crucibleImage(t, crucibleBinaryPath, versionScript("stagefreight 1.2.3")+"# rebuilt\n", planLabels("plan-a"))
	plan := &build.BuildPlan{Steps: []build.BuildStep{{Name: "api"}, {Name: "worker"}}}

	v, err := verifyCrucibleSteps(ctx, plan,
		[]string{writeLayout(t, []v1.Image{good}, false), writeLayout(t, []v1.Image{good}, false)},
		[]string{writeLayout(t, []v1.Image{good}, false), writeLayout(t, []v1.Image{rebuilt}, false)},
		t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// One step reproducible, one only consistent: the run is as strong as its weakest image.
	if v.TrustLevel != build.TrustConsistent {
		t.Fatalf("trust = %q, want consistent (the weakest step)", v.TrustLevel)
	}
	if !strings.HasPrefix(check(v, "binary hash").Detail, "api: ") {
		t.Fatalf("multi-step checks must name their step, got %q", check(v, "binary hash").Detail)
	}

	if _, err := verifyCrucibleSteps(ctx, plan, []string{"one"}, []string{"one", "two"}, t.TempDir()); err == nil {
		t.Fatal("a step without both layouts must be an error")
	}
}

// Both passes export OCI layouts only — no --load, no --push — on either runner kind.
func TestCrucibleBuildPassPlan(t *testing.T) {
	plan := &build.BuildPlan{Steps: []build.BuildStep{{
		Name: "stagefreight", Output: build.OutputImage, Platforms: []string{"linux/amd64"},
		Tags: []string{"v1"}, Load: true, Push: true,
		Registries: []build.RegistryTarget{{URL: "docker.io", Path: "prplanit/stagefreight", Tags: []string{"v1"}}},
		CacheTo:    []build.CacheRef{{Type: "registry", Ref: "docker.io/prplanit/cache"}},
	}}}
	dir := t.TempDir()

	for kind, env := range map[string]map[string]string{
		"docker daemon":   {"DOCKER_HOST": "unix:///var/run/docker.sock", "BUILDKIT_HOST": ""},
		"remote buildkit": {"DOCKER_HOST": "", "BUILDKIT_HOST": "tcp://buildkitd:1234"},
	} {
		t.Run(kind, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			pass, layouts, err := crucibleBuildPassPlan(plan, "stagefreight/crucible-candidate:run", filepath.Join(dir, kind))
			if err != nil {
				t.Fatal(err)
			}
			step := pass.Steps[0]
			if step.Load || step.Push || step.Registries != nil || step.CacheTo != nil {
				t.Fatalf("pass step must only export a layout: %+v", step)
			}
			if len(layouts) != 1 || step.OCILayoutDir != layouts[0] {
				t.Fatalf("layouts = %v, step dir = %q", layouts, step.OCILayoutDir)
			}
			if fi, err := os.Stat(layouts[0]); err != nil || !fi.IsDir() {
				t.Fatalf("layout dir not created: %v", err)
			}
			args := NewBuildx(false).buildArgs(step)
			if slices.Contains(args, "--load") || slices.Contains(args, "--push") {
				t.Fatalf("pass args reach a daemon or registry: %v", args)
			}
			if !slices.Contains(args, "type=oci,tar=false,dest="+layouts[0]) {
				t.Fatalf("pass args do not export the layout: %v", args)
			}
		})
	}
	if !plan.Steps[0].Load || !plan.Steps[0].Push {
		t.Fatal("shaping a pass must not mutate the planned steps")
	}
}

// The crucible needs an image build, which a Docker daemon or remote BuildKit satisfies.
func TestCrucibleSubstrate_BothRunnerKinds(t *testing.T) {
	c := &crucibleContributor{}
	opts := runner.Options{DockerRequired: c.NeedsDocker(), BuildRequired: c.NeedsBuild(), IsCrucible: c.NeedsCrucible()}
	base := runner.SubstrateFacts{
		DiskFreeMB: 50000, TmpFreeMB: 50000, MemAvailableMB: 8000, InodePctFree: 50,
		StagefreightWritable: true, WorkdirReadable: true,
	}
	daemon := base
	daemon.DockerAvailable = true
	remote := base
	remote.BuildKitHost, remote.BuildxAvailable = "tcp://buildkitd:1234", true

	for kind, facts := range map[string]runner.SubstrateFacts{"docker daemon": daemon, "remote buildkit": remote} {
		if _, grade := runner.EvaluateHealth(facts, opts); grade == runner.Unhealthy {
			t.Errorf("%s runner graded unhealthy for a crucible build", kind)
		}
	}
	if _, grade := runner.EvaluateHealth(base, opts); grade != runner.Unhealthy {
		t.Error("a runner with neither a daemon nor a remote BuildKit must be unhealthy for a crucible build")
	}
}
