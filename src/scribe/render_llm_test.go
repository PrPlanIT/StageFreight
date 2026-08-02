package scribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// TestLLMStencil covers the AI stencil end to end: the body composes through the
// SAME pipeline as a text stencil (facts + embeds resolve into the prompt), the
// ollama response comes back as the stencil's markdown, and the result memoizes
// per run (one backend call however many consumers reference it).
func TestLLMStencil(t *testing.T) {
	calls := 0
	var gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotPrompt = req.Prompt
		_ = json.NewEncoder(w).Encode(map[string]string{"response": "Start with substituteInline."})
	}))
	defer srv.Close()

	rootDir := t.TempDir()
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name: "test", Attempted: true, Outcome: "failed",
			Results: map[string]string{"passed": "139", "total": "142"},
		})
	}); err != nil {
		t.Fatal(err)
	}

	appCfg := &config.Config{}
	appCfg.LLMs = config.OrderedLLMs{{ID: "local", Provider: "ollama", URL: srv.URL, Model: "llama3.2"}}
	appCfg.Stencils = config.OrderedStencils{
		{ID: "detail-a", Type: "text", Body: "Tests {tests.passed}/{tests.total}"},
		{ID: "triage-a", Type: "llm", LLM: "local", Body: "{detail-a}\n\nWhat broke?"},
	}
	def := appCfg.StencilsByID()["triage-a"]

	got, err := resolveStencilMarkdown(appCfg, def, "", "", &gitver.VersionInfo{}, rootDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Start with substituteInline." {
		t.Errorf("llm output: got %q", got)
	}
	if !strings.Contains(gotPrompt, "Tests 139/142") || !strings.Contains(gotPrompt, "What broke?") {
		t.Errorf("prompt should be the COMPOSED body (facts + embeds + ask): %q", gotPrompt)
	}

	// Memoized per run: a second consumer costs no backend call.
	if _, err := resolveStencilMarkdown(appCfg, def, "", "", &gitver.VersionInfo{}, rootDir); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("backend called %d times; want 1 (memoized per run)", calls)
	}
}

// TestLLMStencil_Degrades: a dead backend degrades the stencil to empty —
// presentation never hard-fails — and the failure memoizes (warn once, not per
// consumer).
func TestLLMStencil_Degrades(t *testing.T) {
	appCfg := &config.Config{}
	appCfg.LLMs = config.OrderedLLMs{{ID: "dead", Provider: "ollama", URL: "http://127.0.0.1:1", Model: "m"}}
	appCfg.Stencils = config.OrderedStencils{
		{ID: "triage-dead", Type: "llm", LLM: "dead", Body: "hello?"},
	}
	got, err := resolveStencilMarkdown(appCfg, appCfg.StencilsByID()["triage-dead"], "", "", &gitver.VersionInfo{}, t.TempDir())
	if err != nil {
		t.Fatalf("llm errors must degrade, not fail: %v", err)
	}
	if got != "" {
		t.Errorf("dead backend should render empty (the embed line elides): %q", got)
	}
}
