package report

import (
	"encoding/json"
	"fmt"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

// JSON renders r as the versioned JSON report, encoded with the in-tree
// canonical encoder.
//
// Encoding runs in two steps, encoding/json to bytes and then
// snapshot.CanonicalJSON over those bytes, because the canonical encoder
// accepts only generic JSON shapes, not Go structs. The round trip is
// what makes determinism hold end to end: struct field order, map
// insertion order, and how a number was originally spelled all
// disappear, so identical inputs produce byte-identical output. The
// visible cost is that object keys come out in lexicographic rather than
// declaration order. Determinism is the property worth having, and
// schema_version keys the parsing rather than field position.
//
// A trailing newline is appended so the artifact is a well-formed text
// file for CI tooling; it is outside the JSON value and does not affect
// parsing.
func JSON(r model.Result) ([]byte, error) {
	// The result has to be labelled a **Potential impact estimate**; the
	// JSON report is one of the three formats that obligation covers, and a
	// bare `impact_estimates` key carries no such label. The label and note
	// are stamped onto a copy here rather than upstream so the pipeline
	// stays free of prose (see model.Result's field comments and
	// internal/impact's doc.go).
	if len(r.ImpactEstimates) > 0 {
		r.ImpactEstimateLabel = ImpactEstimateLabel
		r.ImpactEstimateNote = ImpactEstimateNote
	}

	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encoding result document: %w", err)
	}
	canonical, err := snapshot.CanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return nil, fmt.Errorf("canonicalizing result document: %w", err)
	}
	return append(canonical, '\n'), nil
}
