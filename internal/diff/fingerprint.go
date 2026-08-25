package diff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

// fingerprintResourceChange computes the stable, equality-preserving
// digest documented on model.ResourceChange.Fingerprint, over a change's
// *unredacted* canonical evidence. It must be called during pass 1,
// before redactChanges runs.
//
// The digested tuple deliberately includes kind, identity, and parameter
// name in addition to the before/after evidence: task 10 groups on
// (kind, identity, parameter) already, but including them here means two
// changes with different keys can never collide on Fingerprint alone, so
// a consumer may treat the Fingerprint as the complete group token
// without re-deriving the key.
//
// For a File-content change, the evidence is the pre-redaction
// FileContentEvidence (state, evidence source, algorithm, and both
// digests) rather than Before/After, which that entry deliberately
// leaves unset (see resources.go). Without this, every redacted File
// content change on the same path would collapse into a single
// aggregate group regardless of whether the underlying content actually
// matched — exactly the "merging distinct sensitive changes in aggregate
// groups" design.md section 7.3 forbids.
func fingerprintResourceChange(change model.ResourceChange) (string, error) {
	evidence := map[string]model.Value{
		"kind":      string(change.Kind),
		"type":      change.Identity.Type,
		"title":     change.Identity.Title,
		"parameter": change.Parameter,
	}
	switch {
	case change.FileContent != nil:
		fc := change.FileContent
		evidence["file_content"] = map[string]model.Value{
			"state":           string(fc.State),
			"evidence_source": string(fc.EvidenceSource),
			"algorithm":       fc.Algorithm,
			"before_digest":   fc.BeforeDigest,
			"after_digest":    fc.AfterDigest,
		}
	default:
		// An absent parameter and one explicitly present with undef are
		// the same Go nil in the model.Value domain (internal/normalize
		// decodes JSON null to nil), so no encoding at this layer could
		// distinguish them — and diffParameters never emits such a pair
		// as a change in the first place.
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
// no conversion; only model.Number — a defined type over string, which
// the encoder's type switch does not recognize and would reject as an
// unsupported type — has to be converted. It is rewritten to a
// json.Number carrying the same digits. That reuses snapshot's single
// canonicalization algorithm rather than introducing a second one (per
// design.md's Property 1), and is exactly idempotent: model.Number
// values are produced by internal/normalize via
// snapshot.CanonicalNumberString, so re-canonicalizing their digits
// yields the same string.
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
