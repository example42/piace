package report

import (
	"encoding/json"
	"fmt"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

// invocationKey is the result document's invocation-metadata member.
// SemanticProjection removes exactly this one key, at the top level
// only.
const invocationKey = "invocation"

// SemanticProjection returns the canonical bytes of everything in r that
// describes the comparison, with the invocation metadata removed.
//
// Two claims about determinism are made in this project, and they are
// not the same claim.
//
// The first is byte equality: the same complete result, invocation
// metadata included, encodes to the same bytes every time. That is what
// JSON's canonical encoding buys, and it holds for a re-render, for a
// stored document read back, and for two runs whose recorded metadata
// happens to be identical.
//
// The second is semantic reproducibility: two runs over the same
// catalogs and the same configuration reach the same comparison. That
// one cannot be stated as byte equality of the whole document, because
// a real run stamps a real timestamp, and two runs are never at the same
// instant. Until 2026-09-10 the project claimed byte equality for it
// anyway, and the acceptance suite appeared to demonstrate that by
// freezing the clock, which is not a property production has.
//
// This is the projection the second claim is about. It is also what a
// reader can compute themselves from two stored documents, with
// `jq -S 'del(.invocation)'` or equivalent, so the guarantee is
// checkable outside this codebase rather than only by its own tests.
//
// The whole invocation block goes, not only its timestamp. Tool version
// and configured service authorities describe the run rather than the
// comparison: an upgraded binary reaching the same conclusion about the
// same catalogs has reproduced the comparison, and saying so is the
// point of separating the two.
func SemanticProjection(r model.Result) ([]byte, error) {
	encoded, err := JSON(r)
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decoding the encoded result document: %w", err)
	}
	delete(document, invocationKey)

	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encoding the semantic projection: %w", err)
	}
	// Canonicalized again rather than reassembled by hand: the members
	// kept are already canonical, but their container is a fresh map,
	// and one canonicalization step is what makes key order and number
	// spelling the encoder's business rather than this function's.
	canonical, err := snapshot.CanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return nil, fmt.Errorf("canonicalizing the semantic projection: %w", err)
	}
	return append(canonical, '\n'), nil
}
