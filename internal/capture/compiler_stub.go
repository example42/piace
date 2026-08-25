package capture

import (
	"context"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
)

// StubCompiler is a CompilerCatalogRequester that always fails with a
// clear, per-target operational diagnostic rather than a working v3/v4
// compiler request. It exists as a lightweight test double for this
// package's own workflow tests (workflow_test.go), so
// `capture catalog`'s target-loop, envelope-construction, and per-target
// error-isolation logic can be exercised without a real compiler — see
// compiler.go's doc comment for the real implementation
// (internal/compiler.Adapter), which cmd/piace/main.go uses in
// production.
type StubCompiler struct{}

// RequestCandidate always returns a non-nil diagnostic with Operation
// model.OperationRequestCandidate and never a usable Catalog/
// CandidateProvenance, so a caller cannot mistake this stub for a
// succeeded compilation.
func (StubCompiler) RequestCandidate(ctx context.Context, target resolve.Target, facts puppetdb.Factset) (puppetdb.Catalog, model.CandidateProvenance, []string, *model.Diagnostic) {
	diag := model.Diagnostic{
		Severity:  model.SeverityError,
		Operation: model.OperationRequestCandidate,
		Certname:  target.Certname,
		Message:   "compiler catalog requester is not implemented yet (task 6); catalog capture cannot complete",
	}
	return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, &diag
}

var _ CompilerCatalogRequester = StubCompiler{}
