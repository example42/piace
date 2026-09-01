package normalize

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

// generatedMetadataParameters names the resource parameters this package
// drops as generated catalog metadata rather than managed
// configuration. Generated and noise-oriented fields are excluded from
// the semantic diff: tags, source file and line information, and catalog
// metadata unrelated to managed file content. See doc.go's
// "What is dropped, and why that is safe" section for the full rationale
// and the measurements behind it.
var generatedMetadataParameters = map[string]bool{
	"alias": true,
}

// isGeneratedMetadataParameter reports whether name is a parameter
// decodeParameters drops on both sides of a comparison.
func isGeneratedMetadataParameter(name string) bool {
	return generatedMetadataParameters[name]
}

// decodeParameters decodes a resource's raw "parameters" JSON object
// into the model.Value domain, canonicalizing every numeric value with
// snapshot.CanonicalNumberString along the way, and dropping every
// parameter isGeneratedMetadataParameter names. A missing or empty
// "parameters" field decodes to an empty (nil) parameter map rather than
// an error. PuppetDB's documented catalog wire format v8 states "Puppet
// will only provide Booleans, strings, arrays, and hashes... Attributes
// with undef values are not added to the catalog", so a resource with no
// parameters at all is a legitimate, if unusual, input, and both
// documented wire shapes always declare "parameters" at the wire level
// even when a Puppet manifest sets none. This package treats an entirely
// absent field the same as an empty object rather than rejecting it: the
// concern about unknown or unparseable required shapes is about a value
// that cannot be decoded at all, not about an empty-but-present or
// absent optional collection.
func decodeParameters(raw json.RawMessage) (map[string]model.Value, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var decoded map[string]json.RawMessage
	// Decode into a map[string]json.RawMessage first (via UseNumber on a
	// fresh decoder per raw value below) so top-level key order plays no
	// role; canonicalizeValue below re-decodes each individual value with
	// its own UseNumber decoder to preserve numeric precision throughout
	// nested arrays/objects too.
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return nil, fmt.Errorf("decoding parameters object: %w", err)
	}
	out := make(map[string]model.Value, len(decoded))
	for k, v := range decoded {
		if isGeneratedMetadataParameter(k) {
			continue
		}
		cv, err := canonicalizeRaw(v)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", k, err)
		}
		out[k] = cv
	}
	return out, nil
}

// canonicalizeRaw decodes one JSON value (preserving exact numeric
// precision via json.Decoder.UseNumber()) and converts it into the
// model.Value domain via canonicalizeValue.
func canonicalizeRaw(raw json.RawMessage) (model.Value, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decoding JSON value: %w", err)
	}
	return canonicalizeValue(v)
}

// canonicalizeValue converts a generic decoded JSON value (as produced by
// a json.Decoder with UseNumber() enabled) into the model.Value domain:
// nil, bool, string, model.Number (exact decimal digits, never a machine
// float64), []model.Value, or map[string]model.Value. See doc.go for why
// this reuses snapshot.CanonicalNumberString rather than a second
// canonicalization algorithm, and why object keys are not separately
// pre-sorted here.
func canonicalizeValue(v any) (model.Value, error) {
	switch val := v.(type) {
	case nil:
		return nil, nil
	case bool:
		return val, nil
	case string:
		return val, nil
	case json.Number:
		s, err := snapshot.CanonicalNumberString(string(val))
		if err != nil {
			return nil, fmt.Errorf("canonicalizing number %q: %w", string(val), err)
		}
		return model.Number(s), nil
	case []any:
		out := make([]model.Value, len(val))
		for i, e := range val {
			cv, err := canonicalizeValue(e)
			if err != nil {
				return nil, fmt.Errorf("index %d: %w", i, err)
			}
			out[i] = cv
		}
		return out, nil
	case map[string]any:
		out := make(map[string]model.Value, len(val))
		for k, e := range val {
			cv, err := canonicalizeValue(e)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			out[k] = cv
		}
		return out, nil
	default:
		// Unreachable from any value produced by encoding/json's decoder (see
		// doc.go); guarded defensively, since "catalog data outside this
		// JSON-compatible value domain is rejected as a normalization error."
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}
