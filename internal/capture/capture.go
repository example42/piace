package capture

import (
	"time"

	"github.com/example42/piace/internal/model"
)

// TargetOutcome is one target's result from a capture run: either a
// snapshot was written (Path non-empty), the target was skipped because
// its configuration does not select a file-backed destination for this
// capture command (see doc.go), or Diagnostic records why it failed.
// Exactly one of {Path non-empty, Skipped, Diagnostic non-nil} holds for
// a given TargetOutcome.
type TargetOutcome struct {
	Certname   string
	Path       string
	Skipped    bool
	Diagnostic *model.Diagnostic
}

// Failed reports whether this outcome represents a reported failure (as
// opposed to a success or an intentional skip).
func (o TargetOutcome) Failed() bool { return o.Diagnostic != nil }

// nowUTCRFC3339 is the single clock used to stamp captured_at, injectable
// by tests via the Workflow.Now field (see workflow.go) so tests do not
// depend on wall-clock time.
func nowUTCRFC3339(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return now().UTC().Format(time.RFC3339)
}
