package snapshot

import (
	"encoding/json"
	"testing"
)

// TestEnvelope_JSONRoundTrip_Catalog verifies a catalog snapshot
// envelope (with its mandatory requested_environment/capture/
// input_factset_identity fields) marshals and unmarshals via
// encoding/json without field loss.
func TestEnvelope_JSONRoundTrip_Catalog(t *testing.T) {
	original := Envelope{
		FormatVersion:        FormatVersion,
		Kind:                 KindCatalog,
		Target:               "web-01.example.test",
		Source:               Source{Kind: "compiler", Producer: "puppetserver-8.5.0"},
		CapturedAt:           "2026-08-24T00:00:00Z",
		RequestedEnvironment: "production",
		Capture:              validCaptureProvenance(),
		InputFactsetIdentity: "sha256:deadbeef",
		PayloadChecksum:      "sha256:cafebabe",
		Payload:              json.RawMessage(`{"resources":[]}`),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.FormatVersion != FormatVersion {
		t.Errorf("FormatVersion = %d, want %d", decoded.FormatVersion, FormatVersion)
	}
	if decoded.Kind != KindCatalog {
		t.Errorf("Kind = %q, want %q", decoded.Kind, KindCatalog)
	}
	if decoded.RequestedEnvironment != "production" {
		t.Errorf("RequestedEnvironment = %q", decoded.RequestedEnvironment)
	}
	if decoded.Capture == nil || *decoded.Capture != *original.Capture {
		t.Errorf("Capture = %+v, want %+v", decoded.Capture, original.Capture)
	}
	if decoded.InputFactsetIdentity != original.InputFactsetIdentity {
		t.Errorf("InputFactsetIdentity = %q", decoded.InputFactsetIdentity)
	}
	if decoded.PayloadChecksum != original.PayloadChecksum {
		t.Errorf("PayloadChecksum = %q", decoded.PayloadChecksum)
	}
	if string(decoded.Payload) != string(original.Payload) {
		t.Errorf("Payload = %s, want %s", decoded.Payload, original.Payload)
	}
}

// TestEnvelope_JSONRoundTrip_Factset verifies a factset snapshot envelope
// omits the catalog-only fields, since they are not applicable.
func TestEnvelope_JSONRoundTrip_Factset(t *testing.T) {
	original := Envelope{
		FormatVersion:   FormatVersion,
		Kind:            KindFactset,
		Target:          "web-01.example.test",
		Source:          Source{Kind: "puppetdb"},
		CapturedAt:      "2026-08-24T00:00:00Z",
		PayloadChecksum: "sha256:cafebabe",
		Payload:         json.RawMessage(`{"values":{}}`),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	for _, absent := range []string{"requested_environment", "capture", "input_factset_identity"} {
		if _, ok := asMap[absent]; ok {
			t.Errorf("factset envelope JSON unexpectedly contains %q", absent)
		}
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Kind != KindFactset {
		t.Errorf("Kind = %q, want %q", decoded.Kind, KindFactset)
	}
	if decoded.Target != original.Target {
		t.Errorf("Target = %q, want %q", decoded.Target, original.Target)
	}
}
