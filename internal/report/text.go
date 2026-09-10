package report

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/model"
)

// textf writes one line of the text report, with every control character
// an interpolated value carried replaced by a printable escape.
//
// This format is the one PIACE writes to a terminal: `compare` sends it
// to stdout whenever --text-out is omitted, which is how a CI log gets
// it. Almost everything it interpolates is untrusted. A resource type
// and title, a parameter value and a File `source` come from the
// candidate catalog, which was compiled from the very change under
// review; a change assessment's summary and rationale are free text a
// model wrote. An ESC in any of them is an ANSI control sequence in a
// terminal, and the sequences that clear the screen and reposition the
// cursor are enough to make a run that exits 30 read as `outcome: clean
// (exit 0)`. A bare CR does the same thing more crudely, and a newline
// forges a whole line. The most-read line of the report is the one worth
// forging, so none of them is passed through.
//
// Only the newlines the format string itself contributes, always a
// leading or trailing run and never an interior one, survive. Everything
// between them is a value, and a value has no business carrying a
// control character.
//
// The JSON and HTML reports are deliberately not treated this way. JSON
// escaping already makes a control character inert and the document's
// canonical checksum is what `explain` ties an assessment to, so
// rewriting bytes there would change a document PIACE promises is
// reproducible; html/template's contextual escaping covers the HTML, and
// a terminal control character is not a control character in a browser.
func textf(b *bytes.Buffer, format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	lead := len(s) - len(strings.TrimLeft(s, "\n"))
	trail := len(s) - len(strings.TrimRight(s, "\n"))
	b.WriteString(s[:lead])
	b.WriteString(sanitizeControl(s[lead : len(s)-trail]))
	b.WriteString(s[len(s)-trail:])
}

