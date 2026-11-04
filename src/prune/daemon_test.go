package prune

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeDaemon stands in for a Docker daemon holding imgs; removing an image shrinks the
// store by its size. It returns the removal calls.
func fakeDaemon(t *testing.T, imgs []fakeImage, inUse ...string) *[][]string {
	t.Helper()
	oldDocker, oldDaemon := docker, dockerDaemon
	t.Cleanup(func() { docker, dockerDaemon = oldDocker, oldDaemon })
	dockerDaemon = func() (string, bool) { return "/var/run/docker.sock", true }
	var removed [][]string
	present := map[string]bool{}
	for _, im := range imgs {
		present[im.id] = true
	}
	docker = func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args[:2], " ")
		switch cmd {
		case "image ls":
			var ids []string
			for _, im := range imgs {
				if present[im.id] {
					ids = append(ids, im.id)
				}
			}
			return []byte(strings.Join(ids, "\n")), nil
		case "image inspect":
			var out []string
			for _, im := range imgs {
				tags := `[]`
				if len(im.tags) > 0 {
					tags = `["` + strings.Join(im.tags, `","`) + `"]`
				}
				out = append(out, fmt.Sprintf(`{"Id":%q,"RepoTags":%s,"Created":"2020-01-01T00:00:00Z","Metadata":{"LastTagTime":%q}}`,
					im.id, tags, im.pulled.UTC().Format(time.RFC3339Nano)))
			}
			return []byte(strings.Join(out, "\n")), nil
		case "container ls":
			if len(inUse) == 0 {
				return nil, nil
			}
			return []byte("c1"), nil
		case "container inspect":
			return []byte(strings.Join(inUse, "\n")), nil
		case "system df":
			var total float64
			for _, im := range imgs {
				if present[im.id] {
					total += im.gb
				}
			}
			return []byte(fmt.Sprintf(`{"Type":"Images","Size":"%.2fGB"}`+"\n"+`{"Type":"Containers","Size":"0B"}`, total)), nil
		case "image rm":
			removed = append(removed, args[2:])
			for _, im := range imgs {
				for _, ref := range args[2:] {
					if ref == im.id || (len(im.tags) > 0 && ref == im.tags[0]) {
						present[im.id] = false
					}
				}
			}
			return nil, nil
		}
		t.Fatalf("unexpected docker %v", args)
		return nil, nil
	}
	return &removed
}

type fakeImage struct {
	id     string
	tags   []string
	pulled time.Time
	gb     float64
}

// Over budget, images no container uses go least recently pulled first until the store
// fits; one pulled within the minimum age, or used by a container, stays.
func TestPruneDaemonImages_Budget(t *testing.T) {
	now := time.Now()
	removed := fakeDaemon(t, []fakeImage{
		{"sha256:a", []string{"a:1"}, now.Add(-10 * 24 * time.Hour), 3},
		{"sha256:b", []string{"b:1", "b:latest"}, now.Add(-5 * 24 * time.Hour), 2},
		{"sha256:c", []string{"c:1"}, now.Add(-time.Minute), 4},
		{"sha256:d", []string{"d:1"}, now.Add(-20 * 24 * time.Hour), 1},
	}, "sha256:d")

	items, note, skipped, err := pruneDaemonImages(context.Background(),
		Action{Kind: KindDaemonImages, MaxSize: 5e9, MinAge: 2 * time.Minute}, true)
	if err != nil || skipped != "" {
		t.Fatalf("err=%v skipped=%q", err, skipped)
	}
	if len(*removed) != 2 || (*removed)[0][0] != "a:1" || strings.Join((*removed)[1], ",") != "b:1,b:latest" {
		t.Fatalf("removed %v, want a then every tag of b", *removed)
	}
	if len(items) != 2 || !strings.Contains(note, "→") {
		t.Errorf("items=%v note=%q", items, note)
	}
}

// Within budget nothing is removed; past max_age an image goes whatever the budget.
func TestPruneDaemonImages_MaxAge(t *testing.T) {
	now := time.Now()
	removed := fakeDaemon(t, []fakeImage{
		{"sha256:old", []string{"old:1"}, now.Add(-30 * 24 * time.Hour), 1},
		{"sha256:new", []string{"new:1"}, now.Add(-24 * time.Hour), 1},
	})
	if _, _, _, err := pruneDaemonImages(context.Background(),
		Action{Kind: KindDaemonImages, MaxSize: 100e9, MinAge: 2 * time.Minute}, true); err != nil || len(*removed) != 0 {
		t.Fatalf("within budget: removed %v err=%v", *removed, err)
	}
	if _, _, _, err := pruneDaemonImages(context.Background(),
		Action{Kind: KindDaemonImages, MaxAge: 14 * 24 * time.Hour, MinAge: 2 * time.Minute}, true); err != nil {
		t.Fatal(err)
	}
	if len(*removed) != 1 || (*removed)[0][0] != "old:1" {
		t.Fatalf("removed %v, want only the image pulled past max_age", *removed)
	}
}

// The runner declares the budget in its environment; undeclared there is no action, and
// a malformed declaration is reported instead of guessed at.
func TestDaemonImageAction_Declaration(t *testing.T) {
	t.Setenv(DaemonGCMaxSizeEnv, "")
	t.Setenv(DaemonGCMaxAgeEnv, "")
	if a := daemonImageAction(); a != nil {
		t.Fatalf("undeclared: %+v", a)
	}
	t.Setenv(DaemonGCMaxSizeEnv, "8GB")
	a := daemonImageAction()
	if len(a) != 1 || a[0].MaxSize != 8<<30 || a[0].MinAge != defaultDaemonMinAge || a[0].Invalid != "" {
		t.Fatalf("declared: %+v", a)
	}
	t.Setenv(DaemonGCMaxAgeEnv, "soon")
	if a := daemonImageAction(); len(a) != 1 || !strings.Contains(a[0].Invalid, DaemonGCMaxAgeEnv) {
		t.Fatalf("malformed: %+v", a)
	}
	_, _, skipped, _ := pruneDaemonImages(context.Background(), daemonImageAction()[0], true)
	if skipped == "" {
		t.Error("a malformed declaration must be reported, not run")
	}
}

// The budget runs whatever the cache root's pressure.
func TestPlan_DaemonBudgetIgnoresPressureGate(t *testing.T) {
	t.Setenv(DaemonGCMaxSizeEnv, "8GB")
	root := fakeCacheRoot(t)
	actions := Plan(nil, Options{CacheRoot: root, Target: 0.999999})
	if len(actions) != 1 || actions[0].Kind != KindDaemonImages || actions[0].LockRoot != "" {
		t.Fatalf("healthy disk: %+v, want only the daemon budget", actions)
	}
}

func TestParseDockerSize(t *testing.T) {
	for in, want := range map[string]int64{"11.25GB": 11_250_000_000, "7.756MB": 7_756_000, "0B": 0, "512kB": 512_000} {
		if got, err := parseDockerSize(in); err != nil || got != want {
			t.Errorf("%s = %d %v, want %d", in, got, err, want)
		}
	}
	if _, err := parseDockerSize("lots"); err == nil {
		t.Error("an unknown unit must be an error")
	}
}
