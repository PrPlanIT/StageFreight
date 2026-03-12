package modules

import (
	"context"
	"os"
	"sync"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("secrets", func() lint.Module { return &secretsModule{} })
}

type secretsModule struct {
	once     sync.Once
	detector *detect.Detector
	initErr  error
}

func (m *secretsModule) Name() string        { return "secrets" }
func (m *secretsModule) DefaultEnabled() bool { return true }
func (m *secretsModule) AutoDetect() []string { return nil }

func (m *secretsModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	m.once.Do(func() {
		m.detector, m.initErr = detect.NewDetectorDefaultConfig()
	})
	if m.initErr != nil {
		return nil, m.initErr
	}

	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}

	hits := m.detector.DetectBytes(data)
	if len(hits) == 0 {
		return nil, nil
	}

	findings := make([]lint.Finding, 0, len(hits))
	for _, h := range hits {
		findings = append(findings, lint.Finding{
			File:     file.Path,
			Line:     h.StartLine + 1, // gitleaks is 0-indexed
			Module:   m.Name(),
			Severity: lint.SeverityCritical,
			Message:  h.Description + " (" + h.RuleID + ")",
		})
	}
	return findings, nil
}
