package gitstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/storer"
	gittransport "github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func init() {
	// go-git's stock file transport runs git-upload-pack/git-receive-pack binaries; its
	// in-process server serves a local repository with no git binary.
	client.InstallProtocol("file", localTransport{loader: repoLoader{}, inner: server.NewClient(repoLoader{})})
}

// repoLoader finds a local repository the way git does: the path itself when bare, else
// its .git directory, or the gitdir a .git file names.
type repoLoader struct{}

func (repoLoader) Load(ep *gittransport.Endpoint) (storer.Storer, error) {
	dir := ep.Path
	if _, err := os.Stat(filepath.Join(dir, "config")); err != nil {
		dotgit := filepath.Join(dir, ".git")
		fi, err := os.Stat(dotgit)
		switch {
		case err != nil:
			return nil, gittransport.ErrRepositoryNotFound
		case fi.IsDir():
			dir = dotgit
		default:
			b, err := os.ReadFile(dotgit)
			gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
			if err != nil || !ok {
				return nil, gittransport.ErrRepositoryNotFound
			}
			if !filepath.IsAbs(gitdir) {
				gitdir = filepath.Join(dir, gitdir)
			}
			dir = gitdir
		}
	}
	return filesystem.NewStorage(osfs.New(dir), cache.NewObjectLRUDefault()), nil
}

// localTransport is go-git's in-process server with git's upload-pack negotiation: the
// client's "have" commits the served repository lacks — its own unpushed work — are
// ignored instead of failing the fetch.
type localTransport struct {
	loader server.Loader
	inner  gittransport.Transport
}

func (t localTransport) NewUploadPackSession(ep *gittransport.Endpoint, auth gittransport.AuthMethod) (gittransport.UploadPackSession, error) {
	sess, err := t.inner.NewUploadPackSession(ep, auth)
	if err != nil {
		return nil, err
	}
	sto, err := t.loader.Load(ep)
	if err != nil {
		sess.Close()
		return nil, err
	}
	return knownHaves{UploadPackSession: sess, sto: sto}, nil
}

func (t localTransport) NewReceivePackSession(ep *gittransport.Endpoint, auth gittransport.AuthMethod) (gittransport.ReceivePackSession, error) {
	return t.inner.NewReceivePackSession(ep, auth)
}

type knownHaves struct {
	gittransport.UploadPackSession
	sto storer.EncodedObjectStorer
}

// AdvertisedReferences adds each annotated tag's peeled commit, as git's upload-pack
// advertises it (refs/tags/v1^{}).
func (s knownHaves) AdvertisedReferences() (*packp.AdvRefs, error) {
	return s.AdvertisedReferencesContext(context.Background())
}

func (s knownHaves) AdvertisedReferencesContext(ctx context.Context) (*packp.AdvRefs, error) {
	ar, err := s.UploadPackSession.AdvertisedReferencesContext(ctx)
	if err != nil {
		return nil, err
	}
	if ar.Peeled == nil {
		ar.Peeled = map[string]plumbing.Hash{}
	}
	for name, h := range ar.References {
		if !strings.HasPrefix(name, "refs/tags/") {
			continue
		}
		tag, err := object.GetTag(s.sto, h)
		if err != nil {
			continue // a lightweight tag: nothing to peel
		}
		if c, err := tag.Commit(); err == nil {
			ar.Peeled[name] = c.Hash
		}
	}
	return ar, nil
}

func (s knownHaves) UploadPack(ctx context.Context, req *packp.UploadPackRequest) (*packp.UploadPackResponse, error) {
	known := make([]plumbing.Hash, 0, len(req.Haves))
	for _, h := range req.Haves {
		if s.sto.HasEncodedObject(h) == nil {
			known = append(known, h)
		}
	}
	req.Haves = known
	return s.UploadPackSession.UploadPack(ctx, req)
}

