package build

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProvenanceStatement follows the in-toto Statement v1 / SLSA Provenance v1
// structure. Not full SLSA compliance, but a useful provenance document that
// can evolve into DSSE envelopes, cosign attestations, or OCI referrer artifacts.
type ProvenanceStatement struct {
	Type          string              `json:"_type"`
	PredicateType string              `json:"predicateType"`
	Subject       []ProvenanceSubject `json:"subject"`
	Predicate     ProvenancePredicate `json:"predicate"`
}

// ProvenanceSubject identifies what was built.
type ProvenanceSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest,omitempty"`
}

// ProvenancePredicate describes how it was built.
type ProvenancePredicate struct {
	BuildType    string                 `json:"buildType"`
	Builder      ProvenanceBuilder      `json:"builder"`
	Invocation   ProvenanceInvocation   `json:"invocation"`
	Metadata     ProvenanceMetadata     `json:"metadata"`
	Materials    []ProvenanceMaterial   `json:"materials,omitempty"`
	StageFreight map[string]any         `json:"stagefreight,omitempty"`
}

// ProvenanceBuilder identifies the build system.
type ProvenanceBuilder struct {
	ID string `json:"id"`
}

// ProvenanceInvocation captures the build parameters and environment.
type ProvenanceInvocation struct {
	ConfigSource map[string]any `json:"configSource,omitempty"`
	Parameters   map[string]any `json:"parameters,omitempty"`
	Environment  map[string]any `json:"environment,omitempty"`
}

// ProvenanceMetadata captures timing and completeness.
type ProvenanceMetadata struct {
	BuildStartedOn  string          `json:"buildStartedOn,omitempty"`
	BuildFinishedOn string          `json:"buildFinishedOn,omitempty"`
	Completeness    map[string]bool `json:"completeness,omitempty"`
	Reproducible    bool            `json:"reproducible"`
}

// ProvenanceMaterial represents an input to the build.
type ProvenanceMaterial struct {
	URI    string            `json:"uri"`
	Digest map[string]string `json:"digest,omitempty"`
}

// WriteProvenance writes a provenance statement as indented JSON.
func WriteProvenance(path string, stmt ProvenanceStatement) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating provenance dir: %w", err)
	}
	data, err := json.MarshalIndent(stmt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling provenance: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// DSSEEnvelope is a Dead Simple Signing Envelope for wrapping provenance statements.
type DSSEEnvelope struct {
	PayloadType string          `json:"payloadType"`
	Payload     string          `json:"payload"`    // base64-encoded ProvenanceStatement
	Signatures  []DSSESignature `json:"signatures"`
}

// DSSESignature is a single signature within a DSSE envelope.
type DSSESignature struct {
	KeyID string `json:"keyid,omitempty"`
	Sig   string `json:"sig"`
}

// WrapDSSE wraps a ProvenanceStatement in a DSSE envelope (unsigned).
// The caller can sign the payload externally (cosign) and attach the signature.
func WrapDSSE(stmt ProvenanceStatement) (DSSEEnvelope, error) {
	payload, err := json.Marshal(stmt)
	if err != nil {
		return DSSEEnvelope{}, err
	}
	return DSSEEnvelope{
		PayloadType: "application/vnd.in-toto+json",
		Payload:     base64.StdEncoding.EncodeToString(payload),
	}, nil
}

// WriteDSSEProvenance writes a DSSE-wrapped provenance statement as indented JSON.
func WriteDSSEProvenance(path string, stmt ProvenanceStatement) error {
	envelope, err := WrapDSSE(stmt)
	if err != nil {
		return fmt.Errorf("wrapping provenance in DSSE: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating DSSE provenance dir: %w", err)
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling DSSE provenance: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
