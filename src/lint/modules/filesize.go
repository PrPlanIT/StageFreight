package modules

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

const defaultMaxBytes int64 = 500 * 1024 // 500 KB

func init() {
	lint.Register("filesize", func() lint.Module {
		return &filesizeModule{cfg: filesizeConfig{MaxBytes: defaultMaxBytes}}
	})
}

type filesizeConfig struct {
	MaxBytes int64 `json:"max_bytes"`
}

type filesizeModule struct {
	cfg filesizeConfig
}

func (m *filesizeModule) Name() string        { return "filesize" }
func (m *filesizeModule) DefaultEnabled() bool { return true }
func (m *filesizeModule) AutoDetect() []string { return nil }

// Configure implements lint.ConfigurableModule.
func (m *filesizeModule) Configure(opts map[string]any) error {
	cfg := filesizeConfig{MaxBytes: defaultMaxBytes}
	if len(opts) != 0 {
		b, err := json.Marshal(opts)
		if err != nil {
			return fmt.Errorf("filesize: marshal options: %w", err)
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("filesize: unmarshal options: %w", err)
		}
	}
	if cfg.MaxBytes < 0 {
		return fmt.Errorf("filesize: max_bytes must be non-negative, got %d", cfg.MaxBytes)
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = defaultMaxBytes
	}
	m.cfg = cfg
	return nil
}

func (m *filesizeModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	if file.Size <= m.cfg.MaxBytes {
		return nil, nil
	}

	return []lint.Finding{
		{
			File:     file.Path,
			Line:     1,
			Module:   m.Name(),
			Severity: lint.SeverityWarning,
			Message:  fmt.Sprintf("file size %s exceeds threshold %s", humanSize(file.Size), humanSize(m.cfg.MaxBytes)),
		},
	}, nil
}

func humanSize(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
