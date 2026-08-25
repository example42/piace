package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

// formatValue renders one canonical parameter value as compact canonical
// JSON, so text and HTML display a value exactly as the JSON report
// encodes it and a number is spelled the same way in all three.
//
// It marshals first and canonicalizes the result rather than handing the
// value straight to snapshot.CanonicalJSON, because the model.Value
// domain includes model.Number, which the canonical encoder does not
// accept directly but which marshals to its exact decimal digits through
// its MarshalJSON method.
//
// The canonical bytes are then re-encoded with HTML escaping disabled,
// for display only. encoding/json escapes `<`, `>`, and `&` as \u003c,
// \u003e, and \u0026 by default, and the canonical encoder inherits
// that — which is correct for the JSON artifact and for a snapshot
// checksum, but makes the redaction marker read as
// "\u003credacted\u003e" in a CI log. The re-encode changes escaping
// and nothing else: keys stay sorted, numbers keep the canonical decimal
// spelling (json.Number round-trips verbatim), so the displayed value is
// the same JSON value the artifact records. The HTML renderer receives
// the unescaped form and html/template escapes it for its own context.
//
// A value that cannot be encoded is rendered as a fixed placeholder
// rather than as an error: the surrounding change is still real and must
// still be reported, and the only values that can reach here have already
// passed internal/normalize's domain check. The placeholder is not a
// value excerpt, so it discloses nothing.
func formatValue(v model.Value) string {
	if v == nil {
		return "null"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return unencodableValue
	}
	canonical, err := snapshot.CanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return unencodableValue
	}
	return displayJSON(canonical)
}

// unencodableValue is the placeholder shown for a value outside the
// model.Value domain. It is not a value excerpt.
const unencodableValue = "<unencodable>"

// displayJSON re-encodes canonical JSON bytes with HTML escaping
// disabled. On any failure it returns the canonical bytes unchanged:
// escaped output is less readable but never wrong.
func displayJSON(canonical []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return string(canonical)
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(decoded); err != nil {
		return string(canonical)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// changeSummary renders one resource-level change as a single line.
// File-content evidence is summarized from its state, algorithm, and
// digests only; managed content bytes are never available here to render
// (requirements.md 5.8).
func changeSummary(c model.ResourceChange) string {
	switch c.Kind {
	case model.ChangeResourceAdded:
		return "+ " + c.Identity.String()
	case model.ChangeResourceRemoved:
		return "- " + c.Identity.String()
	case model.ChangeParameterChanged:
		if c.FileContent != nil {
			return fmt.Sprintf("~ %s %s: %s", c.Identity, c.Parameter, fileContentSummary(*c.FileContent))
		}
		return fmt.Sprintf("~ %s %s: %s -> %s", c.Identity, c.Parameter, formatValue(c.Before), formatValue(c.After))
	default:
		return fmt.Sprintf("? %s (%s)", c.Identity, c.Kind)
	}
}

// fileContentSummary describes File-content evidence: the comparison
// state, which step of design.md section 7.2's priority order produced
// it, and the digest pair when one exists and is not redacted.
func fileContentSummary(e model.FileContentEvidence) string {
	summary := string(e.State)
	if e.EvidenceSource != "" {
		summary += " (via " + string(e.EvidenceSource) + ")"
	}
	if e.Redacted {
		return summary + " [redacted]"
	}
	if e.BeforeDigest != "" || e.AfterDigest != "" {
		algorithm := e.Algorithm
		if algorithm == "" {
			algorithm = "digest"
		}
		summary += fmt.Sprintf(" %s %s -> %s", algorithm, e.BeforeDigest, e.AfterDigest)
	}
	return summary
}

// edgeSummary renders one edge-level change as a single line. Direction
// is significant (design.md section 7.1), so the arrow is never
// normalized away.
func edgeSummary(c model.EdgeChange) string {
	sign := "+"
	if c.Kind == model.ChangeEdgeRemoved {
		sign = "-"
	}
	return fmt.Sprintf("%sedge %s -> %s", sign, c.Edge.Source, c.Edge.Target)
}

// aggregateKeyLabel renders an aggregate group's key. It handles both
// shapes of model.AggregateChangeKey — exactly one of Identity and Edge
// is set, selected by Kind — so an edge group never dereferences a nil
// Identity.
func aggregateKeyLabel(key model.AggregateChangeKey) string {
	if key.Edge != nil {
		return fmt.Sprintf("%s %s -> %s", key.Kind, key.Edge.Source, key.Edge.Target)
	}
	if key.Identity == nil {
		return string(key.Kind)
	}
	if key.Parameter != "" {
		return fmt.Sprintf("%s %s %s", key.Kind, key.Identity, key.Parameter)
	}
	return fmt.Sprintf("%s %s", key.Kind, key.Identity)
}

// exclusionSummary renders one applied exclusion rule and its suppressed
// counts, per requirements.md 6.5.
func exclusionSummary(e model.ExclusionOutcome) string {
	return fmt.Sprintf("%s[%s]: %d resource(s), %d parameter(s), %d edge(s) suppressed",
		e.Rule.Type, e.Rule.Title, e.SuppressedResources, e.SuppressedParameters, e.SuppressedEdges)
}

// estimateSummary renders one estimate's status line. It describes what
// the bounded query returned, never a total and never a prediction; see
// model.ImpactEstimate.ResultCount.
func estimateSummary(e model.ImpactEstimate) string {
	switch e.Status {
	case model.ImpactStatusCompleted:
		summary := fmt.Sprintf("completed: %d node(s) returned (result_limit %d)", e.ResultCount, e.ResultLimit)
		if e.Truncated {
			summary += fmt.Sprintf("; truncated, showing the first %d certnames in sorted order", len(e.Certnames))
		}
		return summary
	default:
		reason := e.FailureReason
		if reason == "" {
			reason = "no reason reported"
		}
		return fmt.Sprintf("%s: %s", e.Status, reason)
	}
}

// sortedKeys returns m's keys in lexicographic order. Every map in a
// model.Result is iterated through this, so no text or HTML output
// depends on Go's randomized map iteration order (design.md Property 1).
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mapEntry is one provenance key/value pair, pre-rendered and ordered for
// display by both the text and the HTML renderer.
type mapEntry struct {
	Key   string
	Value string
}

// mapEntries renders a provenance map as ordered, display-ready pairs.
func mapEntries(m map[string]any) []mapEntry {
	entries := make([]mapEntry, 0, len(m))
	for _, k := range sortedKeys(m) {
		entries = append(entries, mapEntry{Key: k, Value: fmt.Sprintf("%v", m[k])})
	}
	return entries
}
