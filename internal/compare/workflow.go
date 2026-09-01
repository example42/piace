package compare

import (
	"context"
	"sort"
	"time"

	"github.com/example42/piace/internal/aggregate"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/diff"
	"github.com/example42/piace/internal/filecontent"
	"github.com/example42/piace/internal/impact"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/normalize"
	"github.com/example42/piace/internal/puppetdb"
)

// CatalogRequester is the compiler-adapter contract this package needs:
// `Compiler.RequestCandidate(target, facts) -> Catalog, Provenance,
// Warnings`. *compiler.Adapter implements it.
//
// It is declared here rather than imported from internal/capture so the
// two commands do not couple to each other. Both name the same method
// set and both are satisfied by the same single adapter, which is what
// capture catalog using the exact same adapter and policy as comparison
// means in practice.
type CatalogRequester interface {
	RequestCandidate(ctx context.Context, target resolve.Target, facts puppetdb.Factset) (puppetdb.Catalog, model.CandidateProvenance, []string, *model.Diagnostic)
}

// Workflow implements `piace compare`. Every collaborator is injected so
// the whole pipeline is exercisable without a network, which is what the
// fixture matrix needs.
type Workflow struct {
	// PuppetDBFacts and FileFacts are the two FactSource implementations;
	// puppetdb.SelectFactSource picks between them per target.
	PuppetDBFacts puppetdb.FactSource
	FileFacts     puppetdb.FactSource
	// PuppetDBBaseline and FileBaseline are the two CatalogSource
	// implementations; puppetdb.SelectCatalogSource picks between them.
	PuppetDBBaseline puppetdb.CatalogSource
	FileBaseline     puppetdb.CatalogSource
	// Compiler requests each target's candidate catalog.
	Compiler CatalogRequester
	// ContentRetriever backs internal/filecontent's step-3 retrieval. It
	// may be nil; internal/filecontent then reports
	// content_indeterminate with a diagnostic rather than claiming a
	// verified comparison.
	ContentRetriever filecontent.ContentRetriever
	// ImpactQuerier issues the bounded PQL estimates. See doc.go for why
	// a nil querier is reported rather than skipped.
	ImpactQuerier impact.ImpactQuerier
	// ToolVersion is recorded in the result's invocation metadata.
	ToolVersion string
	// Now supplies the invocation timestamp; nil uses time.Now.
	Now func() time.Time
}

// Run compares every target in cfg and returns the complete, reduced
// shared result document. It never returns an error: every failure it can
// encounter is a target-local or run-level diagnostic already present in
// the returned Result, and the returned Result.ExitCode is the process
// exit status the caller should use.
func (w *Workflow) Run(ctx context.Context, cfg resolve.Config) model.Result {
	result := model.NewResult(w.ToolVersion, w.now())
	result.Invocation.Services = serviceProvenance(cfg.Services)

	result.Targets = make([]model.TargetResult, 0, len(cfg.Targets))
	nodeDiffs := make([]model.NodeDiff, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		tr := w.compareTarget(ctx, target)
		if tr.NodeDiff != nil {
			nodeDiffs = append(nodeDiffs, *tr.NodeDiff)
		}
		result.Targets = append(result.Targets, tr)
	}

	// The document is target-sorted while the work above ran in target-file
	// order, which impact.EstimateAll's documented "first target in
	// target-file order" tie-break for an identity exhibited by several
	// targets depends on. Sorting the emitted results rather than the input
	// preserves both.
	sort.SliceStable(result.Targets, func(i, j int) bool {
		return result.Targets[i].Certname < result.Targets[j].Certname
	})

	result.Aggregate = aggregate.Build(nodeDiffs)

	estimates, diagnostics := w.estimateImpact(ctx, cfg.Targets, nodeDiffs)
	result.ImpactEstimates = estimates
	result.Diagnostics = append(result.Diagnostics, diagnostics...)

	result.Reduce()
	return result
}

