package presetref

import (
	"fmt"
	"strings"
)

// Fetcher does the actual remote I/O for a preset source; the Resolver stays pure policy
// over it, so the tracked/pinned/fallback logic is testable without a network.
type Fetcher interface {
	// Fetch returns the file at path from source at ref (ref "" = the default branch).
	Fetch(source, ref, path string) ([]byte, error)
	// Classify resolves a BARE Named ref against the source: a tag → Pinned, a branch →
	// Tracked. Called only for Named refs.
	Classify(source, ref string) (Kind, error)
}

// Cache is the retention store for fetched presets — the fallback for tracked refs and
// the authoritative store for pinned refs. Keyed by a ref's CacheKey.
type Cache interface {
	Read(key string) ([]byte, bool)
	Write(key string, content []byte) error
}

// FetchMode controls network behavior.
type FetchMode int

const (
	// FetchLive is the default: a tracked ref is fetched each run (cache as fallback); a
	// pinned ref is served from cache if present, else fetched once.
	FetchLive FetchMode = iota
	// FetchOffline never touches the network: everything resolves from cache, and a miss
	// is an error. For air-gapped CI running against a pre-seeded cache.
	FetchOffline
)

// Resolver resolves a SOURCED preset Ref (Tracked/Pinned/Named) to its content, applying
// the source-tracking policy. Local refs are out of scope — the local loader handles them.
type Resolver struct {
	Fetcher Fetcher
	Cache   Cache
	Mode    FetchMode
	// Warnf, if set, reports a non-fatal condition (e.g. a stale-cache fallback).
	Warnf func(format string, args ...any)
}

// Resolve returns the content for a sourced ref. It errors on a Local ref (wrong
// resolver) and when a fetch is unrecoverable with no cache to fall back to.
func (r Resolver) Resolve(ref Ref) ([]byte, error) {
	if ref.Kind == Local {
		return nil, fmt.Errorf("preset %q is local, not sourced", ref.Raw)
	}
	kind := r.resolveKind(ref)
	key := CacheKey(ref)

	if kind == Pinned {
		// Immutable: the cache is authoritative — use it when present, else fetch once.
		if c, ok := r.Cache.Read(key); ok {
			return c, nil
		}
		if r.Mode == FetchOffline {
			return nil, fmt.Errorf("pinned preset %q not in cache and offline", ref.Raw)
		}
		return r.fetchAndCache(ref, key)
	}

	// Tracked (or a Named ref that degraded to tracked).
	if r.Mode == FetchOffline {
		if c, ok := r.Cache.Read(key); ok {
			return c, nil
		}
		return nil, fmt.Errorf("tracked preset %q not in cache and offline", ref.Raw)
	}
	// Fetch live; on failure fall back to the retained (stale) cache with a warning.
	c, err := r.Fetcher.Fetch(ref.Source, ref.Ref, ref.Path)
	if err == nil {
		_ = r.Cache.Write(key, c)
		return c, nil
	}
	if cached, ok := r.Cache.Read(key); ok {
		r.warnf("tracked preset %q: live fetch failed (%v); using retained cache", ref.Raw, err)
		return cached, nil
	}
	return nil, fmt.Errorf("tracked preset %q: fetch failed and no cache fallback: %w", ref.Raw, err)
}

// resolveKind resolves a Named ref to Tracked/Pinned via the source. A classify failure
// degrades to Tracked (fetch-with-fallback) rather than hard-failing — classification is
// an optimization (cache-authoritative pinning), never a gate on resolving at all.
func (r Resolver) resolveKind(ref Ref) Kind {
	if ref.Kind != Named {
		return ref.Kind
	}
	if r.Mode == FetchOffline {
		return Tracked // can't classify offline; the tracked cache-only path handles it
	}
	if k, err := r.Fetcher.Classify(ref.Source, ref.Ref); err == nil {
		return k
	}
	r.warnf("preset %q: could not classify ref %q as branch/tag; treating as tracked", ref.Raw, ref.Ref)
	return Tracked
}

func (r Resolver) fetchAndCache(ref Ref, key string) ([]byte, error) {
	c, err := r.Fetcher.Fetch(ref.Source, ref.Ref, ref.Path)
	if err != nil {
		return nil, fmt.Errorf("pinned preset %q: fetch failed: %w", ref.Raw, err)
	}
	_ = r.Cache.Write(key, c)
	return c, nil
}

func (r Resolver) warnf(format string, args ...any) {
	if r.Warnf != nil {
		r.Warnf(format, args...)
	}
}

// CacheKey is the retention-store key for a ref: source + ref + path, sanitized to a safe
// relative path. A pinned ref's key includes its immutable ref; a tracked ref's key is
// stable across runs so a re-fetch overwrites the same entry.
func CacheKey(ref Ref) string {
	return sanitizeKey(ref.Source + "@" + ref.Ref + "/" + ref.Path)
}

func sanitizeKey(s string) string {
	repl := strings.NewReplacer("://", "-", ":", "-", "@", "-", "..", "-")
	return strings.Trim(repl.Replace(s), "/")
}
