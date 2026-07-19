package cistate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/paths"
)

// copyInto copies srcRoot/rel (a file or a directory tree) into dstRoot/rel,
// overwriting — modeling a GitLab artifact download that extracts files into the
// downstream job's workspace. A missing source is a no-op (that artifact simply
// carried nothing at that path).
func copyInto(t *testing.T, srcRoot, dstRoot, rel string) {
	t.Helper()
	src := filepath.Join(srcRoot, rel)
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	if !info.IsDir() {
		copyFile(t, src, filepath.Join(dstRoot, rel))
		return
	}
	if err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		r, _ := filepath.Rel(srcRoot, p)
		copyFile(t, p, filepath.Join(dstRoot, r))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSubsystemShards_UnionAcrossArtifactClobber reproduces the publish-authz bug
// and proves the fragment model.
//
// perform records {build} and forwards all of .stagefreight/ (its content store +
// the base pipeline.json). review clones perform's state, records {build,
// security}, and forwards ONLY its fragment set (security/, subsystems/) — it does
// NOT re-forward pipeline.json. publish (needs: [perform, review]) downloads both.
// The security outcome crosses SOLELY as subsystems/security.json, which never
// collides with perform's build.json, so ReadState unions build+security no matter
// the download order. (Before this, review re-forwarded pipeline.json; publish
// downloaded two copies at one path and the last write won — "perform last"
// dropped review's security and the gate saw "security did not run".)
func TestSubsystemShards_UnionAcrossArtifactClobber(t *testing.T) {
	perform := t.TempDir()
	if err := UpdateState(perform, func(s *State) {
		s.RecordSubsystem(SubsystemState{Name: "build", Attempted: true, Completed: true, Outcome: "success"})
	}); err != nil {
		t.Fatal(err)
	}

	review := t.TempDir()
	copyInto(t, perform, review, paths.Root) // review needs:[perform] → downloads its .stagefreight/
	if err := UpdateState(review, func(s *State) {
		s.RecordSubsystem(SubsystemState{Name: "security", Attempted: true, Completed: true, Outcome: "success"})
	}); err != nil {
		t.Fatal(err)
	}

	// perform forwards its whole .stagefreight/ (content store + base pipeline.json);
	// review forwards only its fragment (subsystems/) — never pipeline.json.
	forwardPerform := func(dst string) { copyInto(t, perform, dst, paths.Root) }
	forwardReview := func(dst string) { copyInto(t, review, dst, SubsystemsDir) }

	orders := []struct {
		name string
		dl   []func(string)
	}{
		{"perform-last (the order that used to lose security)", []func(string){forwardReview, forwardPerform}},
		{"review-last (the order that accidentally worked)", []func(string){forwardPerform, forwardReview}},
	}
	for _, o := range orders {
		t.Run(o.name, func(t *testing.T) {
			publish := t.TempDir()
			for _, dl := range o.dl {
				dl(publish)
			}
			st, err := ReadState(publish)
			if err != nil {
				t.Fatal(err)
			}
			if st.GetSubsystem("build") == nil {
				t.Fatalf("build subsystem missing after download order %q", o.name)
			}
			if st.GetSubsystem("security") == nil {
				t.Fatalf("security subsystem missing after download order %q — publish authz would wrongly deny", o.name)
			}
		})
	}
}

// TestReadState_NoShardsUnchanged pins back-compat: with no subsystems/ dir,
// ReadState returns exactly pipeline.json's subsystems (older workspaces / local
// runs are unaffected by the shard overlay).
func TestReadState_NoShardsUnchanged(t *testing.T) {
	root := t.TempDir()
	st := &State{}
	st.RecordSubsystem(SubsystemState{Name: "build", Attempted: true, Outcome: "success"})
	if err := WriteState(root, st); err != nil {
		t.Fatal(err)
	}
	// Remove the shard dir to simulate a pre-shard workspace.
	if err := os.RemoveAll(filepath.Join(root, SubsystemsDir)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetSubsystem("build") == nil || got.GetSubsystem("security") != nil {
		t.Fatalf("expected exactly {build} from pipeline.json, got %+v", got.Subsystems)
	}
}
