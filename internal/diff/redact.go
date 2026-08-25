package diff

import (
	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/model"
)

// pcoreTypeKey and pcoreSensitiveType are the reserved Pcore generic-data
// keys Puppet's Ruby serializer emits for a Sensitive-wrapped value; see
// doc.go's "Redaction source 1" section for the full derivation and its
// confidence level.
const (
	pcoreTypeKey       = "__ptype"
	pcoreSensitiveType = "Sensitive"
)

// redactChanges implements pass 3 (see doc.go). It runs strictly after
// applyExclusions and after NodeDiff.HasDifference has already been
// computed, so masking a value can never turn a real difference into a
// non-difference — it only replaces what a report may display.
//
// Both redaction sources are applied to every remaining change:
//
//   - a Puppet `Sensitive` wrapper found at any depth in a Before/After
//     canonical value tree, and
//   - a configured config.RedactionSelector whose Type and Parameter both
//     match the change exactly (case-sensitive).
//
// Changes are returned as a new slice; the input entries are copied by
// value and their canonical value trees are never mutated in place, so a
// caller holding the pre-redaction changes (for example for its own
// comparison purposes) is unaffected.
func redactChanges(selectors []config.RedactionSelector, changes []model.ResourceChange) []model.ResourceChange {
	if len(changes) == 0 {
		return changes
	}
	out := make([]model.ResourceChange, len(changes))
	for i, change := range changes {
		out[i] = redactChange(selectors, change)
	}
	return out
}

// redactChange applies both redaction sources to one change.
func redactChange(selectors []config.RedactionSelector, change model.ResourceChange) model.ResourceChange {
	selected := matchesRedactionSelector(selectors, change.Identity.Type, change.Parameter)

	if change.FileContent != nil {
		// A File-content entry carries no Before/After at all (see
		// resources.go); its only redactable evidence is the digest pair.
		// State is deliberately preserved either way, per design.md
		// section 7.2: "a redacted content selector emits a stable
		// REDACTED value while preserving the change classification and
		// no digest in reports."
		evidence := *change.FileContent
		if selected {
			evidence.Algorithm = ""
			evidence.BeforeDigest = model.RedactedValue
			evidence.AfterDigest = model.RedactedValue
			evidence.Redacted = true
		}
		change.FileContent = &evidence
		return change
	}

	if selected {
		// A configured selector redacts the whole matched value, so
		// there is nothing left for the Sensitive walk to find.
		if change.Before != nil {
			change.Before = model.RedactedValue
		}
		if change.After != nil {
			change.After = model.RedactedValue
		}
		return change
	}

	change.Before = redactSensitiveValue(change.Before)
	change.After = redactSensitiveValue(change.After)
	return change
}

// matchesRedactionSelector reports whether any configured selector names
// this exact resource type and parameter name. Both comparisons are
// exact and case-sensitive, per requirements.md 8.8 and design.md
// section 3.2 rule 4. A change with no parameter name (a resource
// added/removed entry) never matches, since a selector always names a
// parameter.
func matchesRedactionSelector(selectors []config.RedactionSelector, resourceType, parameter string) bool {
	if parameter == "" {
		return false
	}
	for _, s := range selectors {
		if s.Type == resourceType && s.Parameter == parameter {
			return true
		}
	}
	return false
}

// redactSensitiveValue walks a canonical value tree and replaces every
// Puppet `Sensitive` wrapper it finds — at any depth, inside maps and
// arrays alike — with model.RedactedValue, matching design.md section
// 7.3's "Puppet `Sensitive` wrappers are detected recursively; their
// payload is never copied to the serializable result."
//
// The entire matched subtree is replaced, never merely its `__pvalue`
// entry: leaving the wrapper object in place with a redacted payload
// would still disclose the payload's shape (map keys, array length,
// nesting depth), which is evidence about the secret.
//
// The walk never mutates its input: every map and slice containing a
// redacted descendant is rebuilt, so the caller's pre-redaction tree —
// the one pass 1 compared and fingerprintResourceChange digested —
// stays intact.
func redactSensitiveValue(v model.Value) model.Value {
	switch val := v.(type) {
	case map[string]model.Value:
		if isSensitiveWrapper(val) {
			return model.RedactedValue
		}
		out := make(map[string]model.Value, len(val))
		for k, e := range val {
			out[k] = redactSensitiveValue(e)
		}
		return out
	case []model.Value:
		out := make([]model.Value, len(val))
		for i, e := range val {
			out[i] = redactSensitiveValue(e)
		}
		return out
	default:
		return v
	}
}

// isSensitiveWrapper reports whether m is the Pcore generic-data
// encoding of a Sensitive-wrapped value: a JSON object whose reserved
// `__ptype` key holds exactly the string "Sensitive". The payload key
// (`__pvalue`) is deliberately not required to be present — a wrapper
// missing it is still a declared Sensitive value and must still be
// masked rather than passed through.
func isSensitiveWrapper(m map[string]model.Value) bool {
	ptype, ok := m[pcoreTypeKey]
	if !ok {
		return false
	}
	name, ok := ptype.(string)
	return ok && name == pcoreSensitiveType
}
