package presetref

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// Fetcher does the actual remote I/O for a preset source; the Resolver stays pure policy
// over it, so the tracked/pinned/fallback logic is testable without a network.
// Revisioner is an optional Fetcher capability: report what the source currently points
// at, without transferring content. A fetcher that cannot answer cheaply omits it and
// every resolve fetches in full.
type Revisioner interface {
	Revision(source, ref string) (string, error)
}

// AgedCache is an optional Cache capability: how long ago an entry was retained. Without
// it a fallback cannot be bounded, because there is no way to tell a copy retained five
// minutes ago from one retained five months ago.
type AgedCache interface {
	Age(key string) (time.Duration, bool)
}

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
// Outcome records how one reference resolved. A tracked source changing is the
// behaviour the operator selected by not pinning, so this exists to make the change
// visible and auditable — not to flag it as suspect.
type Outcome struct {
	Ref       Ref
	Kind      Kind
	Fetched   bool // reached the source on this run
	Drifted   bool // what the source served differs from what was retained
	Fallback  bool // source unreachable; the retained copy was served instead
	Violated  bool // pinned, and the source no longer serves what was retained
	Unchanged bool // the source points at what was retained; nothing transferred
}

// RevisionSuffix keys the recorded revision beside its content, so a distributor can
// seed it and a resolver can find it.
const RevisionSuffix = ".revision"

// DefaultMaxFallbackAge is how long a retained copy may stand in for an unreachable
// source before resolution stops accepting it. Unbounded is not a safe default: it is
// how a source that quietly stops answering leaves everyone frozen on old content. A
// week absorbs a long outage while still surfacing one that is not going to end.
const DefaultMaxFallbackAge = 7 * 24 * time.Hour

type Resolver struct {
	Fetcher Fetcher
	Cache   Cache
	Mode    FetchMode
	// Observe, if set, receives one Outcome per resolved reference.
	Observe func(Outcome)
	// MaxFallbackAge bounds how long a retained copy may stand in for an unreachable
	// source. Zero is unbounded. Without a bound, a source that stays unreachable leaves
	// every consumer silently frozen on old content — the failure this whole mechanism
	// exists to remove, arriving by a different route.
	MaxFallbackAge time.Duration
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
	retained, had := r.Cache.Read(key)

	// Resolution is ONE operation for every kind: obtain what the source serves, and
	// reconcile it against what was retained. Only the policy on a mismatch differs.
	// Serving a pin from cache without asking the source would make the pin
	// unverifiable — precisely the check an operator pins in order to get.
	if r.Mode == FetchOffline {
		if had {
			r.observe(Outcome{Ref: ref, Kind: kind, Fallback: true})
			return retained, nil
		}
		return nil, fmt.Errorf("preset %q not in cache and offline", ref.Raw)
	}

	// Ask what the source points at before transferring anything. When it matches what
	// was retained, the content cannot have changed, so there is nothing to fetch and
	// nothing to reconcile — this is what makes tracking cheap enough to do every run,
	// and a pin cheap enough to verify every run.
	if rev, ok := r.Fetcher.(Revisioner); ok && had {
		if cur, rerr := rev.Revision(ref.Source, ref.Ref); rerr == nil && cur != "" {
			if prev, ok := r.Cache.Read(key + RevisionSuffix); ok && string(prev) == cur {
				r.observe(Outcome{Ref: ref, Kind: kind, Fetched: true, Unchanged: true})
				return retained, nil
			}
		}
	}

	fetched, err := r.Fetcher.Fetch(ref.Source, ref.Ref, ref.Path)
	if err != nil {
		// Fall back to the retained copy whatever the kind: an unreachable source is
		// not evidence that anything changed.
		if had {
			if age, aerr := r.retainedAge(key); aerr && r.MaxFallbackAge > 0 && age > r.MaxFallbackAge {
				return nil, fmt.Errorf("preset %q: source unreachable (%v) and the retained copy is %s old, past the %s bound — "+
					"a fallback that never expires is a freeze nobody declared", ref.Raw, err, age.Round(time.Hour), r.MaxFallbackAge)
			}
			r.warnf("preset %q: fetch failed (%v); using retained copy", ref.Raw, err)
			r.observe(Outcome{Ref: ref, Kind: kind, Fallback: true})
			return retained, nil
		}
		return nil, fmt.Errorf("preset %q: fetch failed and nothing retained: %w", ref.Raw, err)
	}

	differs := had && !bytes.Equal(retained, fetched)

	if kind == Pinned && differs {
		// A pinned reference names a revision that is supposed to be immutable. If the
		// source now serves something else, the assumption is broken — a moved tag,
		// rewritten history, or a substituted host — and the retained copy is the
		// evidence. Report it rather than silently choosing a side.
		r.observe(Outcome{Ref: ref, Kind: kind, Fetched: true, Violated: true})
		return nil, &PinViolation{Ref: ref, Retained: retained, Fetched: fetched}
	}

	// Compare before overwriting: once the retained copy is replaced, the fact that the
	// source moved is unrecoverable, and that fact is what a satellite reports and
	// republishes.
	_ = r.Cache.Write(key, fetched)
	if rev, ok := r.Fetcher.(Revisioner); ok {
		if cur, rerr := rev.Revision(ref.Source, ref.Ref); rerr == nil && cur != "" {
			_ = r.Cache.Write(key+RevisionSuffix, []byte(cur))
		}
	}
	r.observe(Outcome{Ref: ref, Kind: kind, Fetched: true, Drifted: differs})
	return fetched, nil
}

// PinViolation reports a pinned reference whose source no longer serves what was
// retained. Returned as an error so the default is to stop; a caller that would rather
// warn can recognize it and continue with Retained.
type PinViolation struct {
	Ref      Ref
	Retained []byte
	Fetched  []byte
}

func (e *PinViolation) Error() string {
	return fmt.Sprintf("pinned preset %q: the source no longer serves what was retained "+
		"(%d bytes retained, %d fetched) — the pinned revision moved, or the source was substituted",
		e.Ref.Raw, len(e.Retained), len(e.Fetched))
}

// retainedAge reports the age of a retained entry when the cache can tell.
func (r Resolver) retainedAge(key string) (time.Duration, bool) {
	if ac, ok := r.Cache.(AgedCache); ok {
		return ac.Age(key)
	}
	return 0, false
}

func (r Resolver) observe(o Outcome) {
	if r.Observe != nil {
		r.Observe(o)
	}
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

func (r Resolver) warnf(format string, args ...any) {
	if r.Warnf != nil {
		r.Warnf(format, args...)
	}
}

// CacheKey is the retention-store key for a ref: source + ref + path, sanitized to a safe
// relative path. A pinned ref's key includes its immutable ref; a tracked ref's key is
// stable across runs so a re-fetch overwrites the same entry.
func CacheKey(ref Ref) string {
	// The ref is part of the identity only when there is one: an unpinned reference
	// tracks the default branch, and rendering its absence as a separator leaves a
	// stray "-" in every retained path.
	key := ref.Source
	if ref.Ref != "" {
		key += "@" + ref.Ref
	}
	return sanitizeKey(key + "/" + ref.Path)
}

func sanitizeKey(s string) string {
	repl := strings.NewReplacer("://", "-", ":", "-", "@", "-", "..", "-")
	return strings.Trim(repl.Replace(s), "/")
}
