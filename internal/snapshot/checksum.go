package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Checksum computes the SHA-256 payload integrity checksum: SHA-256 over
// the compact canonical JSON encoding of only payload. payload is
// decoded with number precision preserved (see decodeAny) before being
// canonically re-encoded, so the checksum is stable across cosmetic
// re-serialization (key order, numeric spelling, insignificant
// whitespace) but sensitive to any actual value change.
//
// The returned string is "sha256:<hex>", matching the payload_checksum
// field's documented format.
func Checksum(payload json.RawMessage) (string, error) {
	decoded, err := decodeAny(payload)
	if err != nil {
		return "", fmt.Errorf("snapshot: computing checksum: %w", err)
	}
	canon, err := CanonicalJSON(decoded)
	if err != nil {
		return "", fmt.Errorf("snapshot: computing checksum: %w", err)
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
