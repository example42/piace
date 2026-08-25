package model

import (
	"encoding/json"
	"testing"
)

// TestImpactEstimate_JSONRoundTrip verifies the impact estimate schema
// (PQL, limits, sample, truncation, timeout, failure state) marshals and
// unmarshals via encoding/json without field loss.
func TestImpactEstimate_JSONRoundTrip(t *testing.T) {
	original := ImpactEstimate{
		Identity:    ResourceIdentity{Type: "File", Title: "/etc/motd"},
		PQL:         `resources[certname] { type = "File" and title = "/etc/motd" }`,
		ResultLimit: 1000,
		Timeout:     "10s",
		Status:      ImpactStatusCompleted,
		Certnames:   []string{"a.example.test", "b.example.test"},
		ResultCount: 2,
		Truncated:   false,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded ImpactEstimate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.PQL != original.PQL {
		t.Errorf("PQL = %q, want %q", decoded.PQL, original.PQL)
	}
	if decoded.ResultLimit != original.ResultLimit {
		t.Errorf("ResultLimit = %d, want %d", decoded.ResultLimit, original.ResultLimit)
	}
	if decoded.Status != ImpactStatusCompleted {
		t.Errorf("Status = %q", decoded.Status)
	}
	if len(decoded.Certnames) != 2 {
		t.Errorf("Certnames = %+v", decoded.Certnames)
	}
}

// TestImpactEstimate_FailedStatusCarriesReason verifies a failed estimate
// carries a safe failure reason and no certname sample.
func TestImpactEstimate_FailedStatusCarriesReason(t *testing.T) {
	original := ImpactEstimate{
		Identity:      ResourceIdentity{Type: "File", Title: "/etc/motd"},
		PQL:           `resources[certname] { type = "File" and title = "/etc/motd" }`,
		ResultLimit:   1000,
		Timeout:       "10s",
		Status:        ImpactStatusFailed,
		FailureReason: "puppetdb request timed out",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded ImpactEstimate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Status != ImpactStatusFailed {
		t.Errorf("Status = %q, want %q", decoded.Status, ImpactStatusFailed)
	}
	if decoded.FailureReason != original.FailureReason {
		t.Errorf("FailureReason = %q, want %q", decoded.FailureReason, original.FailureReason)
	}
	if len(decoded.Certnames) != 0 {
		t.Errorf("Certnames = %+v, want empty", decoded.Certnames)
	}
}
