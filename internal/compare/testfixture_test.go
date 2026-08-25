package compare

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/impact"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
)

// fixedClock is the invocation timestamp every test in this package uses,
// so a result document is fully determined by its inputs.
func fixedClock() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }

// fakeFactSource returns one canned factset (or one canned diagnostic)
// for every target.
type fakeFactSource struct {
	factset puppetdb.Factset
	diag    *model.Diagnostic
}

func (f fakeFactSource) Load(_ context.Context, target resolve.Target) (puppetdb.Factset, model.SourceProvenance, *model.Diagnostic) {
	if f.diag != nil {
		return puppetdb.Factset{}, model.SourceProvenance{}, f.diag
	}
	factset := f.factset
	factset.Certname = target.Certname
	return factset, model.SourceProvenance{
		Kind:     model.SourceKindPuppetDB,
		Certname: target.Certname,
		Producer: factset.Producer,
	}, nil
}

// fakeCatalogSource returns a canned baseline catalog (or diagnostic) for
// every target, built from a resource list so tests describe catalogs in
// terms of what they contain.
type fakeCatalogSource struct {
	resources []testResource
	edges     []testEdge
	diag      *model.Diagnostic
}

func (f fakeCatalogSource) LoadBaseline(_ context.Context, target resolve.Target) (puppetdb.Catalog, model.SourceProvenance, *model.Diagnostic) {
	if f.diag != nil {
		return puppetdb.Catalog{}, model.SourceProvenance{}, f.diag
	}
	catalog := buildCatalog(target.Certname, target.Baseline.Environment, f.resources, f.edges)
	return catalog, model.SourceProvenance{
		Kind:            model.SourceKindPuppetDB,
		Certname:        target.Certname,
		Environment:     target.Baseline.Environment,
		CatalogIdentity: "sha256:baseline",
	}, nil
}

// fakeCompiler returns a canned candidate catalog (or diagnostic).
type fakeCompiler struct {
	resources []testResource
	edges     []testEdge
	warnings  []string
	v3Warning string
	diag      *model.Diagnostic
}

func (f fakeCompiler) RequestCandidate(_ context.Context, target resolve.Target, _ puppetdb.Factset) (puppetdb.Catalog, model.CandidateProvenance, []string, *model.Diagnostic) {
	if f.diag != nil {
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, f.diag
	}
	catalog := buildCatalog(target.Certname, target.Candidate.Environment, f.resources, f.edges)
	provenance := model.CandidateProvenance{
		RequestedAPI: target.Candidate.CatalogAPI,
		EffectiveAPI: target.Candidate.CatalogAPI,
		Environment:  target.Candidate.Environment,
		FactSource:   model.SourceKindPuppetDB,
		V3Warning:    f.v3Warning,
	}
	return catalog, provenance, f.warnings, nil
}

// fakeQuerier records the identities it was asked about and replies with
// a canned estimate.
type fakeQuerier struct {
	asked  *[]model.ResourceIdentity
	status model.ImpactEstimateStatus
}

func (f fakeQuerier) Estimate(_ context.Context, identity model.ResourceIdentity, limits impact.Limits) (model.ImpactEstimate, *model.Diagnostic) {
	if f.asked != nil {
		*f.asked = append(*f.asked, identity)
	}
	estimate := model.ImpactEstimate{
		Identity:    identity,
		PQL:         `resources[certname] { type = "x" and title = "y" }`,
		Request:     model.ImpactRequest{Path: "/pdb/query/v4", Limit: limits.ResultLimit + 1},
		ResultLimit: limits.ResultLimit,
		Timeout:     limits.Timeout.String(),
		Status:      f.status,
	}
	if f.status != model.ImpactStatusCompleted {
		estimate.FailureReason = "query failed"
		return estimate, &model.Diagnostic{
			Severity:  model.SeverityError,
			Operation: model.OperationEstimateImpact,
			Message:   "estimating impact for " + identity.String() + ": query failed",
		}
	}
	estimate.Certnames = []string{"other-01.example.test"}
	estimate.ResultCount = 1
	return estimate, nil
}

// testResource and testEdge describe a catalog in test terms; buildCatalog
// renders them into the PuppetDB wire shape internal/normalize accepts.
type testResource struct {
	Type       string
	Title      string
	Parameters map[string]any
}

type testEdge struct {
	SourceType, SourceTitle string
	TargetType, TargetTitle string
}

func buildCatalog(certname, environment string, resources []testResource, edges []testEdge) puppetdb.Catalog {
	wireResources := make([]map[string]any, 0, len(resources))
	for _, r := range resources {
		parameters := r.Parameters
		if parameters == nil {
			parameters = map[string]any{}
		}
		wireResources = append(wireResources, map[string]any{
			"type": r.Type, "title": r.Title, "parameters": parameters,
		})
	}
	wireEdges := make([]map[string]any, 0, len(edges))
	for _, e := range edges {
		wireEdges = append(wireEdges, map[string]any{
			"source_type": e.SourceType, "source_title": e.SourceTitle,
			"target_type": e.TargetType, "target_title": e.TargetTitle,
		})
	}
	return puppetdb.Catalog{
		Certname:    certname,
		Environment: environment,
		Resources:   mustMarshal(wireResources),
		Edges:       mustMarshal(wireEdges),
	}
}

func mustMarshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// testTarget builds a fully resolved target the way
// internal/config/resolve would, so these tests exercise the same shape
// production does.
func testTarget(certname string, mutate ...func(*resolve.Target)) resolve.Target {
	target := resolve.Target{
		Certname: certname,
		Candidate: resolve.Candidate{
			Environment: "feature-123",
			CatalogAPI:  config.CatalogAPIv4,
		},
		Facts:    resolve.Facts{Source: config.FactSourcePuppetDB},
		Baseline: resolve.Baseline{Source: config.BaselineSourcePuppetDB, Environment: "production"},
		ImpactEstimate: resolve.ImpactEstimate{
			Timeout:     10 * time.Second,
			ResultLimit: 100,
		},
	}
	for _, m := range mutate {
		m(&target)
	}
	return target
}

// findTarget returns the result for certname, failing the test when the
// document has none.
func findTarget(t *testing.T, result model.Result, certname string) model.TargetResult {
	t.Helper()
	for _, tr := range result.Targets {
		if tr.Certname == certname {
			return tr
		}
	}
	t.Fatalf("no target result for %q", certname)
	return model.TargetResult{}
}
