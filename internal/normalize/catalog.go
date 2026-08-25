// Package normalize's Catalog function is the entry point task 7's brief
// describes: constructing a model.NormalizedCatalog from a raw
// puppetdb.Catalog carrier. See doc.go for the package-level contract.
package normalize

import (
	"fmt"
	"sort"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
)

// Catalog converts raw into a model.NormalizedCatalog: a resource list
// keyed by (and sorted on) the exact `Type[title]` identity, and an edge
// list keyed by (and sorted on) the ordered (source identity, target
// identity) pair, per design.md section 7.1. It returns a non-nil
// diagnostic (and a zero NormalizedCatalog) for any unrecognized/
// malformed resources or edges shape, a resource missing its required
// type/title, a duplicate resource identity, or an edge endpoint missing
// its required type/title — never a silently empty or partial catalog,
// per this task's brief and design.md's Components and Interfaces
// section ("Unknown or malformed catalog/fact data is an operational
// normalization failure, never an empty catalog or factset").
func Catalog(raw puppetdb.Catalog) (model.NormalizedCatalog, *model.Diagnostic) {
	resourceWires, err := extractResources(raw.Resources)
	if err != nil {
		diag := normalizeDiagnostic(raw.Certname, err.Error())
		return model.NormalizedCatalog{}, &diag
	}
	edgeWires, err := extractEdges(raw.Edges)
	if err != nil {
		diag := normalizeDiagnostic(raw.Certname, err.Error())
		return model.NormalizedCatalog{}, &diag
	}

	resources := make([]model.Resource, 0, len(resourceWires))
	seen := make(map[model.ResourceIdentity]bool, len(resourceWires))
	for i, rw := range resourceWires {
		if rw.Type == "" || rw.Title == "" {
			diag := normalizeDiagnostic(raw.Certname,
				fmt.Sprintf("resource at index %d is missing a required type or title", i))
			return model.NormalizedCatalog{}, &diag
		}
		identity := model.ResourceIdentity{Type: rw.Type, Title: rw.Title}
		if seen[identity] {
			diag := normalizeDiagnostic(raw.Certname,
				fmt.Sprintf("duplicate resource identity %s in catalog", identity.String()))
			return model.NormalizedCatalog{}, &diag
		}
		seen[identity] = true

		params, err := decodeParameters(rw.Parameters)
		if err != nil {
			diag := normalizeDiagnostic(raw.Certname,
				fmt.Sprintf("resource %s: %s", identity.String(), err.Error()))
			return model.NormalizedCatalog{}, &diag
		}

		resources = append(resources, model.Resource{
			Identity:   identity,
			Parameters: params,
		})
	}
	sort.Slice(resources, func(i, j int) bool {
		return resourceLess(resources[i].Identity, resources[j].Identity)
	})

	edges := make([]model.Edge, 0, len(edgeWires))
	for i, ew := range edgeWires {
		if ew.SourceType == "" || ew.SourceTitle == "" || ew.TargetType == "" || ew.TargetTitle == "" {
			diag := normalizeDiagnostic(raw.Certname,
				fmt.Sprintf("edge at index %d is missing a required source or target type/title", i))
			return model.NormalizedCatalog{}, &diag
		}
		source := model.ResourceIdentity{Type: ew.SourceType, Title: ew.SourceTitle}.String()
		target := model.ResourceIdentity{Type: ew.TargetType, Title: ew.TargetTitle}.String()
		edges = append(edges, model.Edge{Source: source, Target: target})
	}
	sort.Slice(edges, func(i, j int) bool {
		return edgeLess(edges[i], edges[j])
	})

	return model.NormalizedCatalog{
		Certname:    raw.Certname,
		Environment: raw.Environment,
		Resources:   resources,
		Edges:       edges,
	}, nil
}

// resourceLess orders two resource identities by Type then Title, both
// compared as plain Go strings (byte-wise), with no case folding, per
// design.md section 7.1: "type and title are strings with no case
// folding."
func resourceLess(a, b model.ResourceIdentity) bool {
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.Title < b.Title
}

// edgeLess orders two edges by the ordered pair (Source, Target), per
// design.md section 7.1: "A graph edge key is the ordered pair (source
// identity, target identity)."
func edgeLess(a, b model.Edge) bool {
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.Target < b.Target
}

// normalizeDiagnostic builds a model.Diagnostic classified as design.md
// section 10's "operational error" sub-category for a normalization
// failure (model.OperationNormalize). message is always a locally
// constructed, safe string built from field names/identities only —
// never raw parameter values — matching design.md's Error Handling
// section and this task's redaction-readiness requirement.
func normalizeDiagnostic(certname, message string) model.Diagnostic {
	return model.Diagnostic{
		Severity:  model.SeverityError,
		Operation: model.OperationNormalize,
		Certname:  certname,
		Message:   message,
	}
}
