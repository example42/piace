package report

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/example42/piace/internal/model"
)

// Text renders r as the concise CI log report required by
// requirements.md 8.1, in the section order design.md section 9 fixes:
// "Text renders final outcome first, then per-target status, node
// changes, warnings/errors, aggregate summary, and impact summary."
//
// The outcome and its reasons come first because a CI log is read from
// the top and often truncated; requirements.md 10.2 requires both to be
// present.
func Text(r model.Result) ([]byte, error) {
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
	writeTextImpact(&b, r.ImpactEstimates)
	writeTextRunDiagnostics(&b, r.Diagnostics)

	return b.Bytes(), nil
}

func writeTextTargets(b *bytes.Buffer, targets []model.TargetResult) {
	fmt.Fprintf(b, "\ntargets (%d):\n", len(targets))
	for _, t := range targets {
		fmt.Fprintf(b, "  %s: %s\n", t.Certname, t.Outcome)
		writeTextProvenance(b, t)

		// The v3 trusted-fact warning is emitted before the change list,
		// not buried after it: requirements.md 2.5 calls for a
		// "prominent" warning in every output format, and a reader who
		// stops at the changes must still have seen it.
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
// provenance requirements.md 1.2 and 2.2 require in the result. A section
// is omitted entirely when the pipeline never got far enough to establish
// it, which is itself informative about where a failed target stopped.
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

func writeTextNodeDiff(b *bytes.Buffer, nd model.NodeDiff) {
	if !nd.HasDifference {
		fmt.Fprintf(b, "    changes: none\n")
	} else {
		fmt.Fprintf(b, "    changes (%d resource, %d edge):\n", len(nd.ResourceChanges), len(nd.EdgeChanges))
		for _, c := range nd.ResourceChanges {
			fmt.Fprintf(b, "      %s\n", changeSummary(c))
		}
		for _, c := range nd.EdgeChanges {
			fmt.Fprintf(b, "      %s\n", edgeSummary(c))
		}
	}
	for _, e := range nd.Exclusions {
		fmt.Fprintf(b, "    excluded: %s\n", exclusionSummary(e))
	}
}

func writeTextAggregate(b *bytes.Buffer, agg model.AggregateDiff) {
	fmt.Fprintf(b, "\naggregate diff (%d group(s)):\n", len(agg.Groups))
	for _, g := range agg.Groups {
		fmt.Fprintf(b, "  %s\n", aggregateKeyLabel(g.Key))
		if g.Key.Edge == nil && (g.Before != nil || g.After != nil) {
			fmt.Fprintf(b, "    %s -> %s\n", formatValue(g.Before), formatValue(g.After))
		}
		fmt.Fprintf(b, "    %d target(s): %s\n", len(g.Certnames), strings.Join(g.Certnames, ", "))
	}
}

// writeTextImpact renders the impact section. The section header carries
// requirement 9.3's label and note; individual entries never phrase a
// returned certname as a node that will change.
func writeTextImpact(b *bytes.Buffer, estimates []model.ImpactEstimate) {
	if len(estimates) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s (%d):\n", ImpactEstimateLabel, len(estimates))
	fmt.Fprintf(b, "  %s\n", ImpactEstimateNote)
	for _, e := range estimates {
		fmt.Fprintf(b, "  %s: %s\n", e.Identity, estimateSummary(e))
		fmt.Fprintf(b, "    pql: %s\n", e.PQL)
		fmt.Fprintf(b, "    request: path=%s limit=%d timeout=%s", e.Request.Path, e.Request.Limit, e.Timeout)
		if e.Request.OrderBy != "" {
			fmt.Fprintf(b, " order_by=%s", e.Request.OrderBy)
		}
		fmt.Fprintln(b)
		if len(e.Certnames) > 0 {
			fmt.Fprintf(b, "    nodes with this resource in their latest stored catalog: %s\n", strings.Join(e.Certnames, ", "))
		}
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
