package pages

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureProject_GloballyTakenNameIsClearError covers the collision hardening: a
// create rejected as "already taken" while the account-scoped GET keeps returning
// not-found means the name is owned by ANOTHER account (Pages names are global). We must
// re-GET to disambiguate and surface an actionable error, not swallow it as success.
func TestEnsureProject_GloballyTakenNameIsClearError(t *testing.T) {
	var getCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pages/projects/taken"):
			getCount++
			writeCFError(w, http.StatusNotFound, "project not found") // never in our account
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pages/projects"):
			writeCFError(w, http.StatusBadRequest, "the name has already been taken")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newCFPagesClient("t", "acct", "taken", "")
	c.base = srv.URL
	err := c.ensureProject(context.Background())
	if err == nil {
		t.Fatal("expected an error for a globally-taken name, got nil (would fail opaquely later)")
	}
	if !strings.Contains(err.Error(), "already taken on Cloudflare") {
		t.Errorf("error = %q, want the globally-taken guidance", err.Error())
	}
	if getCount < 2 {
		t.Errorf("re-GET not performed to disambiguate: getCount = %d, want >= 2", getCount)
	}
}

// TestEnsureProject_CreateRaceIsSuccess covers the other side: a create "already exists"
// where the re-GET now finds the project in OUR account is a benign concurrent-create
// race, and must resolve to success.
func TestEnsureProject_CreateRaceIsSuccess(t *testing.T) {
	var getCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pages/projects/mine"):
			getCount++
			if getCount == 1 {
				writeCFError(w, http.StatusNotFound, "project not found") // first GET: not yet
			} else {
				writeCFResult(w, map[string]any{"name": "mine"}) // re-GET: present (race won)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pages/projects"):
			writeCFError(w, http.StatusBadRequest, "already exists")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newCFPagesClient("t", "acct", "mine", "")
	c.base = srv.URL
	if err := c.ensureProject(context.Background()); err != nil {
		t.Fatalf("a concurrent-create race should resolve to success, got: %v", err)
	}
}

// TestClassifyDNS pins the authoritative-nameserver classification: Cloudflare only
// when EVERY NS is under .cloudflare.com; any other NS ⇒ external; an inconclusive
// lookup ⇒ unknown (advisory, never a hard failure).
func TestClassifyDNS(t *testing.T) {
	cf := []*net.NS{{Host: "adam.ns.cloudflare.com."}, {Host: "kara.ns.cloudflare.com."}}
	mixed := []*net.NS{{Host: "adam.ns.cloudflare.com."}, {Host: "ns1.porkbun.com."}}
	ext := []*net.NS{{Host: "ns1.porkbun.com."}, {Host: "ns2.porkbun.com."}}

	cases := []struct {
		name string
		ns   []*net.NS
		err  error
		want DNSProvider
	}{
		{"all cloudflare", cf, nil, DNSCloudflare},
		{"mixed is external", mixed, nil, DNSExternal},
		{"all external", ext, nil, DNSExternal},
		{"lookup error is unknown", nil, errors.New("no such host"), DNSUnknown},
		{"empty is unknown", nil, nil, DNSUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &cfPagesClient{lookupNS: func(string) ([]*net.NS, error) { return tc.ns, tc.err }}
			if got := c.classifyDNS("example.com"); got != tc.want {
				t.Errorf("classifyDNS = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDeploy_DomainFailureIsNonFatal is the core invariant: the site deploy and the
// custom-domain configuration are distinct outcomes, and a domain failure NEVER makes
// the deploy fail. Here the assets upload fine but the domains endpoint 403s (as a
// Pages:Edit token might for an apex auto-DNS write) — deploy must still succeed and
// report the domain problem as data.
func TestDeploy_DomainFailureIsNonFatal(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "index.html"), []byte("<html>hi</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/upload-token"):
			writeCFResult(w, map[string]any{"jwt": "the-jwt"})
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/pages/projects/proj"):
			writeCFResult(w, map[string]any{"name": "proj"}) // project exists
		case p == "/pages/assets/check-missing":
			var body struct {
				Hashes []string `json:"hashes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			writeCFResult(w, body.Hashes)
		case p == "/pages/assets/upload":
			writeCFResult(w, true)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/deployments"):
			writeCFResult(w, map[string]any{"url": "https://abc.proj.pages.dev"})
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/domains"):
			// Simulate the token lacking authority to auto-provision the apex record.
			writeCFError(w, http.StatusForbidden, "insufficient permissions to modify DNS")
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := newCFPagesClient("api-token", "acct-1", "proj", "example.com")
	c.base = srv.URL
	c.lookupNS = func(string) ([]*net.NS, error) {
		return []*net.NS{{Host: "ns1.porkbun.com."}}, nil // external DNS
	}

	res, err := c.deploy(context.Background(), ws)
	if err != nil {
		t.Fatalf("deploy returned error on a domain-only failure: %v (site deploy must not fail)", err)
	}
	if res.URL != "https://abc.proj.pages.dev" {
		t.Errorf("res.URL = %q, want the deployment url", res.URL)
	}
	if res.Domain == nil {
		t.Fatal("res.Domain = nil, want the failed-attach outcome")
	}
	if res.Domain.Attached {
		t.Error("res.Domain.Attached = true, want false (the attach 403'd)")
	}
	if res.Domain.Err == "" {
		t.Error("res.Domain.Err is empty, want the attach error text")
	}
	if res.Domain.DNSProvider != DNSExternal {
		t.Errorf("res.Domain.DNSProvider = %q, want external", res.Domain.DNSProvider)
	}
}
