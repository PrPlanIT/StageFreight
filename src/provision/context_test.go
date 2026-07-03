package provision

import (
	"context"
	"testing"
)

func TestContextLedger_RecordDedupCollectFlush(t *testing.T) {
	ctx := WithLedger(context.Background())

	Record(ctx, Entry{Tool: "cosign", Version: "2.6.1", Verified: "pinned"})
	Record(ctx, Entry{Tool: "cosign", Version: "2.6.1", Verified: "pinned"}) // dup
	Record(ctx, Entry{Tool: "trivy", Version: "0.69.3", Verified: "checksum"})
	Record(ctx, Entry{Tool: "", Version: "x"}) // empty tool ignored

	if got := Collected(ctx); len(got) != 2 {
		t.Fatalf("Collected len = %d, want 2 (dedup + empty dropped): %+v", len(got), got)
	}

	// FlushCollected returns the delta, then only new entries next time.
	if got := FlushCollected(ctx); len(got) != 2 {
		t.Fatalf("first flush = %d, want 2", len(got))
	}
	if got := FlushCollected(ctx); len(got) != 0 {
		t.Fatalf("second flush = %d, want 0 (no new)", len(got))
	}
	Record(ctx, Entry{Tool: "syft", Version: "1.0"})
	if got := FlushCollected(ctx); len(got) != 1 || got[0].Tool != "syft" {
		t.Fatalf("third flush = %+v, want [syft]", got)
	}
}

func TestContextLedger_NoLedgerIsSafe(t *testing.T) {
	// Resolve/Record with a bare ctx must not panic and must no-op the recording.
	ctx := context.Background()
	Record(ctx, Entry{Tool: "x", Version: "1"})
	if got := Collected(ctx); got != nil {
		t.Fatalf("Collected with no ledger = %+v, want nil", got)
	}
}
