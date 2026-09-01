package capture

import (
	"context"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
)

// CompilerCatalogRequester is the interface CaptureCatalog depends on to
// request a candidate catalog from the configured compiler:
// `Compiler.RequestCandidate(target, facts) -> Catalog, Provenance,
// Warnings`.
//
// # Implementation
//
// internal/compiler.Adapter is the real v3/v4 HTTP implementation of
// this interface: request encoding, response validation, identity and
// environment verification, and v3/v4 trusted-fact handling.
// cmd/piace/main.go constructs it via compiler.NewAdapter and passes it
// as Workflow.Compiler for `capture catalog`, and `compare` builds the
// identical adapter for the same purpose, because capture catalog uses
// the exact same adapter and policy as comparison.
//
// StubCompiler (compiler_stub.go) remains in this package only as a
// lightweight test double for this package's own workflow tests
// (workflow_test.go); production wiring no longer uses it.
//
// RequestCandidate takes ctx, the resolved target (so an implementation
// can read target.Candidate.CatalogAPI, target.Candidate.Environment,
// etc.), and facts (the just-retrieved input factset: "capturing a
// catalog snapshot... SHALL use the target facts from the configured
// fact source"). It returns the raw candidate puppetdb.Catalog carrier,
// its model.CandidateProvenance, and any warnings (e.g. the v3
// trusted-fact compatibility warning is carried inside
// CandidateProvenance.V3Warning, so a separate warnings return is not
// needed for that case; warnings here is reserved for a future
// non-provenance warning class internal/compiler may need and is intentionally
// []string rather than unused so callers do not need a signature change
// to start using it).
type CompilerCatalogRequester interface {
	RequestCandidate(ctx context.Context, target resolve.Target, facts puppetdb.Factset) (puppetdb.Catalog, model.CandidateProvenance, []string, *model.Diagnostic)
}
