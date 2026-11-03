package promote

import (
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
)

// A single image manifest is not an index; a multi-platform or attestation-wrapped one is.
func TestIsIndex(t *testing.T) {
	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	sub := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img})
	dir := t.TempDir()
	if _, err := layout.Write(dir, mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: img}, mutate.IndexAddendum{Add: sub})); err != nil {
		t.Fatal(err)
	}
	imgDig, _ := img.Digest()
	subDig, _ := sub.Digest()

	if got, err := IsIndex(dir, imgDig.String()); err != nil || got {
		t.Fatalf("image entry: IsIndex = %v, %v; want false", got, err)
	}
	if got, err := IsIndex(dir, subDig.String()); err != nil || !got {
		t.Fatalf("index entry: IsIndex = %v, %v; want true", got, err)
	}
	if _, err := IsIndex(dir, "sha256:"+"0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("a digest not in the layout must be an error, not a guess")
	}
}
