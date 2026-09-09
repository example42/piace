package normalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

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

// unorderedMetaparameters names the parameters whose array values carry
// no order, so a difference in their order is not a difference in what
// Puppet will do. The list is Puppet's own: the PuppetDB terminus
// declares it as UnorderedMetaparams, "metaparams that may contain
// arrays, but whose semantics are fundamentally unordered", and sorts
// each of them before storing a catalog. A compiler's catalog response
// is not sorted, so comparing a PuppetDB baseline against a compiled
// candidate reports the same relationship set as a change whenever the
// manifest declared it in an order other than the sorted one.
//
// `alias` is on Puppet's list too and is dropped entirely above, so it
// never reaches here.
//
// See doc.go for the measurement and the primary source.
var unorderedMetaparameters = map[string]bool{
	"audit":     true,
	"before":    true,
	"check":     true,
	"notify":    true,
	"require":   true,
	"subscribe": true,
	"tag":       true,
}

// sortUnorderedMetaparameter returns v with its elements in this
// package's own total order when v is a slice, and v unchanged
// otherwise.
//
// The order deliberately does not try to reproduce Ruby's
// `sort_by {|x| x.to_s}`. It does not have to: the same order is
// imposed on both sides of every comparison, and any permutation of one
// multiset sorts to the same sequence, so two catalogs holding the same
// relationships compare equal whatever that order is. Reproducing
// Ruby's would only matter if PIACE had to agree with a third party
// about the sequence, and nothing does.
//
// Ordering by the canonical JSON encoding keeps that property for
// element types the sorted-strings case does not cover. Puppet
// stringifies a resource reference on both wire paths, so in practice
// every element here is a string.
func sortUnorderedMetaparameter(v model.Value) model.Value {
	items, ok := v.([]model.Value)
	if !ok || len(items) < 2 {
		return v
	}
	keys := make([]string, len(items))
	for i, item := range items {
		if s, ok := item.(string); ok {
			// Prefixed so a string can never sort into the middle of
			// the encoded values, which would make the order depend on
			// whether an element happened to be a string.
			keys[i] = "s" + s
			continue
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			// Unreachable: every model.Value is JSON-encodable by
			// construction (canonicalizeValue's domain). Leaving the
			// order alone is the safe answer if it ever is not.
			return v
		}
		keys[i] = "j" + string(encoded)
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return keys[order[i]] < keys[order[j]] })
	sorted := make([]model.Value, len(items))
	for i, from := range order {
		sorted[i] = items[from]
	}
	return sorted
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
		if unorderedMetaparameters[k] {
			cv = sortUnorderedMetaparameter(cv)
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
		if val["__ptype"] == "Sensitive" {
			if _, ok := val["__pvalue"]; !ok {
				return nil, fmt.Errorf("malformed Sensitive wrapper")
			}
		}
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
