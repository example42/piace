package assess

import (
	"context"
	"fmt"

	"github.com/example42/piace/internal/inference"
	"github.com/example42/piace/internal/limits"
	"github.com/example42/piace/internal/model"
)

// Completer is the inference service as this package needs it. It is an
// interface so the whole assessment flow is testable without a network,
// and so internal/assess depends on the idea of an inference service
// rather than on one client.
type Completer interface {
	Complete(context.Context, inference.Request) ([]byte, error)
}

// Meta is the provenance stamped onto a change assessment: when it was
// produced, by which model, from which endpoint, and, the one that ties
// it to a specific comparison, the checksum of the result document it
// was derived from.
type Meta struct {
	GeneratedAt          string
	ModelID              string
	EndpointAuthority    string
	SourceReportChecksum string
}

// Produce builds a change assessment from a stored result document.
//
// It always returns a usable artifact. A failed or unusable inference
// response degrades every risk indication to unknown and records why;
// it never returns a partial assessment with groups silently missing,
// because a reader scanning a list of groups has no way to tell an
// omission from a judgement. Nothing here can change a comparison
// outcome or exit status: the assessment is advisory, and the deterministic
// result document it was built from is unaffected by anything an
// inference service says.
func Produce(ctx context.Context, c Completer, r model.Result, cc ChangeContext, cfg Config, meta Meta) (Assessment, []Diagnostic) {
	planned, total, truncated := PlanGroups(r, cfg.MaxGroups)
	degraded := unknownAssessment(planned)
	// Filled in once the request is built: it may shed evidence to fit
	// its budget, and the artifact reports what was sent.
	var valuesOmitted int

	stamp := func(a Assessment) Assessment {
		a.AISchemaVersion = AISchemaVersion
		a.GeneratedAt = meta.GeneratedAt
		a.ModelID = meta.ModelID
		a.EndpointAuthority = meta.EndpointAuthority
		a.SourceReportChecksum = meta.SourceReportChecksum
		a.SourceReportOutcome = string(r.Outcome)
		a.GroupsTotal = total
		a.GroupsAssessed = len(planned)
		a.GroupsTruncated = truncated
		a.ValuesOmitted = valuesOmitted
		a.InputPartial = inputIsPartial(r)
		if cc.Present {
			ctxCopy := cc
			a.ChangeContext = &ctxCopy
		}
		return a
	}

	req, p, scope, err := BuildRequest(r, cc, cfg)
	if err != nil {
		return stamp(degraded), []Diagnostic{{
			Severity: SeverityError,
			Message:  fmt.Sprintf("building the inference request: %v", err),
		}}
	}
	// The request may have shed evidence to fit its budget, so the
	// artifact records what was actually sent rather than what was
	// planned: an assessment that reviewed fewer groups, or groups
	// without their values, is a narrower review and says so.
	truncated = truncated || scope.GroupsTruncated
	// Re-slicing by count alone is only correct because shedding drops
	// whole groups from the tail of the ranked plan, so what survives is
	// a prefix. Interpret resolves the ids a service returns against
	// this slice; if shedding ever dropped from the middle, an id would
	// silently resolve to a different group's identity.
	planned = planned[:scope.GroupsAssessed]
	valuesOmitted = scope.ValuesOmitted
	degraded = unknownAssessment(planned)

	raw, err := c.Complete(ctx, req)
	if err != nil {
		// A transport or status failure is not retried here: the
		// inference client already bounds one attempt, and a change
		// assessment gates nothing, so a second round trip buys a reader
		// nothing they cannot get by running `piace explain` again.
		return stamp(degraded), []Diagnostic{{
			Severity: SeverityError,
			Message:  fmt.Sprintf("requesting a change assessment: %v", err),
		}}
	}

	a, diags := Interpret(raw, planned, p)
	if !HasErrorDiagnostic(diags) {
		return stamp(a), diags
	}

	// One retry, carrying what was wrong with the first reply. Exactly
	// one: an inference service that cannot produce the requested shape
	// twice is not going to on a third attempt, and a backoff ladder
	// would turn an advisory feature into a slow one.
	first := diags
	// The repair message carries the service's own failure text back to
	// it, so it is capped: an inference service that answered with a
	// megabyte of prose must not have that megabyte quoted back in a
	// second request.
	reason, _ := capString(firstErrorMessage(first), maxRetryReasonBytes)
	retry := req
	retry.Messages = append(append([]inference.Message(nil), req.Messages...), inference.Message{
		Role: "user",
		Content: "Your previous reply could not be used: " + reason +
			"\n\nReply again with JSON matching the requested schema, and nothing else. No prose, no code fence.",
	})
	// A retry is a second disclosure of the same comparison, and a larger
	// request than the first. BuildRequest held RetryAllowance back for
	// exactly this message; if the assembled retry is over budget anyway,
	// it is not sent.
	if size, err := RequestSize(retry); err != nil || size > limits.InferenceRequest {
		return stamp(degraded), append(first, Diagnostic{
			Severity: SeverityError,
			Message:  "the change assessment could not be retried within the inference request size budget",
		})
	}

	raw, err = c.Complete(ctx, retry)
	if err != nil {
		return stamp(degraded), append(downgrade(first), Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("retrying a change assessment: %v", err),
		})
	}

	a, retryDiags := Interpret(raw, planned, p)
	if HasErrorDiagnostic(retryDiags) {
		return stamp(degraded), append(downgrade(first), retryDiags...)
	}
	// The first attempt is kept as a warning: it explains why the run
	// took two round trips, without claiming the assessment failed.
	return stamp(a), append(downgrade(first), retryDiags...)
}

