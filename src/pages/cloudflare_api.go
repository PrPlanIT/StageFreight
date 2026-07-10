package pages

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Native Cloudflare Pages Direct Upload client — a faithful port of wrangler's
// protocol (workers-sdk packages/wrangler/src/pages/upload.ts + api/pages/deploy.ts):
// no wrangler, no npm, no docker. Constants below are ported from that repo's
// constants.ts.
const (
	cfAPIBase            = "https://api.cloudflare.com/client/v4"
	cfMaxAssetSize       = 25 * 1024 * 1024 // 25 MiB per file
	cfMaxBucketSize      = 40 * 1024 * 1024 // 40 MiB per upload bucket
	cfMaxBucketFileCount = 2000
)

type cfAsset struct {
	manifestKey string // leading-slash path, e.g. "/index.html"
	hash        string
	contentType string
	content     []byte
}

type cfPagesClient struct {
	apiToken  string
	accountID string
	project   string
	domains   []string // optional custom domain(s) to attach
	base      string   // API base; overridable in tests
	hasher    AssetHasher
	http      *http.Client
	lookupNS  func(string) ([]*net.NS, error) // authoritative-NS lookup; overridable in tests
}

func newCFPagesClient(apiToken, accountID, project string, domains []string) *cfPagesClient {
	return &cfPagesClient{
		apiToken:  apiToken,
		accountID: accountID,
		project:   project,
		domains:   domains,
		base:      cfAPIBase,
		hasher:    cloudflarePagesV1Hasher{},
		http:      &http.Client{Timeout: 2 * time.Minute},
		lookupNS:  net.LookupNS,
	}
}

// deploy runs the full Direct Upload flow. The SITE deploy is the critical operation —
// any failure up to and including createDeployment returns an error. Custom-domain
// configuration runs only AFTER a successful deploy and is best-effort: its outcome is
// reported as data (DeployResult.Domains), never as the returned error.
func (c *cfPagesClient) deploy(ctx context.Context, ws string) (DeployResult, error) {
	assets, err := c.collectAssets(ws)
	if err != nil {
		return DeployResult{}, err
	}
	if len(assets) == 0 {
		return DeployResult{}, fmt.Errorf("cloudflare pages: no files to deploy in %s", ws)
	}
	// Idempotently ensure the project exists — no dashboard step, and no broader token
	// than deploying already needs (Cloudflare Pages:Edit covers create + deploy).
	if err := c.ensureProject(ctx); err != nil {
		return DeployResult{}, err
	}
	jwt, err := c.uploadToken(ctx)
	if err != nil {
		return DeployResult{}, err
	}
	missing, err := c.checkMissing(ctx, jwt, assets)
	if err != nil {
		return DeployResult{}, err
	}
	if err := c.uploadAssets(ctx, jwt, assets, missing); err != nil {
		return DeployResult{}, err
	}
	url, err := c.createDeployment(ctx, assets)
	if err != nil {
		return DeployResult{}, err
	}
	// Site is deployed. Domain configuration is a separate, non-fatal enhancement.
	// A Pages project can carry several custom domains, so attach each one and report
	// its outcome independently — one failing domain never affects the others.
	res := DeployResult{URL: url}
	for _, d := range c.domains {
		if d == "" {
			continue
		}
		res.Domains = append(res.Domains, c.attachDomain(ctx, d))
	}
	return res, nil
}

// attachDomain best-effort attaches the custom hostname to the Pages project and
// reports the outcome as data. It NEVER returns an error — a domain problem must not
// fail a successful deploy. It first classifies the domain's authoritative nameservers
// (informational, to tailor later guidance), then lets the Pages API be the authority
// for the attach itself — public DNS is eventually consistent and is never used as a
// gate here (verification is a separate concern).
func (c *cfPagesClient) attachDomain(ctx context.Context, domain string) DomainOutcome {
	out := DomainOutcome{Name: domain, DNSProvider: c.classifyDNS(domain)}
	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/domains", c.base, c.accountID, c.project)
	err := c.doJSON(ctx, http.MethodPost, url, c.apiToken, map[string]any{"name": domain}, nil)
	if err == nil || isAlreadyExists(err) {
		out.Attached = true
		return out
	}
	out.Err = err.Error()
	return out
}