// sanitizeControl replaces every C0 control character, DEL, and C1
// control character in s with a `\xNN`/`\uNNNN` escape, and returns s
// unchanged when it holds none, which is every ordinary line.
//
// Escaped rather than dropped: a reader who sees `\x1b` in a resource
// title learns that something put an escape sequence there, which is
// worth knowing, where silent removal would show a title that looks
// merely odd.
func sanitizeControl(s string) string {
	if !strings.ContainsFunc(s, isControlRune) {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for _, r := range s {
		switch {
		case !isControlRune(r):
			out.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&out, `\x%02x`, r)
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	return out.String()
}

// isControlRune reports whether r is a C0 control character, DEL, or a
// C1 control character. C1 is included because a terminal decoding UTF-8
// treats U+009B as CSI, the same introducer `ESC [` produces.
func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// Text renders r as the concise CI log report, in a fixed section order:
// final outcome first, then per-target status, node changes, warnings
// and errors, aggregate summary, and impact summary.
//
// The outcome and its reasons come first because a CI log is read from
// the top and often truncated, and both have to be present.
//
// a is the advisory change assessment, or nil. A nil assessment renders
// nothing at all, so a `piace compare` log is what v0.1.0 printed. It is
// a parameter rather than a field on Options because it is not a display
// choice: Options carries presentation policy, and an assessment is
// content that either exists or does not.
//
// opts selects display policy only: what this format prints, never what
// it says about the run. See Options.
func Text(r model.Result, a *assess.Assessment, opts Options) ([]byte, error) {
	var b bytes.Buffer

	textf(&b, "PIACE %s (%s)\n", r.Invocation.ToolVersion, r.Invocation.TimestampUTC)
	if s := r.Invocation.Services; s != nil {
		textf(&b, "services: compiler=%s puppetdb=%s\n", s.Compiler, s.PuppetDB)
	}
	textf(&b, "outcome: %s (exit %d)\n", r.Outcome, r.ExitCode)
	for _, reason := range r.Reasons {
		textf(&b, "  reason: %s\n", reason)
	}

	writeTextTargets(&b, r.Targets)
	writeTextAggregate(&b, r.Aggregate)
	writeTextImpact(&b, r.ImpactEstimates, opts)
	writeTextRunDiagnostics(&b, r.Diagnostics)
	writeTextAssessment(&b, a)

	return b.Bytes(), nil
}

// writeTextAssessment prints the run-level judgement and nothing below
// it. See the doc comment on Text and doc.go's account of what each
// format shows.
//
// It comes last, after every deterministic section including the run
// diagnostics: the assessment is advisory, and a CI log that is
// truncated at the bottom should lose a model's opinion before it loses
// the comparison. AssessmentNote sits directly under the heading, above
// the risk indication, so a log read line by line states what the
// section is before it states what the model thinks.
func writeTextAssessment(b *bytes.Buffer, a *assess.Assessment) {
	if a == nil {
		return
	}
	textf(b, "\n%s:\n", AssessmentLabel)
	textf(b, "  %s\n", AssessmentNote)
	if a.ModelID != "" {
		textf(b, "  model: %s\n", a.ModelID)
	}
	textf(b, "  risk: %s\n", a.Run.Risk)
	if a.Run.Summary != "" {
		textf(b, "  summary: %s\n", a.Run.Summary)
	}
	for _, f := range a.Run.ReviewFocus {
		textf(b, "  review focus: %s\n", f)
	}
	if a.GroupsTruncated {
		textf(b, "  assessed %d of %d resource-change groups\n", a.GroupsAssessed, a.GroupsTotal)
	}
	if a.InputPartial {
		textf(b, "  input partial: the result document records diagnostics\n")
	}
	for _, d := range a.Diagnostics {
		textf(b, "  %s: %s\n", strings.ToUpper(string(d.Severity)), d.Message)
	}
}

func writeTextTargets(b *bytes.Buffer, targets []model.TargetResult) {
	textf(b, "\ntargets (%d):\n", len(targets))
	for _, t := range targets {
		textf(b, "  %s: %s\n", t.Certname, t.Outcome)
		writeTextProvenance(b, t)

		// The v3 trusted-fact warning is emitted before the change list, not
		// buried after it: the warning is owed prominently in every output
		// format, and a reader who stops at the changes must still have seen it.
		if t.Candidate != nil && t.Candidate.V3Warning != "" {
			textf(b, "    WARNING: %s\n", t.Candidate.V3Warning)
		}
		// The baseline's own capture carries the same warning when it
		// was compiled through v3: those are the bytes being compared,
		// whatever API this run used.
		if t.Baseline != nil && t.Baseline.Capture != nil && t.Baseline.Capture.V3Warning != "" {
			textf(b, "    WARNING (baseline capture): %s\n", t.Baseline.Capture.V3Warning)
		}

		if t.NodeDiff != nil {
			writeTextNodeDiff(b, *t.NodeDiff)
		}
		for _, d := range t.Diagnostics {
			textf(b, "    %s [%s]: %s\n", strings.ToUpper(string(d.Severity)), d.Operation, d.Message)
		}
	}
}

// writeTextProvenance prints the baseline, facts, and candidate
// provenance the result owes. A section is omitted entirely when the
// pipeline never got far enough to establish it, which is itself
// informative about where a failed target stopped.
func writeTextProvenance(b *bytes.Buffer, t model.TargetResult) {
	if t.Baseline != nil {
		textf(b, "    baseline:  %s\n", sourceProvenanceLine(*t.Baseline))
	}
	if t.Facts != nil {
		textf(b, "    facts:     %s\n", sourceProvenanceLine(*t.Facts))
	}
	if t.Candidate != nil {
		textf(b, "    candidate: %s\n", candidateProvenanceLine(*t.Candidate))
	}
}

func sourceProvenanceLine(p model.SourceProvenance) string {
	parts := []string{"source=" + string(p.Kind)}
	if p.Environment != "" {
		parts = append(parts, "environment="+p.Environment)
	}
	if p.ProducerTimestamp != "" {
		parts = append(parts, "producer_timestamp="+p.ProducerTimestamp)
	}
	if p.CatalogIdentity != "" {
		parts = append(parts, "catalog_identity="+p.CatalogIdentity)
	}
	if p.Producer != "" {
		parts = append(parts, "producer="+p.Producer)
	}
	if c := p.Capture; c != nil {
		parts = append(parts, fmt.Sprintf("captured_api=%s (requested %s)", c.EffectiveAPI, c.RequestedAPI))
		if c.FellBackFromV4 {
			parts = append(parts, "fell_back_from_v4=true")
		}
	}
	return strings.Join(parts, " ")
}

func candidateProvenanceLine(p model.CandidateProvenance) string {
	parts := []string{
		fmt.Sprintf("api=%s (requested %s)", p.EffectiveAPI, p.RequestedAPI),
		"environment=" + p.Environment,
		"fact_source=" + string(p.FactSource),
	}
	if p.FactsetIdentity != "" {
		parts = append(parts, "factset_identity="+p.FactsetIdentity)
	}
	if p.TrustedFactsSource != "" {
		parts = append(parts, "trusted_facts="+string(p.TrustedFactsSource))
	}
	if p.FellBackFromV4 {
		parts = append(parts, "fell_back_from_v4=true")
	}
	return strings.Join(parts, " ")
}

// writeTextNodeDiff prints one target's displayed changes. The count in
// the header counts what is actually printed, not what the node diff
// holds, so the header can never promise lines that follow it.
//
// A target whose only differences are dependency-graph edges still has
// HasDifference true and still drives the run's outcome and exit code, so
// it gets an explicit note rather than an empty list: a report that
// printed nothing here would read as "no changes" on a run that exits
// non-zero, which is exactly the stdout/exit-code contradiction
// writeReports in cmd/piace is ordered to prevent. The same applies when
// the only resource rows are content_indeterminate: those stay out of
// the change list (they are verify_content notices) but still set
// HasDifference, so the section must not read as "changes: none".
func writeTextNodeDiff(b *bytes.Buffer, nd model.NodeDiff) {
	displayed := displayedResourceChanges(nd.ResourceChanges)
	var indeterminate int
	for _, c := range nd.ResourceChanges {
		if indeterminateParameterChange(c) {
			indeterminate++
		}
	}
	switch {
	case !nd.HasDifference:
		textf(b, "    changes: none\n")
	case len(displayed) > 0:
		textf(b, "    changes (%d):\n", len(displayed))
		for _, c := range displayed {
			textf(b, "      %s\n", changeSummary(c))
		}
	case len(nd.EdgeChanges) > 0 && indeterminate > 0:
		textf(b, "    changes: %d dependency-edge difference(s) only, not shown in the text report; %d unverifiable File content comparison(s) in verify_content notices\n", len(nd.EdgeChanges), indeterminate)
	case len(nd.EdgeChanges) > 0:
		textf(b, "    changes: %d dependency-edge difference(s) only, not shown in the text report\n", len(nd.EdgeChanges))
	case indeterminate > 0:
		textf(b, "    changes: %d unverifiable File content comparison(s); see verify_content notices\n", indeterminate)
	default:
		// HasDifference with neither displayed resources, edges, nor
		// indeterminate parameter rows should not occur; keep a visible
		// mark rather than "none".
		textf(b, "    changes: differences present but not shown in the text report\n")
	}
	for _, e := range nd.Exclusions {
		textf(b, "    excluded: %s\n", exclusionSummary(e))
	}
}

// writeTextAggregate prints one line per displayed aggregate group. The
// header counts displayed groups rather than len(agg.Groups), which still
// includes the edge groups this format filters out.
//
// Certnames are never capped here: they are the operator's own
// configured targets, a set they wrote themselves and whose size they
// already know, unlike an impact estimate's certnames, which come from
// the estate.
func writeTextAggregate(b *bytes.Buffer, agg model.AggregateDiff) {
	groups := displayedGroups(agg.Groups)
	textf(b, "\naggregate diff (%d):\n", len(groups))
	for _, g := range groups {
		textf(b, "  %s: %s\n", aggregateGroupLabel(g), targetCountList(g.Certnames))
	}
}

// writeTextImpact renders the impact section, one line per estimate. The
// section header carries the mandatory label and note, which is what
// keeps a bare "N nodes" line from reading as a prediction: the note
// above it states, once for the whole section, that a listed certname
// means only that the node's latest stored catalog contains the resource.
// No individual entry ever phrases a certname as a node that will change.
func writeTextImpact(b *bytes.Buffer, estimates []model.ImpactEstimate, opts Options) {
	if len(estimates) == 0 {
		return
	}
	textf(b, "\n%s (%d):\n", ImpactEstimateLabel, len(estimates))
	textf(b, "  %s\n", ImpactEstimateNote)
	for _, e := range estimates {
		textf(b, "  %s: %s\n", e.Identity, estimateSummary(e, opts.ImpactNodes))
	}
}

func writeTextRunDiagnostics(b *bytes.Buffer, diagnostics []model.Diagnostic) {
	if len(diagnostics) == 0 {
		return
	}
	textf(b, "\nrun diagnostics (%d):\n", len(diagnostics))
	for _, d := range diagnostics {
		textf(b, "  %s [%s]: %s\n", strings.ToUpper(string(d.Severity)), d.Operation, d.Message)
	}
}
