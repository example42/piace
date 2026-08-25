package model

import (
	"encoding/json"
	"testing"
)

func TestResourceIdentity_String(t *testing.T) {
	id := ResourceIdentity{Type: "File", Title: "/etc/motd"}
	if got, want := id.String(), "File[/etc/motd]"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestNormalizedCatalog_JSONRoundTrip verifies the normalized catalog
// schema (resources, edges, parameters within the Value domain) marshals
// and unmarshals via encoding/json without field loss.
func TestNormalizedCatalog_JSONRoundTrip(t *testing.T) {
	original := NormalizedCatalog{
		Certname:    "web-01.example.test",
		Environment: "production",
		Resources: []Resource{
			{
				Identity: ResourceIdentity{Type: "File", Title: "/etc/motd"},
				Parameters: map[string]Value{
					"ensure":  "file",
					"mode":    "0644",
					"managed": true,
					"deps":    []Value{"a", "b"},
				},
			},
			{
				Identity:   ResourceIdentity{Type: "Notify", Title: "hello"},
				Parameters: map[string]Value{"message": "hi"},
			},
		},
		Edges: []Edge{
			{Source: "Notify[hello]", Target: "File[/etc/motd]"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded NormalizedCatalog
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Certname != original.Certname || decoded.Environment != original.Environment {
		t.Errorf("decoded top-level fields mismatch: %+v", decoded)
	}
	if len(decoded.Resources) != 2 {
		t.Fatalf("Resources = %+v", decoded.Resources)
	}
	if decoded.Resources[0].Identity != original.Resources[0].Identity {
		t.Errorf("Resources[0].Identity = %+v, want %+v", decoded.Resources[0].Identity, original.Resources[0].Identity)
	}
	if decoded.Resources[0].Parameters["ensure"] != "file" {
		t.Errorf("Resources[0].Parameters[ensure] = %v", decoded.Resources[0].Parameters["ensure"])
	}
	if len(decoded.Edges) != 1 || decoded.Edges[0] != original.Edges[0] {
		t.Errorf("Edges = %+v, want %+v", decoded.Edges, original.Edges)
	}
}

// TestFileContentEvidence_JSONRoundTrip verifies the File-content evidence
// schema never requires bytes to round-trip, only digests/state.
func TestFileContentEvidence_JSONRoundTrip(t *testing.T) {
	original := FileContentEvidence{
		State:          FileContentChanged,
		EvidenceSource: FileContentEvidenceCompiledChecksum,
		Algorithm:      "sha256",
		BeforeDigest:   "aaaa",
		AfterDigest:    "bbbb",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded FileContentEvidence
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != original {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", decoded, original)
	}
}
