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
	domain    string // optional custom domain to attach
	base      string // API base; overridable in tests
	hasher    AssetHasher
	http      *http.Client
}

func newCFPagesClient(apiToken, accountID, project, domain string) *cfPagesClient {
	return &cfPagesClient{
		apiToken:  apiToken,
		accountID: accountID,
		project:   project,
		domain:    domain,
		base:      cfAPIBase,
		hasher:    cloudflarePagesV1Hasher{},
		http:      &http.Client{Timeout: 2 * time.Minute},
	}
}

// deploy runs the full Direct Upload flow and returns the deployment URL.
func (c *cfPagesClient) deploy(ctx context.Context, ws string) (string, error) {
	assets, err := c.collectAssets(ws)
	if err != nil {
		return "", err
	}
	if len(assets) == 0 {
		return "", fmt.Errorf("cloudflare pages: no files to deploy in %s", ws)
	}
	// Idempotently ensure the project exists — no dashboard step, and no broader token
	// than deploying already needs (Cloudflare Pages:Edit covers create + deploy).
	if err := c.ensureProject(ctx); err != nil {
		return "", err
	}
	jwt, err := c.uploadToken(ctx)
	if err != nil {
		return "", err
	}
	missing, err := c.checkMissing(ctx, jwt, assets)
	if err != nil {
		return "", err
	}
	if err := c.uploadAssets(ctx, jwt, assets, missing); err != nil {
		return "", err
	}
	url, err := c.createDeployment(ctx, assets)
	if err != nil {
		return "", err
	}
	// Idempotently attach the custom domain (same token scope). This is the apex
	// cutover — Cloudflare points the domain at Pages — driven by the domain: config.
	if c.domain != "" {
		if err := c.ensureCustomDomain(ctx); err != nil {
			return url, fmt.Errorf("cloudflare pages: deployed, but attaching domain %q failed: %w", c.domain, err)
		}
	}
	return url, nil
}

// ensureProject creates the Pages project if it doesn't already exist. GET-then-create
// so an existing project is a no-op; a create race is tolerated.
func (c *cfPagesClient) ensureProject(ctx context.Context) error {
	getURL := fmt.Sprintf("%s/accounts/%s/pages/projects/%s", c.base, c.accountID, c.project)
	if err := c.doJSON(ctx, http.MethodGet, getURL, c.apiToken, nil, nil); err == nil {
		return nil // already exists
	}
	createURL := fmt.Sprintf("%s/accounts/%s/pages/projects", c.base, c.accountID)
	body := map[string]any{"name": c.project, "production_branch": "main"}
	if err := c.doJSON(ctx, http.MethodPost, createURL, c.apiToken, body, nil); err != nil {
		if isAlreadyExists(err) {
			return nil // created between the GET and POST
		}
		return fmt.Errorf("cloudflare pages: create project %q: %w", c.project, err)
	}
	return nil
}

// ensureCustomDomain attaches the custom domain to the project (idempotent).
func (c *cfPagesClient) ensureCustomDomain(ctx context.Context) error {
	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/domains", c.base, c.accountID, c.project)
	if err := c.doJSON(ctx, http.MethodPost, url, c.apiToken, map[string]any{"name": c.domain}, nil); err != nil {
		if isAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

// isAlreadyExists tolerates the "resource already exists" family of Cloudflare errors
// so create/attach stay idempotent.
func isAlreadyExists(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") || strings.Contains(s, "already been taken") ||
		strings.Contains(s, "duplicate")
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
