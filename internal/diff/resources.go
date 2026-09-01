package diff

import (
	"context"
	"reflect"
	"sort"

	"github.com/example42/piace/internal/filecontent"
	"github.com/example42/piace/internal/model"
)

// fileResourceType is the exact Puppet resource type this package
// synthesizes a single content-bearing parameter-changed entry for,
// instead of reporting content/source/checksum/checksum_value as
// independent parameter changes. See doc.go.
const fileResourceType = "File"

// contentBearingParameter is the stable parameter label a synthesized
// File-content ResourceChange reports, regardless of which of the four
// raw parameters below actually differed. It intentionally reuses
// filecontent's own "content" parameter name (see filecontent's doc.go)
// so a configured redaction selector of {Type: "File", Parameter:
// "content"} matches it directly.
const contentBearingParameter = "content"

// fileContentBearingParameters is the exact set of File parameter names
// internal/filecontent inspects (see evidence.go's contentParameter,
// sourceParameter, checksumParameter, checksumValueParameter). A
// resource's diff must treat these four names as one unit, never as
// four independent parameter-changed entries, so the content-disclosure
// boundary is always in place.
var fileContentBearingParameters = map[string]bool{
	"content":        true,
	"source":         true,
	"checksum":       true,
	"checksum_value": true,
}

// diffResources computes the resource-added/removed and
// parameter-changed portion of pass 1 (see doc.go). identity indexes
// are built once by the caller and passed in so diffEdges and
// exclusion evaluation can reuse them without re-deriving from the raw
// catalogs.
func diffResources(
	ctx context.Context,
	certname, candidateEnvironment string,
	beforeResources, afterResources map[model.ResourceIdentity]model.Resource,
	retriever filecontent.ContentRetriever,
) ([]model.ResourceChange, []model.Diagnostic) {
	var changes []model.ResourceChange
	var diagnostics []model.Diagnostic

	allIdentities := unionIdentities(beforeResources, afterResources)
	for _, identity := range allIdentities {
		beforeRes, inBefore := beforeResources[identity]
		afterRes, inAfter := afterResources[identity]

		switch {
		case inBefore && !inAfter:
			changes = append(changes, model.ResourceChange{
				Kind:     model.ChangeResourceRemoved,
				Identity: identity,
			})
		case !inBefore && inAfter:
			changes = append(changes, model.ResourceChange{
				Kind:     model.ChangeResourceAdded,
				Identity: identity,
			})
		default:
			paramChanges, diags := diffParameters(ctx, certname, candidateEnvironment, identity,
				beforeRes.Parameters, afterRes.Parameters, retriever)
			changes = append(changes, paramChanges...)
			diagnostics = append(diagnostics, diags...)
		}
	}

	return changes, diagnostics
}

// diffParameters compares one resource's before/after canonical
// parameter maps. For a File resource, the four content-bearing
// parameter names are collapsed into at most one synthesized
// FileContent-carrying entry (see doc.go); every other parameter is
// reported independently using reflect.DeepEqual over the model.Value
// domain, which is exactly comparable since the normalizer
// already produces canonical values (exact decimal model.Number strings,
// recursively canonical maps/slices).
func diffParameters(
	ctx context.Context,
	certname, candidateEnvironment string,
	identity model.ResourceIdentity,
	before, after map[string]model.Value,
	retriever filecontent.ContentRetriever,
) ([]model.ResourceChange, []model.Diagnostic) {
	var changes []model.ResourceChange
	var diagnostics []model.Diagnostic

	isFile := identity.Type == fileResourceType
	fileContentDiffers := false

	names := unionParameterNames(before, after)
	for _, name := range names {
		if isFile && fileContentBearingParameters[name] {
			if !reflect.DeepEqual(before[name], after[name]) {
				fileContentDiffers = true
			}
			continue
		}
		bv := before[name]
		av := after[name]
		// An absent parameter and one explicitly present with an undef value are
		// the same semantic state, and comparing the two zero-value model.Values
		// directly says so. PuppetDB's documented catalog wire format v8 is
		// explicit that "attributes with undef values are not added to the
		// catalog" (the same primary source internal/normalize/value.go cites
		// for its own absent-parameters handling), so absence *is* how a catalog
		// spells undef. Reporting the pair as a difference would be exactly the
		// generated noise the differ exists to suppress, and would emit an entry
		// whose Before and After are both nil: a change row with nothing in it
		// to render.
		if reflect.DeepEqual(bv, av) {
			continue
		}
		changes = append(changes, model.ResourceChange{
			Kind:      model.ChangeParameterChanged,
			Identity:  identity,
			Parameter: name,
			Before:    bv,
			After:     av,
		})
	}

	if isFile && fileContentDiffers {
		evidence, diag := filecontent.ResolveFileContentEvidence(ctx, certname, candidateEnvironment,
			identity, before, after, retriever)
		if diag != nil {
			diagnostics = append(diagnostics, *diag)
		}
		if evidence.State != model.FileContentUnchanged {
			change := model.ResourceChange{
				Kind:        model.ChangeParameterChanged,
				Identity:    identity,
				Parameter:   contentBearingParameter,
				FileContent: &evidence,
			}
			changes = append(changes, change)
		}
	}

	return changes, diagnostics
}

// unionIdentities returns every resource identity present in either map,
// sorted by (Type, Title) with no case folding, matching the normalized
// catalog model's identity ordering.
func unionIdentities(a, b map[model.ResourceIdentity]model.Resource) []model.ResourceIdentity {
	seen := make(map[model.ResourceIdentity]bool, len(a)+len(b))
	out := make([]model.ResourceIdentity, 0, len(a)+len(b))
	for id := range a {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for id := range b {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// unionParameterNames returns every parameter name present in either
// map, sorted lexicographically so a deterministic ResourceChange order
// results.
func unionParameterNames(a, b map[string]model.Value) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for name := range a {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for name := range b {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// indexResources builds a lookup map from a NormalizedCatalog's
// resource list. Resources is already deduplicated by identity, since
// the normalizer rejects a duplicate identity as a normalization error,
// so a plain map assignment is safe.
func indexResources(resources []model.Resource) map[model.ResourceIdentity]model.Resource {
	out := make(map[model.ResourceIdentity]model.Resource, len(resources))
	for _, r := range resources {
		out[r.Identity] = r
	}
	return out
}
