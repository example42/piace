package resolve

import (
	"strings"
	"testing"
	"time"

	"github.com/example42/piace/internal/config"
)

func TestProvenance_ExcludesServiceCredentials(t *testing.T) {
	target := Target{
		Certname: "web-01.example.test",
		Candidate: Candidate{
			Environment:     "feature-123",
			CatalogAPI:      config.CatalogAPIv4,
			AllowV3Fallback: false,
		},
		Facts: Facts{Source: config.FactSourcePuppetDB},
		Baseline: Baseline{
			Source:      config.BaselineSourceFile,
			Environment: "production",
			File:        "/config/dir/snapshots/catalogs/web-01.example.test.json",
		},
		Exclude: []config.ExclusionRule{{Type: "File", Title: "/var/cache/*"}},
		ImpactEstimate: ImpactEstimate{
			Enabled:     true,
			Timeout:     10 * time.Second,
			ResultLimit: 1000,
		},
		FailOnDiff: true,
	}

	prov := Provenance(target)

	if prov.Candidate["environment"] != "feature-123" {
		t.Errorf("Candidate[environment] = %v", prov.Candidate["environment"])
	}
	if prov.Baseline["file"] != target.Baseline.File {
		t.Errorf("Baseline[file] = %v, want %v", prov.Baseline["file"], target.Baseline.File)
	}
	if len(prov.Exclude) != 1 || prov.Exclude[0].Type != "File" {
		t.Errorf("Exclude = %+v", prov.Exclude)
	}
	if prov.ImpactEstimate["timeout"] != "10s" {
		t.Errorf("ImpactEstimate[timeout] = %v", prov.ImpactEstimate["timeout"])
	}
	if !prov.FailOnDiff {
		t.Errorf("FailOnDiff = false, want true")
	}

	// Provenance is built only from Target, never from Services/Endpoint,
	// so there is no code path by which a ClientCert/PrivateKey value
	// could appear. Assert the map values contain none of the known
	// credential-shaped strings as a defense-in-depth check.
	for _, m := range []map[string]any{prov.Candidate, prov.Facts, prov.Baseline, prov.ImpactEstimate} {
		for k, v := range m {
			if s, ok := v.(string); ok {
				if containsCredentialHint(s) {
					t.Errorf("provenance field %q = %q looks like a credential path", k, s)
				}
			}
		}
	}
}

func containsCredentialHint(s string) bool {
	for _, hint := range []string{"private_key", "client_cert", ".key", "client-key"} {
		if strings.Contains(s, hint) {
			return true
		}
	}
	return false
}
