package report

import (
	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// sampleResult is one result document exercising every element the three
// renderers have to handle: a clean target, a failed target, a target
// with resource/parameter/edge changes, File-content evidence, a redacted
// value, an exclusion, the v3 warning, an aggregate group of each shape,
// and both a completed and a failed impact estimate.
func sampleResult() model.Result {
	r := model.NewResult("test", "2026-08-25T12:00:00Z")

	r.Targets = []model.TargetResult{
		{
			Certname: "web-01.example.test",
			Baseline: &model.SourceProvenance{
				Kind: model.SourceKindPuppetDB, Certname: "web-01.example.test",
				Environment: "production", CatalogIdentity: "sha256:baseline",
			},
			Facts:  &model.SourceProvenance{Kind: model.SourceKindPuppetDB, Certname: "web-01.example.test"},
			Config: &model.ConfigProvenance{FailOnDiff: true, Candidate: map[string]any{"catalog_api": "v3", "environment": "feature-123"}},
			Candidate: &model.CandidateProvenance{
				RequestedAPI: config.CatalogAPIv3, EffectiveAPI: config.CatalogAPIv3,
				Environment: "feature-123", FactSource: model.SourceKindPuppetDB,
				V3Warning: model.V3TrustedFactWarning,
			},
			NodeDiff: &model.NodeDiff{
				Certname:      "web-01.example.test",
				HasDifference: true,
				ResourceChanges: []model.ResourceChange{
					{Kind: model.ChangeResourceAdded, Identity: model.ResourceIdentity{Type: "Notify", Title: "</script><img src=x>"}},
					{
						Kind: model.ChangeParameterChanged, Identity: model.ResourceIdentity{Type: "Service", Title: "nginx"},
						Parameter: "ensure", Before: "stopped", After: "running",
					},
					{
						Kind: model.ChangeParameterChanged, Identity: model.ResourceIdentity{Type: "Service", Title: "nginx"},
						Parameter: "password", Before: model.RedactedValue, After: model.RedactedValue,
					},
					{
						Kind: model.ChangeParameterChanged, Identity: model.ResourceIdentity{Type: "File", Title: "/etc/motd"},
						Parameter: "content",
						FileContent: &model.FileContentEvidence{
							State: model.FileContentChanged, EvidenceSource: model.FileContentEvidenceInline,
							Algorithm: "sha256", BeforeDigest: "aaaa", AfterDigest: "bbbb",
						},
					},
				},
				EdgeChanges: []model.EdgeChange{
					{Kind: model.ChangeEdgeAdded, Edge: model.Edge{Source: "Class[a]", Target: "Class[b]"}},
				},
				Exclusions: []model.ExclusionOutcome{
					{Rule: model.ExclusionRuleRef{Type: "Package", Title: "*"}, SuppressedResources: 2, SuppressedEdges: 1},
				},
			},
		},
		{
			Certname: "web-02.example.test",
			Config:   &model.ConfigProvenance{},
			Diagnostics: []model.Diagnostic{{
				Severity: model.SeverityError, Operation: model.OperationLoadBaseline,
				Certname: "web-02.example.test", Message: "no baseline catalog stored for this certname",
			}},
		},
	}

	r.Aggregate = model.AggregateDiff{Groups: []model.AggregateGroup{
		{
			Key:            model.AggregateChangeKey{Kind: model.ChangeParameterChanged, Identity: &model.ResourceIdentity{Type: "Service", Title: "nginx"}, Parameter: "ensure"},
			Before:         "stopped",
			After:          "running",
			Certnames:      []string{"web-01.example.test"},
			NodeChangeRefs: []model.NodeChangeRef{{Certname: "web-01.example.test", Index: 1}},
		},
		{
			Key:            model.AggregateChangeKey{Kind: model.ChangeEdgeAdded, Edge: &model.Edge{Source: "Class[a]", Target: "Class[b]"}},
			Certnames:      []string{"web-01.example.test"},
			NodeChangeRefs: []model.NodeChangeRef{{Certname: "web-01.example.test", Index: 0}},
		},
	}}

	r.ImpactEstimates = []model.ImpactEstimate{
		{
			Identity:    model.ResourceIdentity{Type: "Service", Title: "nginx"},
			PQL:         `resources[certname] { type = "Service" and title = "nginx" }`,
			Request:     model.ImpactRequest{Path: "/pdb/query/v4", Limit: 3, OrderBy: `[{"field":"certname","order":"asc"}]`},
			ResultLimit: 2, Timeout: "10s", Status: model.ImpactStatusCompleted,
			Certnames: []string{"db-01.example.test", "db-02.example.test"}, ResultCount: 3, Truncated: true,
		},
		{
			Identity:    model.ResourceIdentity{Type: "File", Title: "/etc/motd"},
			PQL:         `resources[certname] { type = "File" and title = "/etc/motd" }`,
			Request:     model.ImpactRequest{Path: "/pdb/query/v4", Limit: 3},
			ResultLimit: 2, Timeout: "10s", Status: model.ImpactStatusFailed,
			FailureReason: "puppetdb returned status 503",
		},
	}

	r.Diagnostics = []model.Diagnostic{{
		Severity: model.SeverityError, Operation: model.OperationEstimateImpact,
		Message: "estimating impact for File[/etc/motd]: puppetdb returned status 503",
	}}

	r.Reduce()
	return r
}

// wantOutcome is sampleResult's expected reduced outcome, asserted by
// several tests so a change to the fixture cannot silently weaken them.
const wantOutcome = exitcode.OutcomeOperationalError
