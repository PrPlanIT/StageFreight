package provision

import (
	"context"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// Request-scoped provisioning collector. Carried in context.Context — the idiomatic
// Go channel for request-scoped ambient data (like a logger or trace span), NOT a
// package-global. A run seeds one collector; every tool resolved through Resolve
// records into it; the presentation layer (cli/cmd) reads it back with Collected /
// FlushCollected and renders. Domain code produces DATA into the collector and never
// renders — the render boundary (see render_boundary_test.go) still holds.

type ledgerKey struct{}

type collector struct {
	mu      sync.Mutex
	entries []Entry
	flushed int // for FlushCollected's per-phase delta
}

// WithLedger returns a context carrying a fresh provisioning collector. Call once at
// the start of a run. Tool resolutions through Resolve(ctx, …) record into it.
func WithLedger(ctx context.Context) context.Context {
	return context.WithValue(ctx, ledgerKey{}, &collector{})
}

func collectorFrom(ctx context.Context) *collector {
	c, _ := ctx.Value(ledgerKey{}).(*collector)
	return c // nil when no ledger seeded — callers degrade to "resolve, don't record"
}

// Resolve is the sensible provisioning call: it resolves a tool via the toolchain
// engine AND records it (with trust from Result.Trust + purpose) in the ctx collector,
// if one is present. Use this instead of toolchain.Resolve so a tool lands in "Staged
// Tools" by construction — no per-caller result plumbing. Safe with a bare ctx (no
// ledger): it just resolves. toolchain.Resolve stays pure; this is the recording seam.
func Resolve(ctx context.Context, rootDir, tool, version, purpose string) (toolchain.Result, error) {
	res, err := toolchain.Resolve(rootDir, tool, version)
	if err != nil {
		return res, err
	}
	if c := collectorFrom(ctx); c != nil {
		c.record(FromToolchain(res, purpose))
	}
	return res, nil
}

// Record adds an already-built Entry to the ctx collector — for tools acquired outside
// Resolve (ResolvePinned, EnsureRustLlvmTools) or native substrate rows.
func Record(ctx context.Context, e Entry) {
	if e.Tool == "" {
		return
	}
	if c := collectorFrom(ctx); c != nil {
		c.record(e)
	}
}

// RecordCtxAll records a batch (e.g. FromSubstrate output) into the ctx collector.
func RecordCtxAll(ctx context.Context, es []Entry) {
	for _, e := range es {
		Record(ctx, e)
	}
}

func (c *collector) record(e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.entries {
		if x.Tool == e.Tool && x.Version == e.Version {
			return // dedup by tool+version
		}
	}
	c.entries = append(c.entries, e)
}

// Collected returns a copy of everything recorded in this ctx's collector — the whole
// run's tools, flushed or not. For a consolidated receipt or the structured artifact.
func Collected(ctx context.Context) []Entry {
	c := collectorFrom(ctx)
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Entry(nil), c.entries...)
}

// FlushCollected returns the entries recorded since the last FlushCollected — the
// per-phase delta, so each phase's runner can render just the tools it pulled
// (streaming, before_script-style). Empty when nothing new was provisioned.
func FlushCollected(ctx context.Context) []Entry {
	c := collectorFrom(ctx)
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]Entry(nil), c.entries[c.flushed:]...)
	c.flushed = len(c.entries)
	return out
}
