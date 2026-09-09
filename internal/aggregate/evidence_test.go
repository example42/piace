package aggregate_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/example42/piace/internal/aggregate"
	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/diff"
	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/report"
)

func TestMembershipEvidenceGroupingAndDisclosure(t *testing.T) {
	for _, removed := range []bool{false, true} {
		for _, resourceType := range []string{"User", "File"} {
			t.Run(resourceType+map[bool]string{false: "/added", true: "/removed"}[removed], func(t *testing.T) {
				var nodes []model.NodeDiff
				for _, spec := range []struct{ name, owner string }{{"a", "root"}, {"b", "app"}, {"z", "root"}} {
					resource := model.Resource{
						Identity: model.ResourceIdentity{Type: resourceType, Title: "app"},
						Parameters: map[string]any{
							"owner": spec.owner, "password": "metadata-secret", "token": "selector-secret",
							"settings": []any{map[string]any{"__ptype": "Sensitive", "__pvalue": "nested-secret"}, "visible"},
						},
						SensitiveParameters: []string{"password"},
					}
					if resourceType == "File" {
						resource.Parameters["content"] = "managed-file-bytes"
						resource.Parameters["source"] = "puppet:///modules/private/source"
						resource.SensitiveParameters = append(resource.SensitiveParameters, "content")
					}
					before := model.NormalizedCatalog{Certname: spec.name}
					after := model.NormalizedCatalog{Certname: spec.name, Resources: []model.Resource{resource}}
					if removed {
						before, after = after, before
					}
					nd, ds := diff.Diff(context.Background(), resolve.Target{Certname: spec.name, Redact: []config.RedactionSelector{{Type: resourceType, Parameter: "token"}}}, before, after, nil)
					if len(ds) != 0 || len(nd.ResourceChanges) != 1 {
						t.Fatalf("diff failed: %+v %+v", nd, ds)
					}
					change := nd.ResourceChanges[0]
					projection := change.After
					if removed {
						projection = change.Before
					}
					if projection.(map[string]any)["owner"] != spec.owner {
						t.Fatal("managed settings lost")
					}
					if resourceType == "File" {
						fc := change.FileContent
						if fc == nil || !fc.Redacted || (removed && (fc.Before == nil || fc.After != nil || fc.AfterDigest != "")) || (!removed && (fc.After == nil || fc.Before != nil || fc.BeforeDigest != "")) {
							t.Fatalf("invalid one-sided File evidence: %+v", fc)
						}
					}
					nodes = append(nodes, nd)
				}
				r := model.NewResult("test", "2026-09-09T00:00:00Z")
				r.Aggregate = aggregate.Build(nodes)
				if len(r.Aggregate.Groups) != 2 {
					t.Fatalf("different settings merged: %+v", r.Aggregate)
				}
				var paired bool
				for _, g := range r.Aggregate.Groups {
					if len(g.Certnames) == 2 {
						paired = g.Certnames[0] == "a" && g.Certnames[1] == "z" && len(g.NodeChangeRefs) == 2
					}
				}
				if !paired {
					t.Fatal("equivalent settings lost their target references")
				}
				for i := range nodes {
					// A stored document is validated when it is read back, so
					// these fixtures carry the outcomes a real run records
					// rather than leaving them zero.
					r.Targets = append(r.Targets, model.TargetResult{
						Certname: nodes[i].Certname,
						Outcome:  exitcode.OutcomeDifferencesAllowed,
						NodeDiff: &nodes[i],
					})
				}
				r.Finalize()
				encoded, err := report.JSON(r)
				if err != nil {
					t.Fatal(err)
				}
				stored, err := report.DecodeJSON(encoded)
				if err != nil {
					t.Fatal(err)
				}
				req, _, err := assess.BuildRequest(stored, assess.ChangeContext{}, assess.Config{})
				if err != nil {
					t.Fatal(err)
				}
				request, _ := json.Marshal(req)
				text, err := report.Text(r, nil, report.Options{})
				if err != nil {
					t.Fatal(err)
				}
				html, err := report.HTML(r, nil)
				if err != nil {
					t.Fatal(err)
				}
				for label, output := range map[string][]byte{"json": encoded, "text": text, "html": html, "inference": request} {
					for _, secret := range []string{"metadata-secret", "selector-secret", "nested-secret", "managed-file-bytes", "puppet:///modules/private/source"} {
						if strings.Contains(string(output), secret) {
							t.Fatalf("%s exposed %s", label, secret)
						}
					}
					if !strings.Contains(string(output), "owner") || !strings.Contains(string(output), "visible") {
						t.Fatalf("%s lost safe evidence", label)
					}
				}
			})
		}
	}
}

