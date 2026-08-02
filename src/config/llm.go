package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// LLMConfig is one entry in the top-level llms: block — a model endpoint library,
// sibling of forges:/registries:. It earns library status because the alternative
// is endpoints and credentials leaking into stencils: (an AI stencil references a
// backend by id — `llm: local` — and stays pure composition). Provider is the
// engine axis: ollama ships first; openai/anthropic/claude-agent slot in behind
// the same shape (engine additions, zero reshape).
type LLMConfig struct {
	ID          string `yaml:"-"`                     // the map key ({llm: <id>} reference target)
	Provider    string `yaml:"provider"`              // ollama (openai | anthropic | claude-agent reserved)
	URL         string `yaml:"url,omitempty"`         // ollama: server base URL
	Model       string `yaml:"model,omitempty"`       // model name/tag
	Credentials string `yaml:"credentials,omitempty"` // env prefix for hosted providers
}

// OrderedLLMs is the llms: map — id → LLMConfig in document order.
type OrderedLLMs []LLMConfig

func (o *OrderedLLMs) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(l *LLMConfig, id string) { l.ID = id })
	if err != nil {
		return fmt.Errorf("llms: %w", err)
	}
	*o = v
	return nil
}

// ByID returns a lookup map of the llms library keyed by id.
func (o OrderedLLMs) ByID() map[string]LLMConfig {
	m := make(map[string]LLMConfig, len(o))
	for _, l := range o {
		m[l.ID] = l
	}
	return m
}

// LLMTaintedStencils returns every stencil id whose output can carry AI text: the
// type: llm stencils plus any text stencil embedding a tainted stencil,
// transitively (fixpoint). AI output is DISPATCH-ONLY — stdout cards and
// notifications — never the committed record: scribe file regions reject these
// ids at validation (the determinism boundary).
func LLMTaintedStencils(stencils OrderedStencils) map[string]bool {
	tainted := map[string]bool{}
	for _, s := range stencils {
		if s.EffectiveKind() == "llm" {
			tainted[s.ID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, s := range stencils {
			if tainted[s.ID] || s.Body == "" {
				continue
			}
			for _, ref := range BodyRefs(s.Body) {
				if tainted[ref] {
					tainted[s.ID] = true
					changed = true
					break
				}
			}
		}
	}
	return tainted
}
