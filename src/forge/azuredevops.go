package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// AzureDevOpsForge is Azure DevOps's first-class forge client.
//
// Azure differs structurally from the other forges: its addressing is
// organization → project → repository (not owner/repo), it authenticates with a
// PAT over HTTP Basic, every REST call carries an api-version, and — crucially —
// it has NO native git-release object. Release operations therefore return
// ErrNotSupported rather than being faked; tagging, PRs, commits, and content
// reads map to the Azure DevOps Git REST API (7.1).
//
// TODO(azure-live-validation): endpoints and auth follow the documented Azure
// DevOps REST API but have NOT been exercised against a live instance. Run a real
// end-to-end pass (render → build → every client op) before treating this client
// as production-ready; until then it is experimental.
type AzureDevOpsForge struct {
	// BaseURL is the organization collection URL, e.g. "https://dev.azure.com/myorg"
	// (Azure DevOps Services) or "https://server/tfs/DefaultCollection" (Server).
	BaseURL string
	Project string
	Repo    string
	Token   string // Personal Access Token (HTTP Basic, empty username)
}

// NewAzureDevOps creates an Azure DevOps client, resolving org/project/repo and
// the token from the Azure Pipelines environment when present.
//
//	SYSTEM_COLLECTIONURI  → BaseURL (org collection)
//	SYSTEM_TEAMPROJECT    → Project
//	BUILD_REPOSITORY_NAME → Repo
//	AZURE_DEVOPS_EXT_PAT / AZURE_DEVOPS_TOKEN / SYSTEM_ACCESSTOKEN → Token
func NewAzureDevOps(baseURL string) *AzureDevOpsForge {
	if baseURL == "" {
		baseURL = os.Getenv("SYSTEM_COLLECTIONURI")
	}
	token := os.Getenv("AZURE_DEVOPS_EXT_PAT")
	if token == "" {
		token = os.Getenv("AZURE_DEVOPS_TOKEN")
	}
	if token == "" {
		token = os.Getenv("SYSTEM_ACCESSTOKEN")
	}
	return &AzureDevOpsForge{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Project: os.Getenv("SYSTEM_TEAMPROJECT"),
		Repo:    os.Getenv("BUILD_REPOSITORY_NAME"),
		Token:   token,
	}
}

// Provider reports Azure DevOps.
func (a *AzureDevOpsForge) Provider() Provider { return AzureDevOps }

// Azure DevOps has no generic package registry analogous to GitLab's; binary
// distribution there is out of scope for generic-package.
func (a *AzureDevOpsForge) PublishPackageFile(ctx context.Context, opts PublishPackageOptions) (*PublishedPackage, error) {
	return nil, fmt.Errorf("generic-package: Azure DevOps generic package publishing not supported: %w", ErrNotSupported)
}

func (a *AzureDevOpsForge) ListPackageVersions(ctx context.Context, packageName string) ([]PackageVersion, error) {
	return nil, fmt.Errorf("generic-package: Azure DevOps generic package listing not supported: %w", ErrNotSupported)
}

func (a *AzureDevOpsForge) DeletePackageVersion(ctx context.Context, packageName, version string) error {
	return fmt.Errorf("generic-package: Azure DevOps generic package deletion not supported: %w", ErrNotSupported)
}

// gitURL builds a Git REST API URL for this repo and appends api-version.
func (a *AzureDevOpsForge) gitURL(path, query string) string {
	u := fmt.Sprintf("%s/%s/_apis/git/repositories/%s%s", a.BaseURL, a.Project, a.Repo, path)
	sep := "?"
	if query != "" {
		u += "?" + query
		sep = "&"
	}
	return u + sep + "api-version=7.1"
}

// doJSON performs an authenticated JSON request. PAT auth is HTTP Basic with an
// empty username, per the Azure DevOps convention.
func (a *AzureDevOpsForge) doJSON(ctx context.Context, method, url string, body, result interface{}) error {
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
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+a.Token)))
	req.Header.Set("Accept", "application/json")
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

