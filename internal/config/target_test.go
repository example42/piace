package config

import (
	"encoding/json"
	"testing"
)

// TestTargetFile_JSONRoundTrip verifies the versioned target-file schema
// marshals and unmarshals via encoding/json without field loss. Full YAML
// decoding, unknown-field rejection, and default resolution are task 2's
// concern; this only locks the wire shape defined in this task.
func TestTargetFile_JSONRoundTrip(t *testing.T) {
	trueVal := true
	limit := 1000
	original := TargetFile{
		Version: TargetFileVersion,
		Defaults: Defaults{
			Candidate: CandidateConfig{
				Environment:     "feature-123",
				CatalogAPI:      CatalogAPIv4,
				AllowV3Fallback: &trueVal,
			},
			Facts: FactsConfig{Source: FactSourcePuppetDB},
			Baseline: BaselineConfig{
				Source:      BaselineSourceFile,
				Environment: "production",
				File:        "snapshots/catalogs/{certname}.json",
			},
			Exclude: []ExclusionRule{
				{Type: "File", Title: "/var/cache/*"},
				{Type: "Notify", Title: "*"},
			},
			Redact: []RedactionSelector{
				{Type: "File", Parameter: "content"},
			},
			ImpactEstimate: ImpactEstimateConfig{
				Enabled:     &trueVal,
				Timeout:     "10s",
				ResultLimit: &limit,
			},
			FailOnDiff: &trueVal,
		},
		Targets: []Target{
			{
				Certname: "web-01.example.test",
				Candidate: &CandidateConfig{
					Environment: "feature-123",
					CatalogAPI:  CatalogAPIv4,
				},
				Facts: &FactsConfig{
					Source: FactSourceFile,
					File:   "snapshots/facts/web-01.example.test.json",
				},
				Baseline: &BaselineConfig{
					Source:      BaselineSourceFile,
					Environment: "production",
					File:        "snapshots/catalogs/web-01.example.test.json",
				},
				Exclude: []ExclusionRule{
					{Type: "File", Title: "/var/lib/app/cache/*"},
				},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded TargetFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Version != TargetFileVersion {
		t.Errorf("Version = %d, want %d", decoded.Version, TargetFileVersion)
	}
	if decoded.Defaults.Candidate.Environment != "feature-123" {
		t.Errorf("Defaults.Candidate.Environment = %q", decoded.Defaults.Candidate.Environment)
	}
	if decoded.Defaults.Candidate.AllowV3Fallback == nil || !*decoded.Defaults.Candidate.AllowV3Fallback {
		t.Errorf("Defaults.Candidate.AllowV3Fallback lost or false")
	}
	if len(decoded.Defaults.Exclude) != 2 || decoded.Defaults.Exclude[1].Title != "*" {
		t.Errorf("Defaults.Exclude = %+v", decoded.Defaults.Exclude)
	}
	if decoded.Defaults.ImpactEstimate.ResultLimit == nil || *decoded.Defaults.ImpactEstimate.ResultLimit != 1000 {
		t.Errorf("Defaults.ImpactEstimate.ResultLimit lost")
	}
	if len(decoded.Targets) != 1 || decoded.Targets[0].Certname != "web-01.example.test" {
		t.Fatalf("Targets = %+v", decoded.Targets)
	}
	if decoded.Targets[0].Facts == nil || decoded.Targets[0].Facts.Source != FactSourceFile {
		t.Errorf("Targets[0].Facts = %+v", decoded.Targets[0].Facts)
	}
	if decoded.Targets[0].Baseline == nil || decoded.Targets[0].Baseline.Environment != "production" {
		t.Errorf("Targets[0].Baseline = %+v", decoded.Targets[0].Baseline)
	}
}

// TestTarget_OmittedOverridesStayNil ensures a target that supplies no
// override for a section unmarshals with a nil pointer rather than a
// zero-value struct, so the (not-yet-implemented) resolver can distinguish
// "not overridden" from "explicitly set to the zero value".
func TestTarget_OmittedOverridesStayNil(t *testing.T) {
	data := []byte(`{"certname": "web-02.example.test"}`)
	var target Target
	if err := json.Unmarshal(data, &target); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if target.Candidate != nil {
		t.Errorf("Candidate = %+v, want nil", target.Candidate)
	}
	if target.Facts != nil {
		t.Errorf("Facts = %+v, want nil", target.Facts)
	}
	if target.Baseline != nil {
		t.Errorf("Baseline = %+v, want nil", target.Baseline)
	}
	if target.FailOnDiff != nil {
		t.Errorf("FailOnDiff = %v, want nil", target.FailOnDiff)
	}
}