// unknownAssessment records every group that was sent as unknown, so a
// degraded artifact is complete rather than empty.
func unknownAssessment(planned []PlannedGroup) Assessment {
	a := Assessment{Run: RunAssessment{Risk: RiskUnknown}}
	for _, g := range planned {
		a.Groups = append(a.Groups, GroupAssessment{
			ID:        g.ID,
			Kind:      string(g.Key.Kind),
			Identity:  g.Identity,
			Parameter: g.Key.Parameter,
			Certnames: g.Certnames,
			Risk:      RiskUnknown,
		})
	}
	return a
}

// inputIsPartial reports whether the result document itself was
// incomplete. An assessment built on one must say so: a reader who sees
// only risk indications has no way to know a target never compiled.
//
// Only an error-severity diagnostic makes a document partial. A warning
// does not: model.OutcomeForDiagnostic is explicit that a warning
// contributes nothing to the outcome, and a complete run emits them
// routinely, a v3 compatibility notice or a directory content source
// among them. Keying on the presence of any diagnostic would tell the
// reader of a successful comparison that their input was incomplete.
func inputIsPartial(r model.Result) bool {
	if hasResultError(r.Diagnostics) {
		return true
	}
	for _, t := range r.Targets {
		if hasResultError(t.Diagnostics) {
			return true
		}
	}
	return false
}

// hasResultError reports whether any of the result document's own
// diagnostics is an error. It is the one place this package interprets
// model severities, so "a warning is not a failure" is decided once.
func hasResultError(diags []model.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			return true
		}
	}
	return false
}

// HasErrorDiagnostic reports whether an assessment failed outright, as
// opposed to having degraded a field. It filters on severity rather than
// on the presence of any diagnostic: a run whose first reply was unusable
// and whose retry succeeded carries the first attempt as a warning, and
// that run produced a usable assessment.
func HasErrorDiagnostic(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

func firstErrorMessage(diags []Diagnostic) string {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return d.Message
		}
	}
	return "the reply did not match the requested schema"
}

// downgrade turns a superseded attempt's errors into warnings. A retry
// that succeeded is a successful run that took two round trips, not a
// failed one, and an error-severity diagnostic left behind would say
// otherwise to every reader and every renderer.
func downgrade(diags []Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d.Severity == SeverityError {
			d.Severity = SeverityWarning
			d.Message = "first attempt: " + d.Message
		}
		out = append(out, d)
	}
	return out
}