// embeddedTransport performs Git transport in-process via go-git. Each remote's
// credential is resolved on its first network use (ResolveGitCredential: host-bound
// for HTTPS, SSH_PRIVATE_KEY / agent / ~/.ssh keys for SSH), so local-only work never
// needs one.
type embeddedTransport struct {
	repo *git.Repository
	cfg  *config.Config

	mu    sync.Mutex
	auths map[string]authResult
}

type authResult struct {
	auth gittransport.AuthMethod
	err  error
}

func newEmbeddedTransport(repo *git.Repository, cfg *config.Config) *embeddedTransport {
	return &embeddedTransport{repo: repo, cfg: cfg, auths: map[string]authResult{}}
}

// auth is remote's credential, resolved once per session.
func (t *embeddedTransport) auth(remote string) (gittransport.AuthMethod, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r, ok := t.auths[remote]; ok {
		return r.auth, r.err
	}
	var r authResult
	url, err := RemoteURL(t.repo, remote)
	if err != nil {
		r.err = fmt.Errorf("resolving remote URL for %s: %w", remote, err)
	} else if r.auth, err = ResolveGitCredential(url, t.cfg); err != nil {
		r.err = fmt.Errorf("resolving credential for %s: %w", remote, err)
	}
	t.auths[remote] = r
	return r.auth, r.err
}

func (t *embeddedTransport) Fetch(remote string) error {
	auth, err := t.auth(remote)
	if err != nil {
		return err
	}
	refspec := gitconfig.RefSpec(fmt.Sprintf("+refs/heads/*:refs/remotes/%s/*", remote))
	err = t.repo.Fetch(&git.FetchOptions{
		RemoteName: remote,
		RefSpecs:   []gitconfig.RefSpec{refspec},
		Auth:       auth,
	})
	if err == git.NoErrAlreadyUpToDate || errors.Is(err, gittransport.ErrEmptyRemoteRepository) {
		return nil // an empty remote has nothing to fetch
	}
	if err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}
	return nil
}

func (t *embeddedTransport) FastForward(remote string) error {
	auth, err := t.auth(remote)
	if err != nil {
		return err
	}
	wt, err := t.repo.Worktree()
	if err != nil {
		return fmt.Errorf("opening worktree: %w", err)
	}
	err = wt.Pull(&git.PullOptions{
		RemoteName: remote,
		Auth:       auth,
	})
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	if err != nil {
		return err // includes git.ErrNonFastForwardUpdate
	}
	return nil
}

func (t *embeddedTransport) Push(remote, refspec string) error {
	auth, err := t.auth(remote)
	if err != nil {
		return err
	}
	spec, err := t.pushRefSpec(remote, refspec, auth)
	if err != nil {
		return err
	}
	err = t.repo.Push(&git.PushOptions{
		RemoteName: remote,
		RefSpecs:   []gitconfig.RefSpec{spec},
		Auth:       auth,
	})
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	if err != nil {
		return fmt.Errorf("push to %s: %w", remote, err)
	}
	return nil
}