// classifyDNS resolves the domain's authoritative nameservers and classifies where DNS
// is hosted — the signal for whether the host can auto-configure records. Registrar is
// irrelevant; only the NS delegation matters. An inconclusive lookup is DNSUnknown, not
// a failure (this is advisory, not a gate).
func (c *cfPagesClient) classifyDNS(domain string) DNSProvider {
	ns, err := c.lookupNS(domain)
	if err != nil || len(ns) == 0 {
		return DNSUnknown
	}
	for _, n := range ns {
		host := strings.ToLower(strings.TrimSuffix(n.Host, "."))
		if !strings.HasSuffix(host, ".cloudflare.com") {
			return DNSExternal // any non-Cloudflare authoritative NS ⇒ external
		}
	}
	return DNSCloudflare
}

// ensureProject creates the Pages project if it doesn't already exist. GET-then-create
// so an existing project is a no-op; a create race is tolerated.
func (c *cfPagesClient) ensureProject(ctx context.Context) error {
	getURL := fmt.Sprintf("%s/accounts/%s/pages/projects/%s", c.base, c.accountID, c.project)
	if err := c.doJSON(ctx, http.MethodGet, getURL, c.apiToken, nil, nil); err == nil {
		return nil // already exists in this account
	}
	createURL := fmt.Sprintf("%s/accounts/%s/pages/projects", c.base, c.accountID)
	body := map[string]any{"name": c.project, "production_branch": "main"}
	err := c.doJSON(ctx, http.MethodPost, createURL, c.apiToken, body, nil)
	if err == nil {
		return nil
	}
	if isAlreadyExists(err) {
		// "already exists"/"already taken" is ambiguous. The account-scoped GET above
		// just told us the project is NOT in this account, so a create-time "taken"
		// means one of two things:
		//   (a) a concurrent create in THIS account between our GET and POST — benign, or
		//   (b) the name is owned by ANOTHER account — Pages project names are globally
		//       unique (they're <name>.pages.dev subdomains on a shared domain).
		// Re-GET scoped to our account to disambiguate: present now ⇒ (a), success;
		// still absent ⇒ (b), a name we can't use — surface an actionable error rather
		// than swallowing it and failing opaquely later at upload-token/deployment.
		if gerr := c.doJSON(ctx, http.MethodGet, getURL, c.apiToken, nil, nil); gerr == nil {
			return nil // (a) it's ours after all — a race
		}
		return fmt.Errorf("cloudflare pages: project name %q is already taken on Cloudflare (Pages project names are globally unique) — set project: to a unique name", c.project)
	}
	return fmt.Errorf("cloudflare pages: create project %q: %w", c.project, err)
}

// isAlreadyExists tolerates the "resource already exists" family of Cloudflare errors
// so create/attach stay idempotent. "already added" covers the custom-domain attach
// case (CF error 8000018: "You have already added this custom domain."), which a
// re-deploy hits every time — it is idempotent success, not a failure.
func isAlreadyExists(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") || strings.Contains(s, "already been taken") ||
		strings.Contains(s, "duplicate") || strings.Contains(s, "already added")
}

