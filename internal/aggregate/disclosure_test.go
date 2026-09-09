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
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/report"
)

func TestGroupDisclosureFromDifferThroughConsumers(t *testing.T) {
	for _, policy := range []string{"selector", "sensitive_before", "sensitive_after", "wrapper_and_selector"} {
		for _, protectedName := range []string{"a", "z"} {
			t.Run(policy+"/"+protectedName, func(t *testing.T) {
				var nodes []model.NodeDiff
				for _, name := range []string{"a", "z"} {
					target := resolve.Target{Certname: name}
					catalog := func(value string, before bool) model.NormalizedCatalog {
						var parameter any = value
						if policy == "wrapper_and_selector" {
							parameter = map[string]any{"nested": map[string]any{"__ptype": "Sensitive", "__pvalue": value}, "sibling": value}
						}
						r := model.Resource{Identity: model.ResourceIdentity{Type: "User", Title: "app"}, Parameters: map[string]any{"password": parameter}}
						if name == protectedName && ((policy == "sensitive_before" && before) || (policy == "sensitive_after" && !before)) {
							r.SensitiveParameters = []string{"password"}
						}
						return model.NormalizedCatalog{Certname: name, Resources: []model.Resource{r}}
					}
					if name == protectedName && (policy == "selector" || policy == "wrapper_and_selector") {
						target.Redact = []config.RedactionSelector{{Type: "User", Parameter: "password"}}
					}
					nd, diagnostics := diff.Diff(context.Background(), target, catalog("old-secret", true), catalog("new-secret", false), nil)
					if len(diagnostics) != 0 || len(nd.ResourceChanges) != 1 {
						t.Fatalf("comparison failed: %+v, %+v", nd, diagnostics)
					}
					nodes = append(nodes, nd)
				}
				var first string
				for _, reverse := range []bool{false, true} {
					if reverse {
						nodes[0], nodes[1] = nodes[1], nodes[0]
					}
					agg := aggregate.Build(nodes)
					if len(agg.Groups) != 1 {
						t.Fatalf("equivalent changes did not group: %+v", agg)
					}
					g := agg.Groups[0]
					if g.Before != model.RedactedValue || g.After != model.RedactedValue {
						t.Fatal("group exposed protected values")
					}
					if len(g.NodeChangeRefs) != 2 || g.NodeChangeRefs[0] != (model.NodeChangeRef{Certname: "a", Index: 0}) || g.NodeChangeRefs[1] != (model.NodeChangeRef{Certname: "z", Index: 0}) {
						t.Fatalf("incorrect member references: %+v", g.NodeChangeRefs)
					}
					encoded, _ := json.Marshal(agg)
					if reverse && string(encoded) != first {
						t.Fatal("target ordering changed group publication")
					}
					first = string(encoded)

					// Isolate aggregate publication: per-target evidence retains its
					// own disclosure policy and can legitimately contain these values.
					r := model.Result{SchemaVersion: model.ResultSchemaVersion, Aggregate: agg}
					for label, render := range map[string]func() ([]byte, error){
						"json": func() ([]byte, error) { return report.JSON(r) },
						"text": func() ([]byte, error) { return report.Text(r, nil, report.Options{}) },
						"html": func() ([]byte, error) { return report.HTML(r, nil) },
						"inference": func() ([]byte, error) {
							req, _, err := assess.BuildRequest(r, assess.ChangeContext{}, assess.Config{})
							if err != nil {
								return nil, err
							}
							return json.Marshal(req)
						},
					} {
						output, err := render()
						if err != nil {
							t.Fatal(err)
						}
						if strings.Contains(string(output), "old-secret") || strings.Contains(string(output), "new-secret") || strings.Contains(string(output), nodes[0].ResourceChanges[0].Fingerprint) {
							t.Fatalf("%s exposed protected group evidence", label)
						}
					}
				}
			})
		}
	}
}
