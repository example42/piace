package model

import "github.com/example42/piace/internal/exitcode"

// ResultSchemaVersion is the current `schema_version` for the shared
// result document. Consumers (CI scripts, the HTML/text renderers) key
// their parsing on this value; an incompatible schema change increments
// it.
const ResultSchemaVersion = 1

// Invocation carries invocation metadata: tool version, run timestamp,
// and resolved safe configuration provenance. It is the result
// document's first element, and it is distinct from the per-target
// baseline, facts and candidate provenance in TargetResult.
type Invocation struct {
	ToolVersion string `json:"tool_version"`
	// TimestampUTC is RFC 3339 in UTC.
	TimestampUTC string `json:"timestamp_utc"`
	// Services records which compiler and PuppetDB this run was
	// configured to reach. It is omitted for a document built without
	// resolved services (a capture-side or test-constructed Result).
	Services *ServiceProvenance `json:"services,omitempty"`
}

// ServiceProvenance names the two service authorities a run was allowed
// to contact. Provenance excludes only endpoint credentials and private
// key paths, so the authority itself is safe to record, and recording it
// is what lets a reader audit a report's reach without consulting the
// services file.
//
// Each field is the endpoint's authority (host and, when non-default,
// port) only: never the CA bundle, client certificate, or private key
// path, and never a full URL with credentials in it.
type ServiceProvenance struct {
	Compiler string `json:"compiler"`
	PuppetDB string `json:"puppetdb"`
}

// TargetResult is the complete reported outcome for one target: its
// source/candidate provenance, node diff, diagnostics, and target-local
// outcome. Every selected target has exactly one TargetResult, sorted by
// certname in the shared result document.
type TargetResult struct {
	Certname    string               `json:"certname"`
	Outcome     exitcode.Outcome     `json:"outcome"`
	Baseline    *SourceProvenance    `json:"baseline,omitempty"`
	Facts       *SourceProvenance    `json:"facts,omitempty"`
	Candidate   *CandidateProvenance `json:"candidate,omitempty"`
	Config      *ConfigProvenance    `json:"config,omitempty"`
	NodeDiff    *NodeDiff            `json:"node_diff,omitempty"`
	Diagnostics []Diagnostic         `json:"diagnostics,omitempty"`
}

// Result is the shared, versioned result document produced by `piace
// compare` and rendered identically into JSON, text, and HTML.
type Result struct {
	SchemaVersion int            `json:"schema_version"`
	Invocation    Invocation     `json:"invocation"`
	Targets       []TargetResult `json:"targets"`
	Aggregate     AggregateDiff  `json:"aggregate"`
	// ImpactEstimates holds the run's potential impact estimates. They live
	// at document level, not on a TargetResult, because impact.EstimateAll
	// deduplicates them run-wide by exact `Type[title]` and resolves each
	// one's limits from the first *enabling* target in target-file order: an
	// estimate is therefore structurally not attributable to a single
	// target.
	//
	// Renderers must label this section a **potential impact estimate**.
	// model.ImpactEstimate carries state, not prose; see internal/impact's
	// doc.go.
	ImpactEstimates []ImpactEstimate `json:"impact_estimates,omitempty"`
	// ImpactEstimateLabel and ImpactEstimateNote carry the mandatory
	// labelling wording into the JSON report. They are populated by the JSON
	// renderer (internal/report), not by the pipeline: the constants live
	// there so text, JSON, and HTML cannot drift into saying different
	// things, and keeping prose out of the pipeline is what lets
	// internal/impact stay state-only (see its doc.go). They are declared
	// here only because the JSON report is this struct.
	ImpactEstimateLabel string `json:"impact_estimate_label,omitempty"`
	ImpactEstimateNote  string `json:"impact_estimate_note,omitempty"`
	// Diagnostics holds run-level diagnostics that belong to no single
	// target: invalid global configuration, and the estimate_impact failures
	// impact.EstimateAll reports for run-wide deduplicated identities.
	// Finalize folds their severity into the final outcome, which is how an
	// estimate failure contributes an operational outcome after all other
	// targets finish.
	Diagnostics []Diagnostic     `json:"diagnostics,omitempty"`
	Outcome     exitcode.Outcome `json:"outcome"`
	ExitCode    int              `json:"exit_code"`
	// Reasons is the ordered reason list explaining Outcome, which the
	// result document carries alongside the final outcome and exit code.
	Reasons []string `json:"reasons,omitempty"`
}

// NewResult builds an empty Result with the schema version and invocation
// metadata populated. Callers append TargetResult entries and finalize the
// outcome via the reducer in package exitcode.
func NewResult(toolVersion, timestampUTC string) Result {
	return Result{
		SchemaVersion: ResultSchemaVersion,
		Invocation: Invocation{
			ToolVersion:  toolVersion,
			TimestampUTC: timestampUTC,
		},
	}
}

// Finalize computes r.Outcome and r.ExitCode from the per-target
// outcomes and r.Diagnostics using the exitcode package's fixed
// precedence. Reasons is left to the caller; Reduce (outcome.go) is the
// entry point that classifies targets, calls Finalize, and builds the
// reason list in one step.
//
// Run-level diagnostics are folded in as well as per-target outcomes, so
// a failed or timed-out impact estimate, which belongs to no target
// since estimates are deduplicated run-wide, still reduces to an
// operational outcome.
func (r *Result) Finalize() {
	outcomes := make([]exitcode.Outcome, 0, len(r.Targets)+len(r.Diagnostics))
	for _, t := range r.Targets {
		outcomes = append(outcomes, t.Outcome)
	}
	for _, d := range r.Diagnostics {
		if outcome, contributes := OutcomeForDiagnostic(d); contributes {
			outcomes = append(outcomes, outcome)
		}
	}
	r.Outcome = exitcode.ReduceAll(outcomes)
	r.ExitCode = int(exitcode.ForOutcome(r.Outcome))
}
