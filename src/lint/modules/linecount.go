package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

const defaultMaxLines = 1000

func init() {
	lint.Register("linecount", func() lint.Module {
		return &linecountModule{cfg: linecountConfig{MaxLines: defaultMaxLines}}
	})
}

type linecountConfig struct {
	MaxLines int `json:"max_lines"`
}

type linecountModule struct {
	cfg linecountConfig
}

func (m *linecountModule) Name() string        { return "linecount" }
func (m *linecountModule) DefaultEnabled() bool { return true }
func (m *linecountModule) AutoDetect() []string { return nil }

// Configure implements lint.ConfigurableModule.
func (m *linecountModule) Configure(opts map[string]any) error {
	cfg := linecountConfig{MaxLines: defaultMaxLines}
	if len(opts) != 0 {
		b, err := json.Marshal(opts)
		if err != nil {
			return fmt.Errorf("linecount: marshal options: %w", err)
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("linecount: unmarshal options: %w", err)
		}
	}
	if cfg.MaxLines < 0 {
		return fmt.Errorf("linecount: max_lines must be non-negative, got %d", cfg.MaxLines)
	}
	if cfg.MaxLines == 0 {
		cfg.MaxLines = defaultMaxLines
	}
	m.cfg = cfg
	return nil
}

func (m *linecountModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}

	count := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		count++
	}

	if count <= m.cfg.MaxLines {
		return nil, nil
	}

	return []lint.Finding{
		{
			File:     file.Path,
			Line:     count,
			Module:   m.Name(),
			Severity: lint.SeverityWarning,
			Message:  fmt.Sprintf("file has %d lines, exceeds threshold %d", count, m.cfg.MaxLines),
		},
	}, nil
}
