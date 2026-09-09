package diff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

// fingerprintResourceChange hashes raw evidence before publishChanges.
// File changes include both content-bearing parameter maps and resolved
// evidence so distinct unresolved changes cannot collapse into one group.
func fingerprintResourceChange(change rawResourceChange) (string, error) {
	evidence := map[string]model.Value{
		"kind":      string(change.Kind),
		"type":      change.Identity.Type,
		"title":     change.Identity.Title,
		"parameter": change.Parameter,
	}
	switch {
	case change.FileContent != nil:
		fc := change.FileContent
		evidence["before"] = fingerprintable(change.Before)
		evidence["after"] = fingerprintable(change.After)
		evidence["file_content"] = map[string]model.Value{
			"state":           string(fc.State),
			"evidence_source": string(fc.EvidenceSource),
			"algorithm":       fc.Algorithm,
			"before_digest":   fc.BeforeDigest,
			"after_digest":    fc.AfterDigest,
		}
	default:
		// An absent parameter and one explicitly present with undef are the same
		// Go nil in the model.Value domain, since internal/normalize decodes
		// JSON null to nil, so no encoding at this layer could distinguish them.
		// diffParameters never emits such a pair as a change in the first place.
		evidence["before"] = fingerprintable(change.Before)
		evidence["after"] = fingerprintable(change.After)
	}

	encoded, err := snapshot.CanonicalJSON(evidence)
	if err != nil {
		return "", fmt.Errorf("fingerprinting %s: %w", change.Identity.String(), err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// fingerprintable converts a model.Value tree into the exact input
// domain snapshot.CanonicalJSON's encoder accepts.
//
// model.Value is a type alias for `any`, so []model.Value and
// map[string]model.Value are already []any and map[string]any and need
// no conversion. Only model.Number, a defined type over string that the
// encoder's type switch does not recognize and would reject as an
// unsupported type, has to be converted, and it is rewritten to a
// json.Number carrying the same digits. That reuses snapshot's single
// canonicalization algorithm rather than introducing a second one, and
// is exactly idempotent: model.Number values are produced by
// internal/normalize via snapshot.CanonicalNumberString, so
// re-canonicalizing their digits yields the same string.
func fingerprintable(v model.Value) model.Value {
	switch val := v.(type) {
	case model.Number:
		return json.Number(string(val))
	case []model.Value:
		out := make([]model.Value, len(val))
		for i, e := range val {
			out[i] = fingerprintable(e)
		}
		return out
	case map[string]model.Value:
		out := make(map[string]model.Value, len(val))
		for k, e := range val {
			out[k] = fingerprintable(e)
		}
		return out
	default:
		return v
	}
}
