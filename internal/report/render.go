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
// digests only; managed content bytes are never available here to
// render.
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
// state, which step of the priority order produced it, and the digest
// pair when one exists and is not redacted.
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

// certnameList renders a certname sample as one comma-separated line.
// Without Options.ImpactNodes it prints at most inlineCertnameCap names
// and reports the rest as a "+N more" tail: the count stays exact, only
// the names are elided, so a compact line never understates how many
// nodes an estimate returned.
func certnameList(names []string, showAll bool) string {
	if len(names) == 0 {
		return ""
	}
	if showAll || len(names) <= inlineCertnameCap {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(names[:inlineCertnameCap], ", "), len(names)-inlineCertnameCap)
}

// aggregateKeyLabel renders an aggregate group's key. It handles both
// shapes of model.AggregateChangeKey — exactly one of Identity and Edge
// is set, selected by Kind — so an edge group never dereferences a nil
// Identity.
//
// The text report filters edge groups out before reaching here, so its
// Edge branch is exercised only by HTML (which renders them) and by
// direct callers. It is written defensively regardless: Identity is a
// pointer that is nil for every edge-kind key, so any caller that reaches
// this function with one would otherwise panic.
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

// aggregateGroupLabel renders an aggregate group's change as one phrase:
// its key, plus the before/after pair when the group carries one.
//
// The value pair is joined without a colon after the parameter name —
// "ensure \"running\" -> \"stopped\"", not "ensure: \"running\" -> ..." —
// so that the only colon on the finished line is the structural one
// separating the change from its targets.
func aggregateGroupLabel(g model.AggregateGroup) string {
	label := aggregateKeyLabel(g.Key)
	if g.Before != nil || g.After != nil {
		label += fmt.Sprintf(" %s -> %s", formatValue(g.Before), formatValue(g.After))
	}
	return label
}

// targetCountList renders an aggregate group's certnames as a count and
// the names, matching the shape an impact estimate uses for its own
// nodes so the two sections read alike.
//
// The count is not bracketed. "Class[nginx] [4]" puts a bracketed number
// immediately after a bracketed resource title, which reads as a second
// resource reference on a line whose whole purpose is to be scanned
// quickly.
//
// Nor is the list ever capped: these are the operator's own configured
// targets, a set they wrote and whose size they already know — unlike an
// impact estimate's certnames, which come from the estate.
func targetCountList(certnames []string) string {
	noun := "targets"
	if len(certnames) == 1 {
		noun = "target"
	}
	return fmt.Sprintf("%d %s: %s", len(certnames), noun, strings.Join(certnames, ", "))
}

// displayedGroups filters an aggregate diff down to the groups the text
// report shows. Edge groups are dropped: a CI log is a linear read with
// no way to skip a section, and a run's edge groups routinely outnumber
// its resource groups.
//
// HTML does not use this, since it renders edge groups behind their own
// disclosure, and the underlying model.AggregateDiff is untouched: edge
// changes survive aggregation as a distinct kind, and they do here too.
func displayedGroups(groups []model.AggregateGroup) []model.AggregateGroup {
	displayed := make([]model.AggregateGroup, 0, len(groups))
	for _, g := range groups {
		if g.Key.Kind == model.ChangeEdgeAdded || g.Key.Kind == model.ChangeEdgeRemoved {
			continue
		}
		displayed = append(displayed, g)
	}
	return displayed
}

// exclusionSummary renders one applied exclusion rule and its suppressed
// counts for the text report. The suppressed-edge count is omitted for
// the same reason edge changes themselves are omitted from that format
// (see doc.go).
func exclusionSummary(e model.ExclusionOutcome) string {
	return fmt.Sprintf("%s[%s]: %d resource(s), %d parameter(s) suppressed",
		e.Rule.Type, e.Rule.Title, e.SuppressedResources, e.SuppressedParameters)
}

// exclusionSummaryFull is exclusionSummary with the suppressed-edge count
// restored, for the HTML report and the JSON document — the two formats
// that keep everything.
func exclusionSummaryFull(e model.ExclusionOutcome) string {
	return fmt.Sprintf("%s[%s]: %d resource(s), %d parameter(s), %d edge(s) suppressed",
		e.Rule.Type, e.Rule.Title, e.SuppressedResources, e.SuppressedParameters, e.SuppressedEdges)
}

// estimateSummary renders one estimate as a single compact line: the
// count the bounded query returned, followed by the certname sample. It
// describes what the query returned, never a total and never a
// prediction; see model.ImpactEstimate.ResultCount.
//
// Only the happy path collapses. A truncated estimate still says so and
// still names its result_limit, and a timeout or failure still reports
// its status and reason rather than a node count — compacting either
// into "N nodes" would state something the run does not know.
//
// The PQL and the request options are deliberately absent: they are the
// same string on every line of a several-hundred-estimate section, and
// reporting the exact generated PQL query is discharged by the JSON
// report, which records both verbatim.
func estimateSummary(e model.ImpactEstimate, showAllNodes bool) string {
	summary := estimateCount(e)
	if e.Status != model.ImpactStatusCompleted {
		return summary
	}
	if list := certnameList(e.Certnames, showAllNodes); list != "" {
		summary += ": " + list
	}
	return summary
}

// estimateCount states what the bounded query returned, without naming
// any node. It is the whole of an estimate's line in the text report
// (before the sample) and the always-visible half of one in HTML, where
// the certnames sit behind a disclosure.
//
// Only the happy path collapses to a bare count. A truncated estimate
// still says so and still names its result_limit, and a timeout or
// failure still reports its status and reason rather than a node count —
// compacting either into "N nodes" would state something the run does
// not know.
func estimateCount(e model.ImpactEstimate) string {
	if e.Status != model.ImpactStatusCompleted {
		reason := e.FailureReason
		if reason == "" {
			reason = "no reason reported"
		}
		return fmt.Sprintf("%s: %s", e.Status, reason)
	}
	if e.Truncated {
		// ResultCount is ResultLimit+1 here — all the bounded query asked
		// for — so the only true statement about the population is that
		// it exceeds the limit. "more than N" says exactly that; the
		// exact count would be a fabrication.
		return fmt.Sprintf("more than %d nodes (truncated at result_limit %d)", e.ResultLimit, e.ResultLimit)
	}
	return fmt.Sprintf("%d %s", e.ResultCount, plural(e.ResultCount, "node", "nodes"))
}

// sortedKeys returns m's keys in lexicographic order. Every map in a
// model.Result is iterated through this, so no text or HTML output
// depends on Go's randomized map iteration order.
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