func TestFileContentSummarySurvivesStoredAssessment(t *testing.T) {
	for _, state := range []model.FileContentState{model.FileContentChanged, model.FileContentReferenceChanged, model.FileContentIndeterminate} {
		t.Run(string(state), func(t *testing.T) {
			evidence := &model.FileContentEvidence{
				State: state, EvidenceSource: model.FileContentEvidenceMixed,
				BeforeDigest: strings.Repeat("a", 64), AfterDigest: strings.Repeat("b", 64), Algorithm: "sha256",
				Before:           &model.FileSideEvidence{Source: model.FileContentEvidenceCaptured, Verified: true, Context: model.ContentContext{CatalogIdentity: "private-catalog-identity"}},
				After:            &model.FileSideEvidence{Source: model.FileContentEvidenceCompilerRetrieval, Verified: state == model.FileContentChanged},
				ReferenceChanged: state == model.FileContentReferenceChanged,
			}
			change := model.ResourceChange{Kind: model.ChangeParameterChanged, Identity: model.ResourceIdentity{Type: "File", Title: "/app"}, Parameter: "content", FileContent: evidence, Fingerprint: "private-fingerprint"}
			node := model.NodeDiff{Certname: "a", ResourceChanges: []model.ResourceChange{change}, HasDifference: true}
			r := model.NewResult("test", "2026-09-09T00:00:00Z")
			r.Aggregate = aggregate.Build([]model.NodeDiff{node})
			r.Targets = []model.TargetResult{{Certname: "a", Outcome: exitcode.OutcomeDifferencesAllowed, NodeDiff: &node}}
			r.Finalize()
			encoded, err := report.JSON(r)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := report.DecodeJSON(encoded)
			if err != nil {
				t.Fatal(err)
			}
			req, _, err := assess.BuildRequest(stored, assess.ChangeContext{}, assess.Config{})
			if err != nil {
				t.Fatal(err)
			}
			body := req.Messages[1].Content
			for _, forbidden := range []string{evidence.BeforeDigest, evidence.AfterDigest, "private-catalog-identity", "private-fingerprint", "sha256"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("inference exposed %s", forbidden)
				}
			}
			for _, expected := range []string{string(state), "captured_digest", "compiler_retrieval", "verified"} {
				if !strings.Contains(body, expected) {
					t.Fatalf("inference lost %s", expected)
				}
			}
			// Inspect group rendering without per-target output masking an omission.
			r.Targets = nil
			text, err := report.Text(r, nil, report.Options{})
			if err != nil {
				t.Fatal(err)
			}
			html, err := report.HTML(r, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, output := range [][]byte{text, html} {
				if !strings.Contains(string(output), string(state)) || !strings.Contains(string(output), "captured_digest") {
					t.Fatal("aggregate rendering lost content evidence")
				}
			}
		})
	}
}

func TestDistinctSensitiveMembershipEvidenceNeverMerges(t *testing.T) {
	for _, removed := range []bool{false, true} {
		for _, typ := range []string{"User", "File"} {
			var nodes []model.NodeDiff
			for _, secret := range []string{"first-secret", "second-secret"} {
				param := "password"
				if typ == "File" {
					param = "content"
				}
				resource := model.Resource{Identity: model.ResourceIdentity{Type: typ, Title: "app"}, Parameters: map[string]any{param: secret}, SensitiveParameters: []string{param}}
				before, after := model.NormalizedCatalog{}, model.NormalizedCatalog{Resources: []model.Resource{resource}}
				if removed {
					before, after = after, before
				}
				nd, ds := diff.Diff(context.Background(), resolve.Target{Certname: secret[:1]}, before, after, nil)
				if len(ds) != 0 {
					t.Fatal(ds)
				}
				nodes = append(nodes, nd)
			}
			if len(aggregate.Build(nodes).Groups) != 2 {
				t.Fatalf("distinct %s membership evidence merged", typ)
			}
		}
	}
}
