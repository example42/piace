package assess

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/example42/piace/internal/snapshot"
)

// JSON encodes a change assessment as the artifact `piace explain`
// writes, canonically encoded with the in-tree encoder and newline
// terminated, the same way internal/report encodes a result document.
//
// Canonical encoding does not make an assessment reproducible, since a
// provider-side model revision changes what it says, and that is the
// whole reason the assessment is a separate artifact rather than part of
// the result document. It does mean that re-encoding an unchanged
// assessment produces unchanged bytes, so a diff between two artifacts
// shows what the model said differently and nothing else.
func JSON(a Assessment) ([]byte, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("encoding change assessment: %w", err)
	}
	canonical, err := snapshot.CanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return nil, fmt.Errorf("canonicalizing change assessment: %w", err)
	}
	return append(canonical, '\n'), nil
}

// WriteArtifact writes the change assessment to path.
//
// It is written 0644, like a report and unlike a snapshot envelope: it
// carries no credential and no managed file content, and CI has to be
// able to publish it. It does carry real certnames, since pseudonyms
// exist only in an inference request body, so it belongs wherever the
// JSON and HTML reports already go and nowhere less protected than that.
func WriteArtifact(path string, a Assessment) error {
	data, err := JSON(a)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing change assessment: %w", err)
	}
	return nil
}