// compareTarget runs one target's pipeline: load facts, load the baseline
// catalog, request the candidate catalog, normalize both, and diff. It
// stops at the first failure (see doc.go) and always returns a
// TargetResult carrying whatever provenance was established before that
// point, so a failed target still shows which source it was reading.
//
// Outcome is left unset here; model.Result.Reduce classifies it from the
// diagnostics and node diff this function collects, so there is exactly
// one implementation of the taxonomy.
func (w *Workflow) compareTarget(ctx context.Context, target resolve.Target) model.TargetResult {
	tr := model.TargetResult{
		Certname: target.Certname,
		Config:   resolve.Provenance(target),
	}

	facts, factsProvenance, diag := puppetdb.SelectFactSource(target, w.PuppetDBFacts, w.FileFacts).
		Load(ctx, target)
	if diag != nil {
		tr.Diagnostics = append(tr.Diagnostics, *diag)
		return tr
	}
	tr.Facts = &factsProvenance

	baselineCatalog, baselineProvenance, diag := puppetdb.SelectCatalogSource(target, w.PuppetDBBaseline, w.FileBaseline).
		LoadBaseline(ctx, target)
	if diag != nil {
		tr.Diagnostics = append(tr.Diagnostics, *diag)
		return tr
	}
	tr.Baseline = &baselineProvenance

	candidateCatalog, candidateProvenance, warnings, diag := w.Compiler.RequestCandidate(ctx, target, facts)
	if diag != nil {
		tr.Diagnostics = append(tr.Diagnostics, *diag)
		return tr
	}
	tr.Candidate = &candidateProvenance
	// Compiler warnings, a permitted v4-to-v3 fallback notice, become
	// warning-severity diagnostics so there is one channel a renderer and
	// the reducer both read. The non-suppressible v3 trusted-fact warning
	// itself stays where internal/compiler put it, on
	// CandidateProvenance.V3Warning, and every renderer surfaces it from
	// there: it is owed in every output format, which is a stronger
	// obligation than being one diagnostic among many.
	for _, warning := range warnings {
		tr.Diagnostics = append(tr.Diagnostics, model.Diagnostic{
			Severity:  model.SeverityWarning,
			Operation: model.OperationRequestCandidate,
			Certname:  target.Certname,
			Message:   warning,
		})
	}

	baseline, diag := normalize.Catalog(baselineCatalog)
	if diag != nil {
		tr.Diagnostics = append(tr.Diagnostics, *diag)
		return tr
	}
	candidate, diag := normalize.Catalog(candidateCatalog)
	if diag != nil {
		tr.Diagnostics = append(tr.Diagnostics, *diag)
		return tr
	}

	nodeDiff, diagnostics := diff.Diff(ctx, target, baseline, candidate, w.ContentRetriever)
	tr.NodeDiff = &nodeDiff
	tr.Diagnostics = append(tr.Diagnostics, diagnostics...)
	return tr
}

// estimateImpact runs the run-wide impact estimation stage, or reports
// why it could not. It is a no-op, no request and no diagnostic, when no
// target enables estimation: a disabled estimate produces no request and
// no failure.
func (w *Workflow) estimateImpact(ctx context.Context, targets []resolve.Target, nodeDiffs []model.NodeDiff) ([]model.ImpactEstimate, []model.Diagnostic) {
	if !anyEstimateEnabled(targets) {
		return nil, nil
	}
	if w.ImpactQuerier == nil {
		return nil, []model.Diagnostic{{
			Severity:  model.SeverityError,
			Operation: model.OperationEstimateImpact,
			Message:   "impact estimation is enabled for at least one target but no PuppetDB impact querier is configured",
		}}
	}
	return impact.EstimateAll(ctx, w.ImpactQuerier, targets, nodeDiffs)
}

func anyEstimateEnabled(targets []resolve.Target) bool {
	for _, t := range targets {
		if t.ImpactEstimate.Enabled {
			return true
		}
	}
	return false
}

// serviceProvenance records the two authorities this run was configured
// to reach. Only URL.Host is read, never a CA bundle, client
// certificate, or private key path, so the run-level provenance cannot
// become a disclosure channel for the material provenance excludes. A
// zero Services, from a test-constructed Config, yields nil rather than
// a pair of empty strings.
func serviceProvenance(services resolve.Services) *model.ServiceProvenance {
	compiler, puppetDB := "", ""
	if services.Compiler.URL != nil {
		compiler = services.Compiler.URL.Host
	}
	if services.PuppetDB.URL != nil {
		puppetDB = services.PuppetDB.URL.Host
	}
	if compiler == "" && puppetDB == "" {
		return nil
	}
	return &model.ServiceProvenance{Compiler: compiler, PuppetDB: puppetDB}
}

// now returns the invocation timestamp as RFC 3339 in UTC.
func (w *Workflow) now() string {
	clock := w.Now
	if clock == nil {
		clock = time.Now
	}
	return clock().UTC().Format(time.RFC3339)
}
