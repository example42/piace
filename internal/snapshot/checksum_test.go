package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestChecksum_ValidChecksumAccepted verifies a checksum computed from a
// payload matches a re-computed checksum from the same logical payload
// spelled differently (key order, numeric spelling) — i.e. Checksum is
// stable under exactly the cosmetic variation CanonicalJSON normalizes
// away.
func TestChecksum_ValidChecksumAccepted(t *testing.T) {
	a := json.RawMessage(`{"certname": "web-01", "count": 1.50}`)
	b := json.RawMessage(`{"count": 1.5, "certname": "web-01"}`)

	sumA, err := Checksum(a)
	if err != nil {
		t.Fatalf("Checksum(a): %v", err)
	}
	sumB, err := Checksum(b)
	if err != nil {
		t.Fatalf("Checksum(b): %v", err)
	}
	if sumA != sumB {
		t.Errorf("Checksum(a) = %s, Checksum(b) = %s, want equal", sumA, sumB)
	}
	if !strings.HasPrefix(sumA, "sha256:") {
		t.Errorf("Checksum = %q, want sha256:<hex> prefix", sumA)
	}
}

// TestChecksum_TamperedPayloadRejected verifies a semantically different
// payload produces a different checksum.
func TestChecksum_TamperedPayloadRejected(t *testing.T) {
	original := json.RawMessage(`{"certname": "web-01", "count": 1}`)
	tampered := json.RawMessage(`{"certname": "web-01", "count": 2}`)

	sumOriginal, err := Checksum(original)
	if err != nil {
		t.Fatalf("Checksum(original): %v", err)
	}
	sumTampered, err := Checksum(tampered)
	if err != nil {
		t.Fatalf("Checksum(tampered): %v", err)
	}
	if sumOriginal == sumTampered {
		t.Errorf("expected different checksums for different payloads, both = %s", sumOriginal)
	}
}

// TestChecksum_MalformedPayloadErrors verifies a payload that isn't valid
// JSON returns an error rather than a bogus checksum.
func TestChecksum_MalformedPayloadErrors(t *testing.T) {
	if _, err := Checksum(json.RawMessage(`{not valid`)); err == nil {
		t.Error("expected an error for malformed payload, got nil")
	}
}
