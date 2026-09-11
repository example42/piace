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
// independent parameter changes. See doc.go. It is model's constant, so
// the differ and the reader that validates a stored report agree on
// which resource type carries a content-disclosure boundary.
const fileResourceType = model.FileResourceType

// contentBearingParameter is the stable parameter label a synthesized
// File-content ResourceChange reports, regardless of which of the four
// raw parameters below actually differed. It intentionally reuses
// filecontent's own "content" parameter name (see filecontent's doc.go)
// so a configured redaction selector of {Type: "File", Parameter:
// "content"} matches it directly.
const contentBearingParameter = "content"

// The set of File parameter names internal/filecontent inspects (see
// evidence.go's contentParameter, sourceParameter, checksumParameter,
// checksumValueParameter) lives in model as
// FileContentBearingParameter. A resource's diff must treat those names
// as one unit, never as independent parameter-changed entries, so the
// content-disclosure boundary is always in place; the reader that
// validates a stored report checks the same set.

// diffResources computes the resource-added/removed and
// parameter-changed portion of pass 1 (see doc.go). identity indexes
// are built once by the caller and passed in so diffEdges and
// exclusion evaluation can reuse them without re-deriving from the raw
// catalogs.
func diffResources(
	ctx context.Context,
	certname string,
	beforeContext, afterContext model.ContentContext,
	beforeResources, afterResources map[model.ResourceIdentity]model.Resource,
	retriever filecontent.ContentRetriever,
	f fidelity,
) ([]rawResourceChange, []model.Diagnostic) {
	var changes []rawResourceChange
	var diagnostics []model.Diagnostic

	allIdentities := unionIdentities(beforeResources, afterResources)
	for _, identity := range allIdentities {
		beforeRes, inBefore := beforeResources[identity]
		afterRes, inAfter := afterResources[identity]

		switch {
		case inBefore && !inAfter:
			change, diag := membershipChange(ctx, certname, model.ChangeResourceRemoved, filecontent.Side{Resource: beforeRes, Context: beforeContext}, retriever)
			changes = append(changes, change)
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
			}
		case !inBefore && inAfter:
			change, diag := membershipChange(ctx, certname, model.ChangeResourceAdded, filecontent.Side{Resource: afterRes, Context: afterContext}, retriever)
			changes = append(changes, change)
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
			}
		default:
			paramChanges, diags := diffParameters(ctx, certname, identity,
				filecontent.Side{Resource: beforeRes, Context: beforeContext}, filecontent.Side{Resource: afterRes, Context: afterContext}, retriever, f)
			changes = append(changes, paramChanges...)
			diagnostics = append(diagnostics, diags...)
		}
	}

	return changes, diagnostics
}

func membershipChange(ctx context.Context, certname string, kind model.ChangeKind, side filecontent.Side, retriever filecontent.ContentRetriever) (rawResourceChange, *model.Diagnostic) {
	change := rawResourceChange{Kind: kind, Identity: side.Resource.Identity}
	if kind == model.ChangeResourceAdded {
		change.After = side.Resource.Parameters
	} else {
		change.Before = side.Resource.Parameters
	}
	if !filecontent.NeedsEvidence(side.Resource) {
		return change, nil
	}
	evidence, diag := filecontent.ResolveMembershipEvidence(ctx, certname, kind, side, retriever)
	change.FileContent = &evidence
	return change, diag
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
	certname string,
	identity model.ResourceIdentity,
	beforeSide, afterSide filecontent.Side,
	retriever filecontent.ContentRetriever,
	f fidelity,
) ([]rawResourceChange, []model.Diagnostic) {
	var changes []rawResourceChange
	var diagnostics []model.Diagnostic
	before, after := beforeSide.Resource.Parameters, afterSide.Resource.Parameters

	isFile := identity.Type == fileResourceType
	fileContentDiffers := false

	names := unionParameterNames(before, after)
	for _, name := range names {
		if isFile && model.FileContentBearingParameter(name) {
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
		if equal, unmeasured := f.equal(bv, av); equal {
			continue
		} else if unmeasured != "" {
			diagnostics = append(diagnostics, fidelityDiagnostic(certname, identity, name, unmeasured))
		}
		changes = append(changes, rawResourceChange{
			Kind:      model.ChangeParameterChanged,
			Identity:  identity,
			Parameter: name,
			Before:    bv,
			After:     av,
		})
	}

	if isFile && (fileContentDiffers || filecontent.NeedsEvidence(beforeSide.Resource) || filecontent.NeedsEvidence(afterSide.Resource)) {
		evidence, diag := filecontent.ResolveFileContentEvidence(ctx, certname,
			identity, beforeSide, afterSide, retriever)
		if diag != nil {
			diagnostics = append(diagnostics, *diag)
		}
		// FileContentUnchanged and FileContentNotManaged are "nothing to
		// report" answers. FileContentIndeterminate is also omitted from
		// the change list: content could not be verified, which is not the
		// same as a verified difference. The verify_content diagnostic
		// already records that the comparison was unverified. A changed
		// source with unverifiable bytes is reference_changed instead
		// (see ResolveFileContentEvidence) and still emits a row.
		if evidence.State != model.FileContentUnchanged && evidence.State != model.FileContentNotManaged && evidence.State != model.FileContentIndeterminate {
			change := rawResourceChange{
				Kind:        model.ChangeParameterChanged,
				Identity:    identity,
				Parameter:   contentBearingParameter,
				FileContent: &evidence,
				Before:      contentParameters(before),
				After:       contentParameters(after),
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

func contentParameters(params map[string]model.Value) map[string]model.Value {
	out := make(map[string]model.Value)
	for _, name := range model.FileContentBearingParameters() {
		if value, ok := params[name]; ok {
			out[name] = value
		}
	}
	return out
}
