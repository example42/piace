package report

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/model"
)

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

	fmt.Fprintf(&b, "PIACE %s (%s)\n", r.Invocation.ToolVersion, r.Invocation.TimestampUTC)
	if s := r.Invocation.Services; s != nil {
		fmt.Fprintf(&b, "services: compiler=%s puppetdb=%s\n", s.Compiler, s.PuppetDB)
	}
	fmt.Fprintf(&b, "outcome: %s (exit %d)\n", r.Outcome, r.ExitCode)
	for _, reason := range r.Reasons {
		fmt.Fprintf(&b, "  reason: %s\n", reason)
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
	fmt.Fprintf(b, "\n%s:\n", AssessmentLabel)
	fmt.Fprintf(b, "  %s\n", AssessmentNote)
	if a.ModelID != "" {
		fmt.Fprintf(b, "  model: %s\n", a.ModelID)
	}
	fmt.Fprintf(b, "  risk: %s\n", a.Run.Risk)
	if a.Run.Summary != "" {
		fmt.Fprintf(b, "  summary: %s\n", a.Run.Summary)
	}
	for _, f := range a.Run.ReviewFocus {
		fmt.Fprintf(b, "  review focus: %s\n", f)
	}
	if a.GroupsTruncated {
		fmt.Fprintf(b, "  assessed %d of %d resource-change groups\n", a.GroupsAssessed, a.GroupsTotal)
	}
	if a.InputPartial {
		fmt.Fprintf(b, "  input partial: the result document records diagnostics\n")
	}
	for _, d := range a.Diagnostics {
		fmt.Fprintf(b, "  %s: %s\n", strings.ToUpper(string(d.Severity)), d.Message)
	}
}

func writeTextTargets(b *bytes.Buffer, targets []model.TargetResult) {
	fmt.Fprintf(b, "\ntargets (%d):\n", len(targets))
	for _, t := range targets {
		fmt.Fprintf(b, "  %s: %s\n", t.Certname, t.Outcome)
		writeTextProvenance(b, t)

		// The v3 trusted-fact warning is emitted before the change list, not
		// buried after it: the warning is owed prominently in every output
		// format, and a reader who stops at the changes must still have seen it.
		if t.Candidate != nil && t.Candidate.V3Warning != "" {
			fmt.Fprintf(b, "    WARNING: %s\n", t.Candidate.V3Warning)
		}

		if t.NodeDiff != nil {
			writeTextNodeDiff(b, *t.NodeDiff)
		}
		for _, d := range t.Diagnostics {
			fmt.Fprintf(b, "    %s [%s]: %s\n", strings.ToUpper(string(d.Severity)), d.Operation, d.Message)
		}
	}
}

// writeTextProvenance prints the baseline, facts, and candidate
// provenance the result owes. A section is omitted entirely when the
// pipeline never got far enough to establish it, which is itself
// informative about where a failed target stopped.
func writeTextProvenance(b *bytes.Buffer, t model.TargetResult) {
	if t.Baseline != nil {
		fmt.Fprintf(b, "    baseline:  %s\n", sourceProvenanceLine(*t.Baseline))
	}
	if t.Facts != nil {
		fmt.Fprintf(b, "    facts:     %s\n", sourceProvenanceLine(*t.Facts))
	}
	if t.Candidate != nil {
		fmt.Fprintf(b, "    candidate: %s\n", candidateProvenanceLine(*t.Candidate))
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
// writeReports in cmd/piace is ordered to prevent.
func writeTextNodeDiff(b *bytes.Buffer, nd model.NodeDiff) {
	switch {
	case !nd.HasDifference:
		fmt.Fprintf(b, "    changes: none\n")
	case len(nd.ResourceChanges) == 0:
		fmt.Fprintf(b, "    changes: %d dependency-edge difference(s) only, not shown in the text report\n", len(nd.EdgeChanges))
	default:
		fmt.Fprintf(b, "    changes (%d):\n", len(nd.ResourceChanges))
		for _, c := range nd.ResourceChanges {
			fmt.Fprintf(b, "      %s\n", changeSummary(c))
		}
	}
	for _, e := range nd.Exclusions {
		fmt.Fprintf(b, "    excluded: %s\n", exclusionSummary(e))
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
	fmt.Fprintf(b, "\naggregate diff (%d):\n", len(groups))
	for _, g := range groups {
		fmt.Fprintf(b, "  %s: %s\n", aggregateGroupLabel(g), targetCountList(g.Certnames))
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
	fmt.Fprintf(b, "\n%s (%d):\n", ImpactEstimateLabel, len(estimates))
	fmt.Fprintf(b, "  %s\n", ImpactEstimateNote)
	for _, e := range estimates {
		fmt.Fprintf(b, "  %s: %s\n", e.Identity, estimateSummary(e, opts.ImpactNodes))
	}
}

func writeTextRunDiagnostics(b *bytes.Buffer, diagnostics []model.Diagnostic) {
	if len(diagnostics) == 0 {
		return
	}
	fmt.Fprintf(b, "\nrun diagnostics (%d):\n", len(diagnostics))
	for _, d := range diagnostics {
		fmt.Fprintf(b, "  %s [%s]: %s\n", strings.ToUpper(string(d.Severity)), d.Operation, d.Message)
	}
}
