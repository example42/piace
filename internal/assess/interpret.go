package assess

import (
	"encoding/json"
	"fmt"
)

// AISchemaVersion is the change assessment artifact's own version. It is
// independent of model.ResultSchemaVersion by design: the assessment is
// quarantined out of the result document so that document's determinism
// guarantee is not weakened to accommodate it. See
// docs/adr/0002-keep-the-change-assessment-out-of-the-result-document.md.
const AISchemaVersion = 1

// DiagnosticSeverity mirrors the result document's two severities without
// sharing the type. This package reads model.Result, but a change
// assessment's diagnostics must never reach the outcome reducer, and a
// shared type is the sort of proximity that eventually lets them.
type DiagnosticSeverity string

const (
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"
)

// Diagnostic is one problem encountered producing a change assessment. It
// never affects a comparison outcome or exit status.
type Diagnostic struct {
	Severity DiagnosticSeverity `json:"severity"`
	Message  string             `json:"message"`
}

// RunAssessment is the run-level judgement.
type RunAssessment struct {
	Risk        Risk     `json:"risk"`
	Summary     string   `json:"summary"`
	ReviewFocus []string `json:"review_focus,omitempty"`
}

// GroupAssessment is the judgement for one aggregate group, anchored to
// the group by the id the request assigned it.
type GroupAssessment struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Identity    string   `json:"identity"`
	Parameter   string   `json:"parameter,omitempty"`
	Certnames   []string `json:"certnames,omitempty"`
	Risk        Risk     `json:"risk"`
	Rationale   string   `json:"rationale,omitempty"`
	ReviewFocus []string `json:"review_focus,omitempty"`
}

// Assessment is the change assessment artifact.
type Assessment struct {
	AISchemaVersion int    `json:"ai_schema_version"`
	GeneratedAt     string `json:"generated_at,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	// EndpointAuthority is the inference service's host, recorded so a
	// reader can audit where an assessment came from without opening the
	// services file — the same reason model.ServiceProvenance exists.
	EndpointAuthority string `json:"endpoint_authority,omitempty"`
	// SourceReportChecksum ties an assessment to the exact result
	// document it was derived from.
	SourceReportChecksum string `json:"source_report_checksum,omitempty"`
	// SourceReportOutcome records the deterministic outcome the
	// assessment was built on, so a reader never has to take the model's
	// word for what the comparison found.
	SourceReportOutcome string `json:"source_report_outcome,omitempty"`

	Run    RunAssessment     `json:"run"`
	Groups []GroupAssessment `json:"groups,omitempty"`

	// GroupsTotal counts the groups eligible for assessment, which is
	// resource-change groups only: edge groups are dropped before ranking
	// (see PlanGroups), so GroupsAssessed and GroupsTruncated are stated
	// against this number rather than against every aggregate group.
	GroupsTotal     int  `json:"groups_total"`
	GroupsAssessed  int  `json:"groups_assessed"`
	GroupsTruncated bool `json:"groups_truncated"`

	// InputPartial records that the result document itself was
	// incomplete — a retrieval or compilation failure — so the assessment
	// says what it could not see rather than reading as a full review.
	InputPartial bool `json:"input_partial,omitempty"`

	ChangeContext *ChangeContext `json:"change_context,omitempty"`
	Diagnostics   []Diagnostic   `json:"diagnostics,omitempty"`
}

// responseDoc is the structured response an inference service returns.
type responseDoc struct {
	Run struct {
		Risk        Risk     `json:"risk"`
		Summary     string   `json:"summary"`
		ReviewFocus []string `json:"review_focus"`
	} `json:"run"`
	Groups []struct {
		ID          string   `json:"id"`
		Risk        Risk     `json:"risk"`
		Rationale   string   `json:"rationale"`
		ReviewFocus []string `json:"review_focus"`
	} `json:"groups"`
}

// Interpret validates a response against what was actually sent and
// builds the assessment. Validation is local and unconditional: a
// provider's schema enforcement is a latency optimisation, never a trust
// boundary, so this runs identically whether or not the request asked for
// structured output.
//
// Every problem is a diagnostic rather than a failure. An unparseable
// response returns an error-severity diagnostic and an empty assessment,
// which is what the caller retries on; everything else degrades a field
// and carries on.
func Interpret(raw []byte, planned []PlannedGroup, p Pseudonyms) (Assessment, []Diagnostic) {
	var diags []Diagnostic
	fail := func(format string, args ...any) (Assessment, []Diagnostic) {
		return Assessment{AISchemaVersion: AISchemaVersion},
			append(diags, Diagnostic{Severity: SeverityError, Message: fmt.Sprintf(format, args...)})
	}

	var doc *responseDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fail("inference service returned a response that is not the requested JSON: %v", err)
	}
	if doc == nil {
		return fail("inference service returned an empty response")
	}

	a := Assessment{AISchemaVersion: AISchemaVersion}

	a.Run.Risk, diags = validRisk(doc.Run.Risk, "run", diags)
	a.Run.Summary = p.Reveal(doc.Run.Summary)
	a.Run.ReviewFocus = p.revealAll(doc.Run.ReviewFocus)

	// Index the response by id so a group sent but not answered can be
	// distinguished from one answered but never sent.
	answered := make(map[string]int, len(doc.Groups))
	sent := make(map[string]bool, len(planned))
	for _, g := range planned {
		sent[g.ID] = true
	}
	for i, g := range doc.Groups {
		if !sent[g.ID] {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("inference service referenced group %q, which was not sent; dropped", g.ID),
			})
			continue
		}
		answered[g.ID] = i
	}

	for _, planned := range planned {
		ga := GroupAssessment{
			ID:        planned.ID,
			Kind:      string(planned.Key.Kind),
			Identity:  planned.Identity,
			Parameter: planned.Key.Parameter,
			Certnames: planned.Certnames,
			Risk:      RiskUnknown,
		}
		if i, ok := answered[planned.ID]; ok {
			got := doc.Groups[i]
			ga.Risk, diags = validRisk(got.Risk, "group "+planned.ID, diags)
			ga.Rationale = p.Reveal(got.Rationale)
			ga.ReviewFocus = p.revealAll(got.ReviewFocus)
		} else {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("inference service returned no assessment for group %q (%s); recorded as unknown", planned.ID, planned.Identity),
			})
		}
		a.Groups = append(a.Groups, ga)
	}

	return a, diags
}

// validRisk accepts one of the four risk indications and turns anything
// else into RiskUnknown plus a diagnostic, so free prose from a model can
// never reach a renderer through the risk field.
func validRisk(got Risk, where string, diags []Diagnostic) (Risk, []Diagnostic) {
	if got.Valid() {
		return got, diags
	}
	return RiskUnknown, append(diags, Diagnostic{
		Severity: SeverityWarning,
		Message:  fmt.Sprintf("inference service returned an unrecognised risk indication %q for %s; recorded as unknown", string(got), where),
	})
}
