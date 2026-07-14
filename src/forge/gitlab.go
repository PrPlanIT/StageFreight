package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// GitLabForge implements the Forge interface for GitLab instances.
type GitLabForge struct {
	BaseURL    string // e.g., "https://gitlab.prplanit.com"
	Token      string // private token or job token
	ProjectID  string // numeric ID or "group/project" URL-encoded path
	isJobToken bool   // true when Token came from CI_JOB_TOKEN
}

// NewGitLab creates a GitLab forge client.
// Token is resolved from env: GITLAB_TOKEN, CI_JOB_TOKEN.
// ProjectID is resolved from env: CI_PROJECT_ID, CI_PROJECT_PATH.
func NewGitLab(baseURL string) *GitLabForge {
	token := os.Getenv("GITLAB_TOKEN")
	isJob := false
	if token == "" {
		token = os.Getenv("CI_JOB_TOKEN")
		isJob = token != ""
	}

	projectID := os.Getenv("CI_PROJECT_ID")
	if projectID == "" {
		projectID = os.Getenv("CI_PROJECT_PATH")
	}

	return &GitLabForge{
		BaseURL:    baseURL,
		Token:      token,
		ProjectID:  projectID,
		isJobToken: isJob,
	}
}

// setAuthHeader sets the appropriate auth header based on token type.
func (g *GitLabForge) setAuthHeader(req *http.Request) {
	if g.isJobToken {
		req.Header.Set("JOB-TOKEN", g.Token)
	} else {
		req.Header.Set("PRIVATE-TOKEN", g.Token)
	}
}

func (g *GitLabForge) Provider() Provider { return GitLab }

func (g *GitLabForge) apiURL(path string) string {
	return fmt.Sprintf("%s/api/v4/projects/%s%s", g.BaseURL, url.PathEscape(g.ProjectID), path)
}

