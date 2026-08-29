package report

import (
	"bytes"
	"testing"

	"github.com/example42/piace/internal/model"
)

// numericResult is sampleResult() with numeric parameter values whose
// exact decimal digits exceed what a float64 can represent: 2^53+1, which
// float64 rounds to 2^53, and a decimal with more significant digits than
// float64 carries. A round trip that decodes numbers as float64 loses
// both, so this fixture is what distinguishes a lossless reader from one
// that merely looks lossless on small integers.
func numericResult() model.Result {
	r := sampleResult()
	r.Targets[0].NodeDiff.ResourceChanges = append(
		r.Targets[0].NodeDiff.ResourceChanges,
		model.ResourceChange{
			Kind:      model.ChangeParameterChanged,
			Identity:  model.ResourceIdentity{Type: "Exec", Title: "tune"},
			Parameter: "timeout",
			Before:    model.Number("9007199254740993"),
			After:     model.Number("9007199254740995"),
		},
		model.ResourceChange{
			Kind:      model.ChangeParameterChanged,
			Identity:  model.ResourceIdentity{Type: "Exec", Title: "tune"},
			Parameter: "ratio",
			Before:    model.Number("0.1234567890123456789"),
			After:     model.Number("0.1234567890123456788"),
		},
	)
	return r
}

// TestResultDocumentRoundTripsThroughItsJSONReport is the assumption every
// later use of a stored result document rests on: reading a JSON report
// back and re-rendering it reproduces the report byte for byte. Nothing in
// v0.1.0 ever read a report back, so nothing established it.
//
// See docs/plans/v0.2.0-change-assessment.md, increment 0.
func TestResultDocumentRoundTripsThroughItsJSONReport(t *testing.T) {
	first, err := JSON(numericResult())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	decoded, err := DecodeJSON(first)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}

	second, err := JSON(decoded)
	if err != nil {
		t.Fatalf("JSON after DecodeJSON: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("re-rendering a decoded result document changed it\n first:  %s\n second: %s", first, second)
	}
}

// TestRenderedReportsAreUnchangedByADecodedResultDocument is the claim
// `piace explain` rests on: re-rendering from a stored JSON report
// reproduces what the run that wrote it rendered. Without it, an
// assessment section could only be added by re-running a comparison.
//
// See docs/plans/v0.2.0-change-assessment.md, increment 0.
func TestRenderedReportsAreUnchangedByADecodedResultDocument(t *testing.T) {
	original := numericResult()

	stored, err := JSON(original)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	decoded, err := DecodeJSON(stored)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}

	t.Run("html", func(t *testing.T) {
		before, err := HTML(original, nil)
		if err != nil {
			t.Fatalf("HTML(original): %v", err)
		}
		after, err := HTML(decoded, nil)
		if err != nil {
			t.Fatalf("HTML(decoded, nil): %v", err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("HTML report differs after a JSON round trip (%d vs %d bytes)", len(before), len(after))
		}
	})

	t.Run("text", func(t *testing.T) {
		for _, opts := range []Options{{}, {ImpactNodes: true}} {
			before, err := Text(original, nil, opts)
			if err != nil {
				t.Fatalf("Text(original, %+v): %v", opts, err)
			}
			after, err := Text(decoded, nil, opts)
			if err != nil {
				t.Fatalf("Text(decoded, %+v): %v", opts, err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("text report differs after a JSON round trip with %+v\n before: %s\n after:  %s", opts, before, after)
			}
		}
	})
}

// TestDecodeJSONReadsAStoredResultDocumentStrictly fixes what a reader
// accepts. A result document is exactly one JSON value whose fields this
// binary knows; anything else is refused rather than silently read as a
// partial or truncated document.
func TestDecodeJSONReadsAStoredResultDocumentStrictly(t *testing.T) {
	valid, err := JSON(numericResult())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	t.Run("accepts the rendered report, trailing newline included", func(t *testing.T) {
		if _, err := DecodeJSON(valid); err != nil {
			t.Fatalf("DecodeJSON: %v", err)
		}
	})

	t.Run("rejects content after the document", func(t *testing.T) {
		appended := append(append([]byte{}, valid...), []byte("{\"schema_version\":1}\n")...)
		if _, err := DecodeJSON(appended); err == nil {
			t.Error("DecodeJSON accepted a second document appended to the first")
		}
	})

	t.Run("rejects a field this binary does not know", func(t *testing.T) {
		extra := append([]byte(`{"unknown_future_field":true,`), valid[1:]...)
		if _, err := DecodeJSON(extra); err == nil {
			t.Error("DecodeJSON accepted an unknown top-level field")
		}
	})

	t.Run("rejects a field this binary does not know inside a target", func(t *testing.T) {
		extra := bytes.Replace(valid,
			[]byte(`{"baseline":{`), []byte(`{"unknown_future_field":true,"baseline":{`), 1)
		if bytes.Equal(extra, valid) {
			t.Fatal("fixture did not contain the expected target shape")
		}
		if _, err := DecodeJSON(extra); err == nil {
			t.Error("DecodeJSON accepted an unknown field inside a target")
		}
	})
}
