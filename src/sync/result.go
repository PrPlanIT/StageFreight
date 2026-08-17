// Package sync implements forge accessory synchronization.
//
// Two sync classes exist, ordered and not peers:
//
//  1. Git mirror — transport-level replication via git push --mirror.
//     All refs, branches, tags, deletions, force updates.
//     Mirrored from the authoritative local worktree.
//
//  2. Artifact projection — API-level for forge-native objects (releases).
//     Runs only after git mirror succeeds for that accessory.
//
// Core invariant: Git is the source of truth; everything else is downstream projection.
package sync

import "time"

// SyncStatus represents the outcome of a sync operation.
type SyncStatus string

const (
	SyncSuccess SyncStatus = "success"
	SyncFailed  SyncStatus = "failed"
	SyncSkipped SyncStatus = "skipped"
)

// MirrorFailureReason classifies git mirror push failures.
// Classification is best-effort via stderr substring matching.
// Fallback is always MirrorUnknown — classification must never crash.
type MirrorFailureReason string

const (
	MirrorAuthFailed           MirrorFailureReason = "auth_failed"
	MirrorProtectedRefRejected MirrorFailureReason = "protected_ref_rejected"
	MirrorNetworkFailed        MirrorFailureReason = "network_failed"
	MirrorRemoteNotFound       MirrorFailureReason = "remote_not_found"
	MirrorPushRejected         MirrorFailureReason = "push_rejected"
	// MirrorDiverged: the push was rejected because an in-scope mirror ref diverged
	// (differs and the facet did not opt into force) — keep-divergent working as
	// intended, not a transport or remote fault. Named explicitly so it is not
	// substring-guessed from go-git's generic "object not found" wording.
	MirrorDiverged MirrorFailureReason = "diverged"
	MirrorUnknown  MirrorFailureReason = "unknown"
)

// MirrorResult reports the outcome of a git mirror push to one accessory.
type MirrorResult struct {
	AccessoryID   string
	Status        SyncStatus
	Duration      time.Duration
	Degraded      bool // true when mirror failed — accessory is diverged
	FailureReason MirrorFailureReason
	Message       string // sanitized human-readable message (never contains credentials)
}

// ReleaseResult reports the outcome of release projection to one accessory.
type ReleaseResult struct {
	AccessoryID string
	Status      SyncStatus
	Message     string
}