// pushRefSpec is the refspec go-git pushes for refspec, as git would read it; go-git
// itself takes refspecs only as full ref names.
//   - An empty refspec is git's default (push.default=simple): the current branch to its
//     upstream, never every branch.
//   - The source resolves as git resolves it: a symbolic ref (HEAD on a branch) names the
//     branch it points at, and a short name its branch or tag.
//   - A source alone pushes to the same name.
//   - A short destination names the matching ref on the remote, else the source's
//     namespace (a branch to refs/heads/, a tag to refs/tags/).
func (t *embeddedTransport) pushRefSpec(remote, refspec string, auth gittransport.AuthMethod) (gitconfig.RefSpec, error) {
	if refspec == "" {
		head, err := t.repo.Reference(plumbing.HEAD, false)
		if err != nil || head.Type() != plumbing.SymbolicReference {
			return "", fmt.Errorf("push: HEAD is not on a branch; name the destination")
		}
		dst := head.Target()
		if cfg, err := t.repo.Config(); err == nil {
			if b := cfg.Branches[head.Target().Short()]; b != nil && b.Merge != "" {
				dst = b.Merge
			}
		}
		return gitconfig.RefSpec(head.Target().String() + ":" + dst.String()), nil
	}
	force := strings.HasPrefix(refspec, "+")
	src, dst, hasDst := strings.Cut(strings.TrimPrefix(refspec, "+"), ":")
	if src == "" {
		return "", fmt.Errorf("push: refspec %q names no source", refspec)
	}
	srcRef, err := t.localRef(src)
	if err != nil {
		return "", err
	}
	if !hasDst {
		if !srcRef.IsBranch() && !srcRef.IsTag() {
			return "", fmt.Errorf("push: %q is not on a branch; name the destination", src)
		}
		dst = srcRef.String()
	} else if dst, err = t.remoteRef(remote, dst, srcRef, auth); err != nil {
		return "", err
	}
	spec := srcRef.String() + ":" + dst
	if force {
		spec = "+" + spec
	}
	return gitconfig.RefSpec(spec), nil
}

// localRef resolves a push source to the full ref it names: a symbolic ref (HEAD on a
// branch) to its target, a short name to its branch or tag.
func (t *embeddedTransport) localRef(name string) (plumbing.ReferenceName, error) {
	candidates := []plumbing.ReferenceName{plumbing.ReferenceName(name)}
	if !strings.HasPrefix(name, "refs/") && name != plumbing.HEAD.String() {
		candidates = []plumbing.ReferenceName{plumbing.NewBranchReferenceName(name), plumbing.NewTagReferenceName(name)}
	}
	var found []plumbing.ReferenceName
	for _, c := range candidates {
		ref, err := t.repo.Reference(c, false)
		if err != nil {
			continue
		}
		if ref.Type() == plumbing.SymbolicReference {
			c = ref.Target()
		}
		found = append(found, c)
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("push: %q matches no local branch or tag", name)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("push: %q is both a branch and a tag; name it in full (refs/heads/%s or refs/tags/%s)", name, name, name)
	}
}

// remoteRef resolves a push destination to a full ref name: as given when it starts with
// refs/, else the remote's branch or tag of that name, else the source's namespace.
func (t *embeddedTransport) remoteRef(remote, name string, src plumbing.ReferenceName, auth gittransport.AuthMethod) (string, error) {
	if strings.HasPrefix(name, "refs/") {
		return name, nil
	}
	r, err := t.repo.Remote(remote)
	if err != nil {
		return "", fmt.Errorf("push: remote %s: %w", remote, err)
	}
	refs, err := r.List(&git.ListOptions{Auth: auth})
	if err != nil && !errors.Is(err, gittransport.ErrEmptyRemoteRepository) {
		return "", fmt.Errorf("push: listing %s: %w", remote, err)
	}
	branch, tag := plumbing.NewBranchReferenceName(name), plumbing.NewTagReferenceName(name)
	var found []plumbing.ReferenceName
	for _, ref := range refs {
		if ref.Name() == branch || ref.Name() == tag {
			found = append(found, ref.Name())
		}
	}
	switch {
	case len(found) == 1:
		return found[0].String(), nil
	case len(found) > 1:
		return "", fmt.Errorf("push: %q is both a branch and a tag on %s; name it in full", name, remote)
	case src.IsBranch():
		return branch.String(), nil
	case src.IsTag():
		return tag.String(), nil
	}
	return "", fmt.Errorf("push: destination %q isn't a full ref name and %s has no branch or tag by that name", name, remote)
}

// RemoteRefHash resolves a branch head on the remote via go-git.
func (t *embeddedTransport) RemoteRefHash(remote, ref string) (plumbing.Hash, error) {
	auth, err := t.auth(remote)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return RemoteRefHash(t.repo, remote, ref, auth)
}