// DefaultBranch returns the repo's default branch (short name).
func (a *AzureDevOpsForge) DefaultBranch(ctx context.Context) (string, error) {
	var resp struct {
		DefaultBranch string `json:"defaultBranch"` // "refs/heads/main"
	}
	if err := a.doJSON(ctx, "GET", a.gitURL("", ""), nil, &resp); err != nil {
		return "", fmt.Errorf("reading repo info: %w", err)
	}
	return strings.TrimPrefix(resp.DefaultBranch, "refs/heads/"), nil
}

// BranchHeadSHA returns the current HEAD object id of a branch.
func (a *AzureDevOpsForge) BranchHeadSHA(ctx context.Context, branch string) (string, error) {
	var resp struct {
		Value []struct {
			ObjectID string `json:"objectId"`
		} `json:"value"`
	}
	url := a.gitURL("/refs", "filter=heads/"+branch)
	if err := a.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return "", fmt.Errorf("reading branch ref: %w", err)
	}
	if len(resp.Value) == 0 {
		return "", fmt.Errorf("branch %q not found", branch)
	}
	return resp.Value[0].ObjectID, nil
}

// GetFileContent reads a file from the repo at a branch ref.
func (a *AzureDevOpsForge) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	q := fmt.Sprintf("path=%s&versionDescriptor.version=%s&versionDescriptor.versionType=branch&includeContent=true",
		path, strings.TrimPrefix(ref, "refs/heads/"))
	var resp struct {
		Content string `json:"content"`
	}
	if err := a.doJSON(ctx, "GET", a.gitURL("/items", q), nil, &resp); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return []byte(resp.Content), nil
}

// CommitFiles creates/updates/deletes files in a single push.
func (a *AzureDevOpsForge) CommitFiles(ctx context.Context, opts CommitFilesOptions) (*CommitResult, error) {
	old := opts.ExpectedSHA
	if old == "" {
		head, err := a.BranchHeadSHA(ctx, opts.Branch)
		if err != nil {
			return nil, err
		}
		old = head
	}

	type item struct {
		Path string `json:"path"`
	}
	type content struct {
		Content     string `json:"content"`
		ContentType string `json:"contentType"`
	}
	type change struct {
		ChangeType string   `json:"changeType"`
		Item       item     `json:"item"`
		NewContent *content `json:"newContent,omitempty"`
	}

	var changes []change
	for _, fa := range opts.Files {
		p := "/" + strings.TrimPrefix(fa.Path, "/")
		if fa.Delete {
			changes = append(changes, change{ChangeType: "delete", Item: item{Path: p}})
			continue
		}
		ct := "edit"
		if _, err := a.GetFileContent(ctx, fa.Path, opts.Branch); err != nil {
			ct = "add" // not present (or unreadable) → add
		}
		changes = append(changes, change{
			ChangeType: ct,
			Item:       item{Path: p},
			NewContent: &content{Content: base64.StdEncoding.EncodeToString(fa.Content), ContentType: "base64encoded"},
		})
	}

	push := map[string]interface{}{
		"refUpdates": []map[string]string{{"name": "refs/heads/" + opts.Branch, "oldObjectId": old}},
		"commits":    []map[string]interface{}{{"comment": opts.Message, "changes": changes}},
	}
	var resp struct {
		Commits []struct {
			CommitID string `json:"commitId"`
		} `json:"commits"`
	}
	if err := a.doJSON(ctx, "POST", a.gitURL("/pushes", ""), push, &resp); err != nil {
		if isConflict(err) {
			return nil, ErrBranchMoved
		}
		return nil, fmt.Errorf("push: %w", err)
	}
	sha := old
	if len(resp.Commits) > 0 {
		sha = resp.Commits[0].CommitID
	}
	return &CommitResult{SHA: sha}, nil
}

