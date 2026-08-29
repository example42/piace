package assess

import (
	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// Values the fixture plants in fields BuildRequest is supposed to drop
// on the floor. Each names a real field of model.Result that a naive
// "marshal the report and send it" would have forwarded, so the
// disclosure test asserts something the boundary actually decides rather
// than something the type system already prevents.
//
// There is deliberately no TLS-path poison here: model.Result has no
// field that can hold one. ServiceProvenance is documented as authority
// only, and ImpactRequest.Path is a PuppetDB query API path. That
// guarantee is structural, and a poison string for it would be a
// permanently vacuous assertion.
const (
	secretCompiler = "compiler.internal.bank.example:8140"
	secretPuppetDB = "puppetdb.internal.bank.example:8081"
	realCertname   = "web-01.pci-prod.bank.example"
	otherCertname  = "web-02.pci-prod.bank.example"
	impactCertname = "db-77.pci-prod.bank.example"

	// secretDigest is managed-File content evidence. A digest is not the
	// bytes, but it is a fingerprint of them, and no request carries one.
	secretDigest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	// secretPQL and secretQueryPath are an impact estimate's query
	// provenance. The PQL embeds a real certname that pseudonymization
	// would never reach, because it is inside a free-text query string.
	secretPQL       = `resources[certname] { type = "Service" and title = "nginx" and certname = "db-77.pci-prod.bank.example" }`
	secretQueryPath = "/pdb/query/v4"
	// secretCatalogID is a baseline catalog's identity from PuppetDB.
	secretCatalogID = "c0ffee00-1111-4222-8333-444455556666"
)

// assessableResult is a result document with everything the request
// builder has to decide about: two targets, groups of differing reach, a
// redacted value, managed-File content evidence, source provenance, an
// impact estimate naming a node outside the target set and carrying its
// query provenance, and service provenance. Everything but the groups and
// the impact counts must be dropped.
func assessableResult() model.Result {
	r := model.NewResult("test", "2026-08-25T12:00:00Z")
	r.Invocation.Services = &model.ServiceProvenance{Compiler: secretCompiler, PuppetDB: secretPuppetDB}

	r.Targets = []model.TargetResult{
		{
			Certname: realCertname,
			Outcome:  exitcode.OutcomePolicyDisallowedDifference,
			Baseline: &model.SourceProvenance{
				Kind:            model.SourceKindPuppetDB,
				Certname:        realCertname,
				Environment:     "production",
				CatalogIdentity: secretCatalogID,
			},
			NodeDiff: &model.NodeDiff{
				Certname:      realCertname,
				HasDifference: true,
				ResourceChanges: []model.ResourceChange{
					{Kind: model.ChangeParameterChanged, Identity: model.ResourceIdentity{Type: "Service", Title: "nginx"}, Parameter: "ensure", Before: "stopped", After: "running"},
					{
						Kind: model.ChangeParameterChanged, Identity: model.ResourceIdentity{Type: "File", Title: "/etc/shadow"},
						Parameter: "content", Before: model.RedactedValue, After: model.RedactedValue,
						FileContent: &model.FileContentEvidence{
							State:          model.FileContentChanged,
							EvidenceSource: model.FileContentEvidenceCompiledChecksum,
							Algorithm:      "sha256",
							BeforeDigest:   secretDigest,
							AfterDigest:    secretDigest,
						},
					},
				},
				EdgeChanges: []model.EdgeChange{{Kind: model.ChangeEdgeAdded, Edge: model.Edge{Source: "Class[a]", Target: "Class[b]"}}},
			},
		},
		{
			Certname: otherCertname,
			Outcome:  exitcode.OutcomePolicyDisallowedDifference,
			NodeDiff: &model.NodeDiff{
				Certname:      otherCertname,
				HasDifference: true,
				ResourceChanges: []model.ResourceChange{
					{Kind: model.ChangeParameterChanged, Identity: model.ResourceIdentity{Type: "Service", Title: "nginx"}, Parameter: "ensure", Before: "stopped", After: "running"},
				},
			},
		},
	}

	nginx := model.ResourceIdentity{Type: "Service", Title: "nginx"}
	shadow := model.ResourceIdentity{Type: "File", Title: "/etc/shadow"}
	r.Aggregate = model.AggregateDiff{Groups: []model.AggregateGroup{
		// One target only — must rank below the two-target group.
		{
			Key:       model.AggregateChangeKey{Kind: model.ChangeParameterChanged, Identity: &shadow, Parameter: "content"},
			Before:    model.RedactedValue,
			After:     model.RedactedValue,
			Certnames: []string{realCertname},
		},
		{
			Key:       model.AggregateChangeKey{Kind: model.ChangeEdgeAdded, Edge: &model.Edge{Source: "Class[a]", Target: "Class[b]"}},
			Certnames: []string{realCertname},
		},
		// Two targets — must rank first.
		{
			Key:       model.AggregateChangeKey{Kind: model.ChangeParameterChanged, Identity: &nginx, Parameter: "ensure"},
			Before:    "stopped",
			After:     "running",
			Certnames: []string{realCertname, otherCertname},
		},
	}}

	r.ImpactEstimates = []model.ImpactEstimate{{
		Identity:    nginx,
		PQL:         secretPQL,
		Request:     model.ImpactRequest{Path: secretQueryPath, Limit: 501, OrderBy: "certname"},
		ResultLimit: 500,
		Timeout:     "10s",
		Status:      model.ImpactStatusCompleted,
		Certnames:   []string{impactCertname},
		ResultCount: 1,
		Truncated:   false,
	}}

	r.Reduce()
	return r
}

func testConfig() Config {
	return Config{
		Model:            "test-model",
		MaxTokens:        4000,
		MaxGroups:        DefaultMaxGroups,
		Pseudonymize:     true,
		StructuredOutput: true,
	}
}