// collectAssets walks the workspace into upload assets: manifest key (leading slash),
// content hash, MIME type, and bytes.
func (c *cfPagesClient) collectAssets(ws string) ([]cfAsset, error) {
	var assets []cfAsset
	err := filepath.Walk(ws, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if info.Size() > cfMaxAssetSize {
			return fmt.Errorf("cloudflare pages: %s exceeds the 25 MiB per-file limit", p)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(ws, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		ct := mime.TypeByExtension(filepath.Ext(rel))
		if ct == "" {
			ct = "application/octet-stream"
		}
		assets = append(assets, cfAsset{
			manifestKey: "/" + rel,
			hash:        c.hasher.Hash(rel, content),
			contentType: ct,
			content:     content,
		})
		return nil
	})
	return assets, err
}

func (c *cfPagesClient) uploadToken(ctx context.Context) (string, error) {
	var res struct {
		JWT string `json:"jwt"`
	}
	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/upload-token", c.base, c.accountID, c.project)
	if err := c.doJSON(ctx, http.MethodGet, url, c.apiToken, nil, &res); err != nil {
		return "", fmt.Errorf("cloudflare pages: upload-token: %w", err)
	}
	if res.JWT == "" {
		return "", fmt.Errorf("cloudflare pages: empty upload token (does project %q exist?)", c.project)
	}
	return res.JWT, nil
}

func (c *cfPagesClient) checkMissing(ctx context.Context, jwt string, assets []cfAsset) ([]string, error) {
	hashes := uniqueHashes(assets)
	var missing []string
	if err := c.doJSON(ctx, http.MethodPost, c.base+"/pages/assets/check-missing", jwt,
		map[string]any{"hashes": hashes}, &missing); err != nil {
		return nil, fmt.Errorf("cloudflare pages: check-missing: %w", err)
	}
	return missing, nil
}

type cfUploadItem struct {
	Key      string `json:"key"`
	Value    string `json:"value"` // base64 content
	Metadata struct {
		ContentType string `json:"contentType"`
	} `json:"metadata"`
	Base64 bool `json:"base64"`
}

func (c *cfPagesClient) uploadAssets(ctx context.Context, jwt string, assets []cfAsset, missing []string) error {
	missingSet := make(map[string]bool, len(missing))
	for _, h := range missing {
		missingSet[h] = true
	}
	// One upload per distinct missing hash (identical files share content).
	seen := map[string]bool{}
	var pending []cfAsset
	for _, a := range assets {
		if !missingSet[a.hash] || seen[a.hash] {
			continue
		}
		seen[a.hash] = true
		pending = append(pending, a)
	}

	for _, bucket := range bucketize(pending) {
		payload := make([]cfUploadItem, 0, len(bucket))
		for _, a := range bucket {
			item := cfUploadItem{
				Key:    a.hash,
				Value:  base64.StdEncoding.EncodeToString(a.content),
				Base64: true,
			}
			item.Metadata.ContentType = a.contentType
			payload = append(payload, item)
		}
		if err := c.doJSON(ctx, http.MethodPost, c.base+"/pages/assets/upload", jwt, payload, nil); err != nil {
			return fmt.Errorf("cloudflare pages: upload: %w", err)
		}
	}
	return nil
}

func (c *cfPagesClient) createDeployment(ctx context.Context, assets []cfAsset) (string, error) {
	manifest := make(map[string]string, len(assets))
	for _, a := range assets {
		manifest[a.manifestKey] = a.hash
	}
	mb, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("manifest", string(mb)); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/deployments", c.base, c.accountID, c.project)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("cloudflare pages: create deployment: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var env struct {
		Success bool              `json:"success"`
		Errors  []cfAPIError      `json:"errors"`
		Result  struct{ URL string `json:"url"` } `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("cloudflare pages: create deployment: status %d: %s", resp.StatusCode, truncate(raw))
	}
	if !env.Success {
		return "", fmt.Errorf("cloudflare pages: create deployment: %s", cfErrs(env.Errors))
	}
	if env.Result.URL != "" {
		return env.Result.URL, nil
	}
	return fmt.Sprintf("https://%s.pages.dev", c.project), nil
}

// ── HTTP + helpers ──────────────────────────────────────────────────────────

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *cfPagesClient) doJSON(ctx context.Context, method, url, bearer string, body, result any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var env struct {
		Success bool            `json:"success"`
		Errors  []cfAPIError    `json:"errors"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("status %d: %s", resp.StatusCode, truncate(raw))
	}
	if !env.Success {
		return fmt.Errorf("%s", cfErrs(env.Errors))
	}
	if result != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, result)
	}
	return nil
}

func uniqueHashes(assets []cfAsset) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range assets {
		if !seen[a.hash] {
			seen[a.hash] = true
			out = append(out, a.hash)
		}
	}
	sort.Strings(out)
	return out
}

// bucketize groups assets into upload buckets bounded by count and total size
// (wrangler's MAX_BUCKET_FILE_COUNT / MAX_BUCKET_SIZE).
func bucketize(assets []cfAsset) [][]cfAsset {
	var buckets [][]cfAsset
	var cur []cfAsset
	curSize := 0
	for _, a := range assets {
		if len(cur) > 0 && (len(cur) >= cfMaxBucketFileCount || curSize+len(a.content) > cfMaxBucketSize) {
			buckets = append(buckets, cur)
			cur, curSize = nil, 0
		}
		cur = append(cur, a)
		curSize += len(a.content)
	}
	if len(cur) > 0 {
		buckets = append(buckets, cur)
	}
	return buckets
}

func cfErrs(errs []cfAPIError) string {
	if len(errs) == 0 {
		return "unknown cloudflare API error"
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = fmt.Sprintf("[%d] %s", e.Code, e.Message)
	}
	return strings.Join(parts, "; ")
}

func truncate(b []byte) string {
	const max = 300
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
