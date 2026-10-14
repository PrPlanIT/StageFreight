package security

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SBOMFile is the SPDX JSON SBOM of the scanned image, in the scan's output directory.
const SBOMFile = "sbom.spdx.json"

// SBOMSubjectFile records which build and image the SBOM describes, so a consumer
// importing it can tell whether it belongs to the build it is rendering.
const SBOMSubjectFile = "sbom.subject.json"

// SBOMSubject is the content of SBOMSubjectFile.
type SBOMSubject struct {
	Build string `json:"build,omitempty"`
	Ref   string `json:"ref"`
}

func writeSBOMSubject(dir string, s SBOMSubject) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, SBOMSubjectFile), append(data, '\n'), 0o644)
}

// ReadSBOMSubject reads the record of which build an output directory's SBOM describes.
func ReadSBOMSubject(dir string) (SBOMSubject, error) {
	var s SBOMSubject
	data, err := os.ReadFile(filepath.Join(dir, SBOMSubjectFile))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}