func (g *GitLabForge) doJSON(ctx context.Context, method, url string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	g.setAuthHeader(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return &APIError{Method: method, URL: url, StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if result != nil {
		return json.Unmarshal(respBody, result)
	}
	return nil
}

func (g *GitLabForge) CreateRelease(ctx context.Context, opts ReleaseOptions) (*Release, error) {
	payload := map[string]interface{}{
		"tag_name":    opts.TagName,
		"name":        opts.Name,
		"description": opts.Description,
	}
	if opts.Ref != "" {
		payload["ref"] = opts.Ref
	}
	// NOTE: GitLab Releases have no native draft/prerelease/latest flag (unlike
	// GitHub/Gitea), so opts.Draft and opts.Type are intentionally not lowered here —
	// adding unknown fields would 400, and GitLab's "latest" is always date/semver-
	// computed. Every ReleaseType collapses to a plain release; the intent is honored
	// on forges that can express it.

	var resp struct {
		TagName string `json:"tag_name"`
		Links   struct {
			Self string `json:"self"`
		} `json:"_links"`
	}

	err := g.doJSON(ctx, "POST", g.apiURL("/releases"), payload, &resp)
	if err != nil {
		return nil, err
	}

	return &Release{
		ID:  resp.TagName,
		URL: fmt.Sprintf("%s/-/releases/%s", g.projectWebURL(), resp.TagName),
	}, nil
}

func (g *GitLabForge) UploadAsset(ctx context.Context, releaseID string, asset Asset) error {
	// GitLab: upload to project, then link to release
	uploadURL := g.apiURL("/uploads")

	f, err := os.Open(asset.FilePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filepath.Base(asset.FilePath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, &buf)
	if err != nil {
		return err
	}
	g.setAuthHeader(req)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var uploadResp struct {
		URL      string `json:"url"`
		FullPath string `json:"full_path"`
		Markdown string `json:"markdown"`
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("GitLab upload: %d %s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, &uploadResp); err != nil {
		return err
	}

	// GitLab's /uploads returns a PROJECT-RELATIVE `url` (/uploads/<secret>/<file>); the
	// resolvable path is `full_path` (/<namespace>/<project>/uploads/...). Using `url` drops
	// the project prefix, so the release download permalink redirects to a namespace-less
	// 404. Prefer full_path; fall back to url for older GitLab that omits it.
	assetPath := uploadResp.FullPath
	if assetPath == "" {
		assetPath = uploadResp.URL
	}

	// Link the uploaded file to the release, with a permanent download permalink
	// (/-/releases/<tag>/downloads/<name>) so consumers can curl a stable URL.
	return g.AddReleaseLink(ctx, releaseID, ReleaseLink{
		Name:            asset.Name,
		URL:             g.BaseURL + assetPath,
		LinkType:        "other",
		DirectAssetPath: gitlabDirectAssetPath(asset.Name),
	})
}

// gitlabDirectAssetPath converts an asset file name into a valid GitLab
// direct_asset_path. GitLab restricts that path to [A-Za-z0-9._-] (plus '/'),
// and rejects anything else — notably the SemVer build-metadata '+' in a name
// like "stagefreight-0.6.1-dev+6e376f2-linux-amd64.tar.gz" — with
// "Filepath is in an invalid format". Disallowed runes are replaced with '-' so
// the permalink stays valid and readable; the result always has a single
// leading slash. This is cosmetic only — the asset still downloads via the link
// URL — so liberal substitution is safe.
func gitlabDirectAssetPath(name string) string {
	var b strings.Builder
	b.Grow(len(name) + 1)
	b.WriteByte('/')
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (g *GitLabForge) AddReleaseLink(ctx context.Context, releaseID string, link ReleaseLink) error {
	payload := map[string]string{
		"name":      link.Name,
		"url":       link.URL,
		"link_type": link.LinkType,
	}
	if link.DirectAssetPath != "" {
		// Permanent permalink: /-/releases/<tag>/downloads/<direct_asset_path> → url.
		payload["direct_asset_path"] = link.DirectAssetPath
	}
	linkURL := g.apiURL(fmt.Sprintf("/releases/%s/assets/links", url.PathEscape(releaseID)))
	return g.doJSON(ctx, "POST", linkURL, payload, nil)
}

func (g *GitLabForge) CommitFile(ctx context.Context, opts CommitFileOptions) error {
	payload := map[string]string{
		"branch":         opts.Branch,
		"content":        base64.StdEncoding.EncodeToString(opts.Content),
		"commit_message": opts.Message,
		"encoding":       "base64",
	}
	encodedPath := url.PathEscape(opts.Path)
	fileURL := g.apiURL(fmt.Sprintf("/repository/files/%s", encodedPath))

	// Try update first (PUT), fall back to create (POST) if file doesn't exist.
	err := g.doJSON(ctx, "PUT", fileURL, payload, nil)
	if err != nil {
		return g.doJSON(ctx, "POST", fileURL, payload, nil)
	}
	return nil
}

func (g *GitLabForge) CommitFiles(ctx context.Context, opts CommitFilesOptions) (*CommitResult, error) {
	if len(opts.Files) == 0 {
		return nil, nil
	}

	// ExpectedSHA preflight: best-effort stale-head check
	if opts.ExpectedSHA != "" {
		head, err := g.BranchHeadSHA(ctx, opts.Branch)
		if err != nil {
			return nil, fmt.Errorf("reading branch head: %w", err)
		}
		if head != opts.ExpectedSHA {
			return nil, fmt.Errorf("%w: expected %s, got %s", ErrBranchMoved, opts.ExpectedSHA, head)
		}
	}

	// Determine action per file: "delete", "update" if exists, "create" if not.
	type glAction struct {
		Action   string `json:"action"`
		FilePath string `json:"file_path"`
		Content  string `json:"content,omitempty"`
		Encoding string `json:"encoding,omitempty"`
	}

	actions := make([]glAction, 0, len(opts.Files))
	for _, f := range opts.Files {
		if f.Delete {
			actions = append(actions, glAction{
				Action:   "delete",
				FilePath: f.Path,
			})
			continue
		}
		action := "update"
		if !g.fileExists(ctx, f.Path, opts.Branch) {
			action = "create"
		}
		actions = append(actions, glAction{
			Action:   action,
			FilePath: f.Path,
			Content:  base64.StdEncoding.EncodeToString(f.Content),
			Encoding: "base64",
		})
	}

	payload := map[string]interface{}{
		"branch":         opts.Branch,
		"commit_message": opts.Message,
		"actions":        actions,
	}

	var resp struct {
		ID string `json:"id"`
	}
	if err := g.doJSON(ctx, "POST", g.apiURL("/repository/commits"), payload, &resp); err != nil {
		return nil, err
	}
	return &CommitResult{SHA: resp.ID}, nil
}

func (g *GitLabForge) BranchHeadSHA(ctx context.Context, branch string) (string, error) {
	var resp struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	branchURL := g.apiURL(fmt.Sprintf("/repository/branches/%s", url.PathEscape(branch)))
	if err := g.doJSON(ctx, "GET", branchURL, nil, &resp); err != nil {
		return "", fmt.Errorf("reading branch %s: %w", branch, err)
	}
	return resp.Commit.ID, nil
}

// fileExists checks whether a file exists on a branch via the repository files API.
func (g *GitLabForge) fileExists(ctx context.Context, path, branch string) bool {
	encodedPath := url.PathEscape(path)
	fileURL := g.apiURL(fmt.Sprintf("/repository/files/%s?ref=%s", encodedPath, url.QueryEscape(branch)))

	req, err := http.NewRequestWithContext(ctx, "HEAD", fileURL, nil)
	if err != nil {
		return false
	}
	g.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (g *GitLabForge) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	if ref == "" {
		var err error
		ref, err = g.DefaultBranch(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolving default branch: %w", err)
		}
	}
	encodedPath := url.PathEscape(path)
	fileURL := g.apiURL(fmt.Sprintf("/repository/files/%s?ref=%s", encodedPath, url.QueryEscape(ref)))

	var resp struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := g.doJSON(ctx, "GET", fileURL, nil, &resp); err != nil {
		return nil, err
	}

	if resp.Encoding == "base64" {
		data, err := base64.StdEncoding.DecodeString(resp.Content)
		if err != nil {
			return nil, fmt.Errorf("decoding base64 content for %s: %w", path, err)
		}
		return data, nil
	}

	return []byte(resp.Content), nil
}

func (g *GitLabForge) DefaultBranch(ctx context.Context) (string, error) {
	var resp struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.doJSON(ctx, "GET", g.apiURL(""), nil, &resp); err != nil {
		return "", fmt.Errorf("reading project info: %w", err)
	}
	return resp.DefaultBranch, nil
}

func (g *GitLabForge) CreateMR(ctx context.Context, opts MROptions) (*MR, error) {
	payload := map[string]interface{}{
		"title":         opts.Title,
		"description":   opts.Description,
		"source_branch": opts.SourceBranch,
		"target_branch": opts.TargetBranch,
	}
	if opts.Draft {
		payload["title"] = "Draft: " + opts.Title
	}

	var resp struct {
		IID    int    `json:"iid"`
		WebURL string `json:"web_url"`
	}

	err := g.doJSON(ctx, "POST", g.apiURL("/merge_requests"), payload, &resp)
	if err != nil {
		return nil, err
	}

	return &MR{
		ID:  fmt.Sprintf("%d", resp.IID),
		URL: resp.WebURL,
	}, nil
}

func (g *GitLabForge) CancelPipeline(ctx context.Context, pipelineID string) error {
	cancelURL := g.apiURL(fmt.Sprintf("/pipelines/%s/cancel", pipelineID))
	return g.doJSON(ctx, "POST", cancelURL, nil, nil)
}

func (g *GitLabForge) ListReleases(ctx context.Context) ([]ReleaseInfo, error) {
	var all []ReleaseInfo
	page := 1

	for {
		url := fmt.Sprintf("%s?per_page=100&page=%d&order_by=released_at&sort=desc", g.apiURL("/releases"), page)

		var releases []struct {
			TagName     string `json:"tag_name"`
			Name        string `json:"name"`
			Description string `json:"description"`
			CreatedAt   string `json:"created_at"`
		}

		if err := g.doJSON(ctx, "GET", url, nil, &releases); err != nil {
			return all, err
		}

		for _, r := range releases {
			info := ReleaseInfo{
				ID:          r.TagName,
				TagName:     r.TagName,
				Name:        r.Name,
				Description: r.Description,
			}
			if t, err := parseTime(r.CreatedAt); err == nil {
				info.CreatedAt = t
			}
			all = append(all, info)
		}

		if len(releases) < 100 {
			break
		}
		page++
	}

	return all, nil
}

func (g *GitLabForge) DeleteRelease(ctx context.Context, tagName string) error {
	releaseURL := g.apiURL(fmt.Sprintf("/releases/%s", url.PathEscape(tagName)))
	return g.doJSON(ctx, "DELETE", releaseURL, nil, nil)
}

func (g *GitLabForge) CreateTag(ctx context.Context, tagName, ref string) error {
	payload := map[string]interface{}{
		"tag_name": tagName,
		"ref":      ref,
	}
	return g.doJSON(ctx, "POST", g.apiURL("/repository/tags"), payload, nil)
}

func (g *GitLabForge) DeleteTag(ctx context.Context, tagName string) error {
	return g.doJSON(ctx, "DELETE", g.apiURL(fmt.Sprintf("/repository/tags/%s", url.PathEscape(tagName))), nil, nil)
}

// ── Generic package registry ───────────────────────────────────────────────

// PublishPackageFile uploads one file to GitLab's generic package registry via
// PUT .../packages/generic/{pkg}/{ver}/{file}. The raw request body is the file
// bytes (octet-stream), not multipart. PullURL is the same path served by GET —
// tokenless on public projects.
func (g *GitLabForge) PublishPackageFile(ctx context.Context, opts PublishPackageOptions) (*PublishedPackage, error) {
	f, err := os.Open(opts.FilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	path := fmt.Sprintf("/packages/generic/%s/%s/%s",
		url.PathEscape(opts.PackageName),
		url.PathEscape(opts.Version),
		url.PathEscape(opts.FileName),
	)
	pullURL := g.apiURL(path)
	putURL := pullURL + "?select=package_file"

	req, err := http.NewRequestWithContext(ctx, "PUT", putURL, f)
	if err != nil {
		return nil, err
	}
	g.setAuthHeader(req)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &APIError{Method: "PUT", URL: putURL, StatusCode: resp.StatusCode, Body: string(body)}
	}

	return &PublishedPackage{
		PackageName: opts.PackageName,
		Version:     opts.Version,
		FileName:    opts.FileName,
		PullURL:     pullURL,
	}, nil
}

// ListPackageVersions returns generic-package versions newest-first. Each GitLab
// package id maps to one (name, version); that id is the deletion handle.
func (g *GitLabForge) ListPackageVersions(ctx context.Context, packageName string) ([]PackageVersion, error) {
	var out []PackageVersion
	page := 1
	for {
		path := fmt.Sprintf("/packages?package_type=generic&package_name=%s&per_page=100&page=%d&order_by=created_at&sort=desc",
			url.QueryEscape(packageName), page)

		var packages []struct {
			ID        int    `json:"id"`
			Version   string `json:"version"`
			CreatedAt string `json:"created_at"`
		}
		if err := g.doJSON(ctx, "GET", g.apiURL(path), nil, &packages); err != nil {
			return out, err
		}
		for _, p := range packages {
			pv := PackageVersion{ID: fmt.Sprintf("%d", p.ID), Version: p.Version}
			pv.CreatedAt, _ = parseTime(p.CreatedAt)
			out = append(out, pv)
		}
		if len(packages) < 100 {
			break
		}
		page++
	}
	return out, nil
}

// DeletePackageVersion deletes a generic-package version by resolving it to its
// GitLab package id and DELETE-ing that package. Not-found is treated as success
// (idempotent prune).
func (g *GitLabForge) DeletePackageVersion(ctx context.Context, packageName, version string) error {
	versions, err := g.ListPackageVersions(ctx, packageName)
	if err != nil {
		return err
	}
	for _, v := range versions {
		if v.Version == version {
			return g.doJSON(ctx, "DELETE", g.apiURL("/packages/"+v.ID), nil, nil)
		}
	}
	return nil // not found — idempotent
}

func (g *GitLabForge) projectWebURL() string {
	// CI_PROJECT_PATH is already "group/project", just join with base
	return fmt.Sprintf("%s/%s", g.BaseURL, g.ProjectID)
}

func (g *GitLabForge) DownloadJobArtifact(ctx context.Context, ref, jobName, artifactPath string) ([]byte, error) {
	rawURL := fmt.Sprintf("%s/api/v4/projects/%s/jobs/artifacts/%s/raw/%s?job=%s",
		g.BaseURL,
		url.PathEscape(g.ProjectID),
		url.PathEscape(ref),
		artifactPath, // already path-like, don't escape slashes
		url.QueryEscape(jobName),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	g.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return nil, os.ErrNotExist
	}
	if resp.StatusCode >= 400 {
		return nil, &APIError{Method: "GET", URL: rawURL, StatusCode: resp.StatusCode, Body: string(body)}
	}

	return body, nil
}