// CommitFile is a single-file CommitFiles.
func (a *AzureDevOpsForge) CommitFile(ctx context.Context, opts CommitFileOptions) error {
	_, err := a.CommitFiles(ctx, CommitFilesOptions{
		Branch:  opts.Branch,
		Message: opts.Message,
		Files:   []FileAction{{Path: opts.Path, Content: opts.Content}},
	})
	return err
}

// CreateTag creates a lightweight tag ref pointing at a commit.
func (a *AzureDevOpsForge) CreateTag(ctx context.Context, tagName, ref string) error {
	body := []map[string]string{{
		"name":        "refs/tags/" + tagName,
		"oldObjectId": "0000000000000000000000000000000000000000",
		"newObjectId": ref,
	}}
	return a.doJSON(ctx, "POST", a.gitURL("/refs", ""), body, nil)
}

// DeleteTag deletes a tag ref.
func (a *AzureDevOpsForge) DeleteTag(ctx context.Context, tagName string) error {
	cur, err := a.tagObjectID(ctx, tagName)
	if err != nil {
		return err
	}
	body := []map[string]string{{
		"name":        "refs/tags/" + tagName,
		"oldObjectId": cur,
		"newObjectId": "0000000000000000000000000000000000000000",
	}}
	return a.doJSON(ctx, "POST", a.gitURL("/refs", ""), body, nil)
}

func (a *AzureDevOpsForge) tagObjectID(ctx context.Context, tagName string) (string, error) {
	var resp struct {
		Value []struct {
			ObjectID string `json:"objectId"`
		} `json:"value"`
	}
	if err := a.doJSON(ctx, "GET", a.gitURL("/refs", "filter=tags/"+tagName), nil, &resp); err != nil {
		return "", err
	}
	if len(resp.Value) == 0 {
		return "", fmt.Errorf("tag %q not found", tagName)
	}
	return resp.Value[0].ObjectID, nil
}

// CreateMR opens a pull request.
func (a *AzureDevOpsForge) CreateMR(ctx context.Context, opts MROptions) (*MR, error) {
	body := map[string]interface{}{
		"sourceRefName": "refs/heads/" + opts.SourceBranch,
		"targetRefName": "refs/heads/" + opts.TargetBranch,
		"title":         opts.Title,
		"description":   opts.Description,
		"isDraft":       opts.Draft,
	}
	var resp struct {
		PullRequestID int    `json:"pullRequestId"`
		URL           string `json:"url"`
	}
	if err := a.doJSON(ctx, "POST", a.gitURL("/pullrequests", ""), body, &resp); err != nil {
		return nil, fmt.Errorf("create pull request: %w", err)
	}
	return &MR{ID: fmt.Sprintf("%d", resp.PullRequestID), URL: resp.URL}, nil
}

// ── Operations with no native Azure DevOps git equivalent ────────────────────
// Azure DevOps has no git-release object (unlike GitHub/GitLab/Gitea). Release
// surfaces are honestly unsupported rather than faked; use tags + an external
// changelog, or Azure Artifacts for packages.

func (a *AzureDevOpsForge) CreateRelease(ctx context.Context, opts ReleaseOptions) (*Release, error) {
	return nil, ErrNotSupported
}
func (a *AzureDevOpsForge) UploadAsset(ctx context.Context, releaseID string, asset Asset) error {
	return ErrNotSupported
}
func (a *AzureDevOpsForge) AddReleaseLink(ctx context.Context, releaseID string, link ReleaseLink) error {
	return ErrNotSupported
}
func (a *AzureDevOpsForge) ListReleases(ctx context.Context) ([]ReleaseInfo, error) {
	return nil, ErrNotSupported
}
func (a *AzureDevOpsForge) DeleteRelease(ctx context.Context, tagName string) error {
	return ErrNotSupported
}
func (a *AzureDevOpsForge) CancelPipeline(ctx context.Context, pipelineID string) error {
	return ErrNotSupported
}
func (a *AzureDevOpsForge) DownloadJobArtifact(ctx context.Context, ref, jobName, artifactPath string) ([]byte, error) {
	return nil, ErrNotSupported
}
