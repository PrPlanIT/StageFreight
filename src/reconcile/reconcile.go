// Package reconcile implements repository reconciliation — a class of mutation
// distinct from dependency updates.
//
// StageFreight makes two fundamentally different kinds of change:
//
//   - Intentful mutations: StageFreight selects among valid futures (a newer
//     version is available — should we take it?). These flow through candidate
//     generation and policy (max_update, holds, review). There is a search space
//     and discretion.
//
//   - Reconciliation mutations: StageFreight makes the repository internally
//     consistent with intent it ALREADY encodes (one file declares a requirement
//     another file violates). There is no search space, no policy, and no
//     discretion — the answer is already written down; the engine derives it or,
//     when it cannot be derived canonically, reports a configuration error.
//
// The governing invariant: a reconciler may only mutate state that is
// functionally dependent on an authoritative source, and authority flows one
// direction only (e.g. go.mod's `go` directive is authoritative for the golang
// builder image, never the reverse). This keeps reconciliation from ever becoming
// "automatic updates" under another name.
//
// The first reconciler (go-toolchain, gotoolchain.go) enforces that a golang
// builder image satisfies its module's `go` directive floor. Future reconcilers
// of the same species — Rust edition ↔ toolchain, Cargo MSRV, Node engine ↔
// builder, Terraform version ↔ runner — add sibling files returning the same
// Result vocabulary.
package reconcile

// Mutation is a derived, non-discretionary change that makes the repository
// internally consistent with an already-encoded authoritative constraint. It is
// NOT an update: no version was chosen among valid futures. It is the single
// canonical representation the repository already requires.
type Mutation struct {
	Reconciler string // which reconciler derived it, e.g. "go-toolchain"
	File       string // repo-relative path to edit
	Line       int    // 1-based line to replace
	From       string // current token, e.g. "golang:1.24.13"
	To         string // canonical token, e.g. "golang:1.25.7"
	Authority  string // the authoritative source that mandates it, e.g. "go.mod: go 1.25.0"
}

// ConfigError is an inconsistency detected against an authoritative source for
// which NO canonical satisfying representation could be derived. The repository
// is invalid and a human must resolve it — the reconciler never guesses, never
// substitutes a different representation, never overshoots.
type ConfigError struct {
	Reconciler string
	File       string // the dependent file that cannot be reconciled
	Line       int
	Message    string
}

// Result is the outcome of a reconciliation pass: the derived mutations and the
// inconsistencies that could not be reconciled. A pass never partially decides —
// it reports everything it found so the caller can apply all derivable work and
// then fail on the config errors.
type Result struct {
	Mutations    []Mutation
	ConfigErrors []ConfigError
}

// Failed reports whether the pass found an unreconcilable inconsistency. The run
// must exit non-zero when it does — but only after every derivable reconciliation
// has been collected.
func (r Result) Failed() bool { return len(r.ConfigErrors) > 0 }
