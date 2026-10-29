package upstream

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/PrPlanIT/StageFreight/src/config"
)

type entry struct {
	name, body, link string
	dir              bool
}

// image builds an image whose single layer holds entries, labelled with labels.
func image(t *testing.T, labels map[string]string, entries ...entry) v1.Image {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		case e.link != "":
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
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

func TestProbe(t *testing.T) {
	img := image(t, map[string]string{"io.homelabhd.ansible-core.version": "v2.21.5"},
		entry{name: "usr/lib/python3.13/site-packages/ansible_core-2.21.5.dist-info/METADATA", body: "Name: ansible-core\n"},
		entry{name: "usr/lib/python3.13/site-packages/ansible-2.21.0.dist-info/METADATA", body: "Name: ansible\n"},
		entry{name: "var/lib/dpkg/status", body: "Package: nut-server\nStatus: install ok installed\nVersion: 2.8.1-3+deb13u1\n\nPackage: nut-client\nStatus: deinstall ok config-files\nVersion: 2.7.0-1\n"},
		entry{name: "lib/apk/db/installed", body: "P:nginx\nV:1.28.0-r3\n\nP:musl\nV:1.2.5-r10\n"},
		entry{name: "usr/share/app/VERSION", body: "app version 4.19.5 (build 77)\n"},
		entry{name: "etc/app-version", link: "../usr/share/app/VERSION"},
	)
	cases := []struct {
		probe config.UpstreamProbe
		want  string
	}{
		{config.UpstreamProbe{Label: "io.homelabhd.ansible-core.version"}, "2.21.5"},
		{config.UpstreamProbe{Package: "ansible-core", Manager: "pip"}, "2.21.5"},
		{config.UpstreamProbe{Package: "Ansible_Core", Manager: "pip"}, "2.21.5"},
		{config.UpstreamProbe{Package: "nut-server", Manager: "dpkg"}, "2.8.1-3+deb13u1"},
		{config.UpstreamProbe{Package: "nginx", Manager: "apk"}, "1.28.0-r3"},
		{config.UpstreamProbe{File: "/usr/share/app/VERSION"}, "4.19.5"},
		{config.UpstreamProbe{File: "/etc/app-version", Match: `build (?P<version>\d+)`}, "77"},
	}
	for _, c := range cases {
		got, err := Probe(img, c.probe)
		if err != nil || got != c.want {
			t.Errorf("%+v: got %q, %v; want %q", c.probe, got, err, c.want)
		}
	}
	for probe, want := range map[config.UpstreamProbe]string{
		{Package: "nut-client", Manager: "dpkg"}: "not installed", // deinstalled, config files only
		{Package: "nope", Manager: "pip"}:        "not installed",
		{File: "/missing"}:                       "not a file",
		{Label: "absent"}:                        "not set",
	} {
		if _, err := Probe(img, probe); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want %q", probe, err, want)
		}
	}
}

func TestUpstreamPart(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"dpkg", "1:2.8.1-3+deb13u1"}: "2.8.1",
		{"dpkg", "2.8.1"}:             "2.8.1",
		{"apk", "1.28.0-r3"}:          "1.28.0",
		{"pip", "2.21.5"}:             "2.21.5",
	} {
		if got := UpstreamPart(in[0], in[1]); got != want {
			t.Errorf("UpstreamPart(%s, %s) = %s, want %s", in[0], in[1], got, want)
		}
	}
}

// TestProbeSymlinkChainAndCycle covers multi-hop symlink chains, two files sharing one target,
// and the cycle guard that keeps a self- or mutually-referential link from recursing until the
// stack overflows.
func TestProbeSymlinkChainAndCycle(t *testing.T) {
	// A two-hop chain resolves to the file at the end.
	chain := image(t, nil,
		entry{name: "usr/share/app/VERSION", body: "app version 4.19.5\n"},
		entry{name: "etc/app-version-1", link: "../usr/share/app/VERSION"},
		entry{name: "etc/app-version", link: "app-version-1"},
	)
	if got, err := Probe(chain, config.UpstreamProbe{File: "/etc/app-version"}); err != nil || got != "4.19.5" {
		t.Fatalf("chain: got %q, %v; want 4.19.5", got, err)
	}

	// Two files pointing at the same target both resolve — a shared target is not a cycle.
	shared := image(t, nil,
		entry{name: "usr/share/app/VERSION", body: "app version 4.19.5\n"},
		entry{name: "etc/a", link: "../usr/share/app/VERSION"},
		entry{name: "etc/b", link: "../usr/share/app/VERSION"},
	)
	if got, err := Probe(shared, config.UpstreamProbe{File: "/etc/a"}); err != nil || got != "4.19.5" {
		t.Fatalf("shared target: got %q, %v; want 4.19.5", got, err)
	}

	// A mutual cycle fails loud instead of hanging.
	cyc := image(t, nil,
		entry{name: "etc/loop", link: "loop-2"},
		entry{name: "etc/loop-2", link: "loop"},
	)
	if _, err := Probe(cyc, config.UpstreamProbe{File: "/etc/loop"}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle: err = %v, want a cycle error", err)
	}
}
