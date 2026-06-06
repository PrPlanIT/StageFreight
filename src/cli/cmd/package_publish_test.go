package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// fakePackageForge implements only the generic-package methods of forge.Forge;
// the embedded interface satisfies the rest (and panics if an untested method
// is ever called).
type fakePackageForge struct {
	forge.Forge
	versions  []forge.PackageVersion
	published []string // "version/file"
	deleted   []string
}

func (f *fakePackageForge) PublishPackageFile(ctx context.Context, opts forge.PublishPackageOptions) (*forge.PublishedPackage, error) {
	f.published = append(f.published, opts.Version+"/"+opts.FileName)
	return &forge.PublishedPackage{
		PackageName: opts.PackageName, Version: opts.Version, FileName: opts.FileName,
		PullURL: "https://forge/" + opts.PackageName + "/" + opts.Version + "/" + opts.FileName,
	}, nil
}

func (f *fakePackageForge) ListPackageVersions(ctx context.Context, name string) ([]forge.PackageVersion, error) {
	return f.versions, nil
}

func (f *fakePackageForge) DeletePackageVersion(ctx context.Context, name, version string) error {
	f.deleted = append(f.deleted, version)
	return nil
}

func twoAssets() []artifact.ResolvedArchiveAsset {
	return []artifact.ResolvedArchiveAsset{
		{ArtifactID: "archive:amd64", Name: "app-linux-amd64.tar.gz", Path: "dist/app-linux-amd64.tar.gz"},
		{ArtifactID: "archive:arm64", Name: "app-linux-arm64.tar.gz", Path: "dist/app-linux-arm64.tar.gz"},
	}
}

// Immutable version doesn't exist yet: publish all immutable files, then refresh
// the alias (delete-then-publish).
func TestPublishPackageTarget_ImmutableNewPlusAlias(t *testing.T) {
	fc := &fakePackageForge{} // no existing versions
	res, err := publishPackageTarget(context.Background(), fc, "pkg", "app", "dev-abc12345", []string{"latest-dev"}, twoAssets())
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.ImmutableSkipped {
		t.Error("immutable should NOT be skipped when it doesn't exist")
	}
	// 2 immutable files + 2 alias files = 4 publishes
	if len(fc.published) != 4 {
		t.Fatalf("published = %v, want 4", fc.published)
	}
	// alias deleted before publish (rolling overwrite)
	if len(fc.deleted) != 1 || fc.deleted[0] != "latest-dev" {
		t.Fatalf("deleted = %v, want [latest-dev]", fc.deleted)
	}
	if len(res.Files) != 2 {
		t.Fatalf("res.Files = %v, want 2 immutable files", res.Files)
	}
}

// Immutable version already exists: skip it entirely (never overwrite), but still
// refresh the rolling alias.
func TestPublishPackageTarget_ImmutableExistsSkipsButRefreshesAlias(t *testing.T) {
	fc := &fakePackageForge{versions: []forge.PackageVersion{{ID: "5", Version: "dev-abc12345"}}}
	res, err := publishPackageTarget(context.Background(), fc, "pkg", "app", "dev-abc12345", []string{"latest-dev"}, twoAssets())
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !res.ImmutableSkipped {
		t.Error("immutable SHOULD be skipped when it already exists")
	}
	// only the 2 alias files published (immutable skipped)
	if len(fc.published) != 2 {
		t.Fatalf("published = %v, want 2 (alias only)", fc.published)
	}
	for _, p := range fc.published {
		if !strings.HasPrefix(p, "latest-dev/") {
			t.Fatalf("expected only latest-dev publishes, got %v", fc.published)
		}
	}
	if len(fc.deleted) != 1 || fc.deleted[0] != "latest-dev" {
		t.Fatalf("alias should be deleted-then-published, deleted=%v", fc.deleted)
	}
}

// Retention candidate set is the dev-* family from the version template; the
// rolling alias is never pruned.
func TestPrunePackageTarget_PrunesDevFamilyProtectsAlias(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	fc := &fakePackageForge{versions: []forge.PackageVersion{
		{ID: "5", Version: "dev-005", CreatedAt: base.Add(5 * time.Hour)},
		{ID: "4", Version: "dev-004", CreatedAt: base.Add(4 * time.Hour)},
		{ID: "3", Version: "dev-003", CreatedAt: base.Add(3 * time.Hour)},
		{ID: "2", Version: "dev-002", CreatedAt: base.Add(2 * time.Hour)},
		{ID: "1", Version: "dev-001", CreatedAt: base.Add(1 * time.Hour)},
		{ID: "L", Version: "latest-dev", CreatedAt: base.Add(6 * time.Hour)}, // newest, but an alias
	}}
	policy := config.RetentionPolicy{KeepLast: 2}

	res, err := prunePackageTarget(context.Background(), fc, "app", "dev-{sha:8}", []string{"latest-dev"}, policy)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	// candidates = dev-005..dev-001; keep_last 2 keeps dev-005/dev-004, prunes 3.
	if len(res.Deleted) != 3 {
		t.Fatalf("deleted = %v, want 3", res.Deleted)
	}
	for _, d := range fc.deleted {
		if d == "latest-dev" {
			t.Fatal("latest-dev must never be pruned")
		}
		if !strings.HasPrefix(d, "dev-") {
			t.Fatalf("pruned a non-dev version: %s", d)
		}
		if d == "dev-005" || d == "dev-004" {
			t.Fatalf("newest dev versions must be kept, but pruned %s", d)
		}
	}
}
