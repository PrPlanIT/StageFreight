// Package llm dispatches composed stencil bodies to model backends (the llms:
// library). It is IMPURE BY CONSTRUCTION — temperature/seed narrow but never
// guarantee byte-stability across a model bump — which is why its output is
// dispatch-only in the stencil language: stdout cards and notifications, never
// the committed record. One provider per function; new providers are engine
// additions behind the same config shape.
package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// generateTimeout bounds one model call. Generous — local models are slow — but
// finite: presentation must never hang a pipeline.
const generateTimeout = 90 * time.Second

// Generate sends the composed prompt to the configured backend and returns its
// text. Provider dispatch lives here; callers treat the result as markdown.
func Generate(cfg config.LLMConfig, prompt string) (string, error) {
	switch cfg.Provider {
	case "ollama":
		return ollamaGenerate(cfg, prompt)
	default:
		return "", fmt.Errorf("llm provider %q not implemented", cfg.Provider)
	}
}

// ollamaGenerate POSTs {url}/api/generate and extracts .response.
func ollamaGenerate(cfg config.LLMConfig, prompt string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"model":  cfg.Model,
		"prompt": prompt,
		"stream": false,
	})
	if err != nil {
		return "", err
	}
	url := strings.TrimSuffix(cfg.URL, "/") + "/api/generate"
	resp, err := (&http.Client{Timeout: generateTimeout}).Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama returned %s", resp.Status)
	}
	var out struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("ollama response: %w", err)
	}
	return strings.TrimSpace(out.Response), nil
}
