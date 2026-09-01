package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/example42/piace/internal/model"
)

// DecodeJSON reads a result document rendered by JSON back into a
// model.Result. It is the inverse of JSON and lives beside it so the two
// cannot drift.
//
// It decodes with json.Decoder.UseNumber(), never plain json.Unmarshal,
// for the same reason internal/snapshot's decodeAny does: model.Value is
// an alias for `any`, so every parameter value in a node diff or an
// aggregate group decodes into an interface. Plain decoding puts a
// float64 there and silently destroys the exact decimal digits the
// canonical encoder went to some trouble to preserve: 2^53+1 comes back
// as 2^53, and two decimals that differ beyond float64's precision come
// back *equal*, turning a real parameter change into a non-change. With
// UseNumber each numeric token arrives as a json.Number holding its
// digits, which snapshot.CanonicalJSON already accepts and normalizes
// exactly on the way back out.
//
// Reading is strict in two further ways, both matching precedent
// elsewhere in the tree rather than taking encoding/json's defaults.
//
// Unknown fields are rejected, as internal/config rejects them in a
// target or services file. The consequence is a rule worth stating
// plainly: any field added to the result document increments
// model.ResultSchemaVersion. Lenient decoding would otherwise leave a
// silent middle ground, where a report from a newer PIACE carrying the
// *same* schema_version but additional fields would decode into a
// partial Result this binary then reasoned over as if it were complete,
// which is exactly the failure the version check cannot catch.
//
// Content after the first JSON value is rejected, as
// internal/snapshot's decodeAny rejects it, so a truncated file with a
// second document concatenated onto it cannot be read as the first one.
// Trailing whitespace is not content: JSON appends a newline so the
// artifact is a well-formed text file.
//
// A decoded Result carries no model.ResourceChange.Fingerprint: it is
// `json:"-"` and never enters a report by design. A consumer of a stored
// result document therefore reads the aggregate groups the run already
// built and must not attempt to re-derive them.
func DecodeJSON(data []byte) (model.Result, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()

	var r model.Result
	if err := dec.Decode(&r); err != nil {
		return model.Result{}, fmt.Errorf("decoding result document: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return model.Result{}, fmt.Errorf("decoding result document: unexpected content after the document")
	}
	return r, nil
}
