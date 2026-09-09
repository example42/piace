package resolve

import (
	"strings"
	"testing"
	"time"

	"github.com/example42/piace/internal/config"
)

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

// validDefaults returns a config.Defaults that alone (with no per-target
// override) resolves every target to a fully valid model, so individual
// tests can start from a known-good baseline and mutate exactly one field.
func validDefaults() config.Defaults {
	return config.Defaults{
		Candidate: config.CandidateConfig{
			Environment: "feature-123",
			CatalogAPI:  config.CatalogAPIv4,
		},
		Facts: config.FactsConfig{
			Source: config.FactSourcePuppetDB,
		},
		Baseline: config.BaselineConfig{
			Source:      config.BaselineSourceFile,
			Environment: "production",
			File:        "snapshots/catalogs/{certname}.json",
		},
		Exclude: []config.ExclusionRule{
			{Type: "File", Title: "/var/cache/*"},
		},
		Redact: []config.RedactionSelector{
			{Type: "File", Parameter: "content"},
		},
		ImpactEstimate: config.ImpactEstimateConfig{
			Enabled:     boolPtr(true),
			Timeout:     "10s",
			ResultLimit: intPtr(1000),
		},
		FailOnDiff: boolPtr(true),
	}
}

func TestResolveTargets_ValidFullResolution(t *testing.T) {
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: validDefaults(),
		Targets: []config.Target{
			{Certname: "web-01.example.test"},
			{
				Certname: "web-02.example.test",
				Candidate: &config.CandidateConfig{
					Environment: "feature-456",
					CatalogAPI:  config.CatalogAPIv3,
				},
				Facts: &config.FactsConfig{
					Source: config.FactSourceFile,
					File:   "snapshots/facts/{certname}.json",
				},
				Exclude: []config.ExclusionRule{
					{Type: "Notify", Title: "*"},
				},
			},
		},
	}

	targets, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2", len(targets))
	}

	web01 := targets[0]
	if web01.Certname != "web-01.example.test" {
		t.Errorf("targets[0].Certname = %q", web01.Certname)
	}
	if web01.Candidate.Environment != "feature-123" || web01.Candidate.CatalogAPI != config.CatalogAPIv4 {
		t.Errorf("targets[0].Candidate = %+v", web01.Candidate)
	}
	if web01.Facts.Source != config.FactSourcePuppetDB {
		t.Errorf("targets[0].Facts = %+v", web01.Facts)
	}
	wantBaselineFile := "/config/dir/snapshots/catalogs/web-01.example.test.json"
	if web01.Baseline.File != wantBaselineFile {
		t.Errorf("targets[0].Baseline.File = %q, want %q", web01.Baseline.File, wantBaselineFile)
	}
	if web01.ImpactEstimate.Timeout != 10*time.Second || web01.ImpactEstimate.ResultLimit != 1000 {
		t.Errorf("targets[0].ImpactEstimate = %+v", web01.ImpactEstimate)
	}
	if !web01.FailOnDiff {
		t.Errorf("targets[0].FailOnDiff = false, want true")
	}
	if len(web01.Exclude) != 1 || web01.Exclude[0].Title != "/var/cache/*" {
		t.Errorf("targets[0].Exclude = %+v", web01.Exclude)
	}

	web02 := targets[1]
	if web02.Candidate.Environment != "feature-456" || web02.Candidate.CatalogAPI != config.CatalogAPIv3 {
		t.Errorf("targets[1].Candidate = %+v", web02.Candidate)
	}
	wantFactsFile := "/config/dir/snapshots/facts/web-02.example.test.json"
	if web02.Facts.File != wantFactsFile {
		t.Errorf("targets[1].Facts.File = %q, want %q", web02.Facts.File, wantFactsFile)
	}
	// append-only merge: global exclude prepended to per-target exclude.
	wantExclude := []config.ExclusionRule{
		{Type: "File", Title: "/var/cache/*"},
		{Type: "Notify", Title: "*"},
	}
	if len(web02.Exclude) != len(wantExclude) {
		t.Fatalf("targets[1].Exclude = %+v, want %+v", web02.Exclude, wantExclude)
	}
	for i, r := range wantExclude {
		if web02.Exclude[i] != r {
			t.Errorf("targets[1].Exclude[%d] = %+v, want %+v", i, web02.Exclude[i], r)
		}
	}
}

func TestResolveTargets_UnsupportedVersion(t *testing.T) {
	tf := config.TargetFile{
		Version:  2,
		Defaults: validDefaults(),
		Targets:  []config.Target{{Certname: "web-01.example.test"}},
	}
	_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err == nil {
		t.Fatal("expected error for unsupported version, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error = %v, want mention of version", err)
	}
}

func TestResolveTargets_MissingValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.Defaults)
		wantErr string
	}{
		{
			name:    "missing candidate environment",
			mutate:  func(d *config.Defaults) { d.Candidate.Environment = "" },
			wantErr: "candidate.environment is required",
		},
		{
			name:    "missing baseline environment",
			mutate:  func(d *config.Defaults) { d.Baseline.Environment = "" },
			wantErr: "baseline.environment is required",
		},
		{
			name: "missing facts.file when source is file",
			mutate: func(d *config.Defaults) {
				d.Facts = config.FactsConfig{Source: config.FactSourceFile}
			},
			wantErr: "facts.file is required",
		},
		{
			name: "missing baseline.file when source is file",
			mutate: func(d *config.Defaults) {
				d.Baseline = config.BaselineConfig{Source: config.BaselineSourceFile, Environment: "production"}
			},
			wantErr: "baseline.file is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defaults := validDefaults()
			tc.mutate(&defaults)
			tf := config.TargetFile{
				Version:  config.TargetFileVersion,
				Defaults: defaults,
				Targets:  []config.Target{{Certname: "web-01.example.test"}},
			}
			_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolveTargets_DuplicateCertnames(t *testing.T) {
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: validDefaults(),
		Targets: []config.Target{
			{Certname: "web-01.example.test"},
			{Certname: "web-01.example.test"},
		},
	}
	_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err == nil {
		t.Fatal("expected error for duplicate certname, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate certname") || !strings.Contains(err.Error(), "web-01.example.test") {
		t.Errorf("error = %v, want mention of duplicate certname web-01.example.test", err)
	}
}

func TestResolveTargets_InvalidCertnameCharacters(t *testing.T) {
	tests := []string{
		"web/01.example.test",
		`web\01.example.test`,
		"web-01..example.test",
		"web-01.example.test\x00",
		"",
	}
	for _, certname := range tests {
		t.Run(certname, func(t *testing.T) {
			tf := config.TargetFile{
				Version:  config.TargetFileVersion,
				Defaults: validDefaults(),
				Targets:  []config.Target{{Certname: certname}},
			}
			_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
			if err == nil {
				t.Fatalf("expected error for certname %q, got nil", certname)
			}
		})
	}
}

func TestResolveTargets_InvalidGlobPattern(t *testing.T) {
	defaults := validDefaults()
	defaults.Exclude = []config.ExclusionRule{
		{Type: "File", Title: "[unterminated"},
	}
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: defaults,
		Targets:  []config.Target{{Certname: "web-01.example.test"}},
	}
	_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err == nil {
		t.Fatal("expected error for invalid glob pattern, got nil")
	}
	if !strings.Contains(err.Error(), "invalid title glob") {
		t.Errorf("error = %v, want mention of invalid title glob", err)
	}
}

func TestResolveTargets_MalformedRedactionSelector(t *testing.T) {
	tests := []config.RedactionSelector{
		{Type: "", Parameter: "content"},
		{Type: "File", Parameter: ""},
		{Type: "", Parameter: ""},
	}
	for _, sel := range tests {
		t.Run(sel.Type+"/"+sel.Parameter, func(t *testing.T) {
			defaults := validDefaults()
			defaults.Redact = []config.RedactionSelector{sel}
			tf := config.TargetFile{
				Version:  config.TargetFileVersion,
				Defaults: defaults,
				Targets:  []config.Target{{Certname: "web-01.example.test"}},
			}
			_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
			if err == nil {
				t.Fatalf("expected error for redact selector %+v, got nil", sel)
			}
			if !strings.Contains(err.Error(), "redact selector") {
				t.Errorf("error = %v, want mention of redact selector", err)
			}
		})
	}
}

func TestResolveTargets_BadDurationsAndLimits(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.ImpactEstimateConfig)
		wantErr string
	}{
		{
			name:    "empty timeout",
			mutate:  func(c *config.ImpactEstimateConfig) { c.Timeout = "" },
			wantErr: "impact_estimate.timeout is required",
		},
		{
			name:    "malformed duration",
			mutate:  func(c *config.ImpactEstimateConfig) { c.Timeout = "not-a-duration" },
			wantErr: "not a valid duration",
		},
		{
			name:    "zero duration",
			mutate:  func(c *config.ImpactEstimateConfig) { c.Timeout = "0s" },
			wantErr: "must be positive",
		},
		{
			name:    "negative duration",
			mutate:  func(c *config.ImpactEstimateConfig) { c.Timeout = "-5s" },
			wantErr: "must be positive",
		},
		{
			name:    "missing result limit",
			mutate:  func(c *config.ImpactEstimateConfig) { c.ResultLimit = nil },
			wantErr: "result_limit is required",
		},
		{
			name:    "zero result limit",
			mutate:  func(c *config.ImpactEstimateConfig) { c.ResultLimit = intPtr(0) },
			wantErr: "must be a positive integer",
		},
		{
			name:    "negative result limit",
			mutate:  func(c *config.ImpactEstimateConfig) { c.ResultLimit = intPtr(-1) },
			wantErr: "must be a positive integer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defaults := validDefaults()
			tc.mutate(&defaults.ImpactEstimate)
			tf := config.TargetFile{
				Version:  config.TargetFileVersion,
				Defaults: defaults,
				Targets:  []config.Target{{Certname: "web-01.example.test"}},
			}
			_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolveTargets_ImpactEstimateDisabledSkipsValidation(t *testing.T) {
	defaults := validDefaults()
	defaults.ImpactEstimate = config.ImpactEstimateConfig{Enabled: boolPtr(false)}
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: defaults,
		Targets:  []config.Target{{Certname: "web-01.example.test"}},
	}
	targets, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	if targets[0].ImpactEstimate.Enabled {
		t.Errorf("ImpactEstimate.Enabled = true, want false")
	}
}

func TestResolveTargets_V3WithAllowFallbackRejected(t *testing.T) {
	defaults := validDefaults()
	defaults.Candidate = config.CandidateConfig{
		Environment:     "feature-123",
		CatalogAPI:      config.CatalogAPIv3,
		AllowV3Fallback: boolPtr(true),
	}
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: defaults,
		Targets:  []config.Target{{Certname: "web-01.example.test"}},
	}
	_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err == nil {
		t.Fatal("expected error for allow_v3_fallback with catalog_api v3, got nil")
	}
	if !strings.Contains(err.Error(), "allow_v3_fallback is valid only with catalog_api: v4") {
		t.Errorf("error = %v", err)
	}
}

func TestResolveTargets_V4WithAllowFallbackAccepted(t *testing.T) {
	defaults := validDefaults()
	defaults.Candidate = config.CandidateConfig{
		Environment:     "feature-123",
		CatalogAPI:      config.CatalogAPIv4,
		AllowV3Fallback: boolPtr(true),
	}
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: defaults,
		Targets:  []config.Target{{Certname: "web-01.example.test"}},
	}
	targets, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	if !targets[0].Candidate.AllowV3Fallback {
		t.Errorf("AllowV3Fallback = false, want true")
	}
}

func TestResolveTargets_InvalidCatalogAPI(t *testing.T) {
	defaults := validDefaults()
	defaults.Candidate.CatalogAPI = "v5"
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: defaults,
		Targets:  []config.Target{{Certname: "web-01.example.test"}},
	}
	_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err == nil {
		t.Fatal("expected error for invalid catalog_api, got nil")
	}
	if !strings.Contains(err.Error(), "catalog_api must be") {
		t.Errorf("error = %v", err)
	}
}

func TestResolveTargets_PuppetDBSourceRejectsFile(t *testing.T) {
	defaults := validDefaults()
	defaults.Facts = config.FactsConfig{Source: config.FactSourcePuppetDB, File: "should-not-be-here.json"}
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: defaults,
		Targets:  []config.Target{{Certname: "web-01.example.test"}},
	}
	_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err == nil {
		t.Fatal("expected error for facts.file set with puppetdb source, got nil")
	}
	if !strings.Contains(err.Error(), "facts.file must not be set") {
		t.Errorf("error = %v", err)
	}
}

func TestResolveTargets_TemplatePathTraversalRejected(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{name: "parent-dir traversal", file: "../{certname}.json"},
		{name: "template not entire component", file: "snapshots/{certname}-catalog.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defaults := validDefaults()
			defaults.Baseline = config.BaselineConfig{
				Source:      config.BaselineSourceFile,
				Environment: "production",
				File:        tc.file,
			}
			tf := config.TargetFile{
				Version:  config.TargetFileVersion,
				Defaults: defaults,
				Targets:  []config.Target{{Certname: "web-01.example.test"}},
			}
			_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
			if err == nil {
				t.Fatalf("expected error for file %q, got nil", tc.file)
			}
		})
	}
}

func TestResolveTargets_ValidTemplatePathVariants(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		wantFile string
	}{
		{
			name:     "template as entire final component",
			file:     "snapshots/catalogs/{certname}.json",
			wantFile: "/config/dir/snapshots/catalogs/web-01.example.test.json",
		},
		{
			name:     "explicit absolute path",
			file:     "/var/piace/snapshots/web-01.example.test.json",
			wantFile: "/var/piace/snapshots/web-01.example.test.json",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defaults := validDefaults()
			defaults.Baseline = config.BaselineConfig{
				Source:      config.BaselineSourceFile,
				Environment: "production",
				File:        tc.file,
			}
			tf := config.TargetFile{
				Version:  config.TargetFileVersion,
				Defaults: defaults,
				Targets:  []config.Target{{Certname: "web-01.example.test"}},
			}
			targets, err := ResolveTargets(tf, "/config/dir", CommandCompare)
			if err != nil {
				t.Fatalf("ResolveTargets: %v", err)
			}
			if targets[0].Baseline.File != tc.wantFile {
				t.Errorf("Baseline.File = %q, want %q", targets[0].Baseline.File, tc.wantFile)
			}
		})
	}
}

func TestResolveTargets_AppendOnlyMergeWithDuplicateRetention(t *testing.T) {
	defaults := validDefaults()
	defaults.Exclude = []config.ExclusionRule{
		{Type: "File", Title: "/var/cache/*"},
		{Type: "Notify", Title: "*"},
	}
	defaults.Redact = []config.RedactionSelector{
		{Type: "File", Parameter: "content"},
	}
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: defaults,
		Targets: []config.Target{
			{
				Certname: "web-01.example.test",
				Exclude: []config.ExclusionRule{
					{Type: "File", Title: "/var/cache/*"}, // exact duplicate of a global rule
					{Type: "Package", Title: "curl"},
				},
				Redact: []config.RedactionSelector{
					{Type: "File", Parameter: "content"}, // exact duplicate
					{Type: "Exec", Parameter: "password"},
				},
			},
		},
	}

	targets, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}

	wantExclude := []config.ExclusionRule{
		{Type: "File", Title: "/var/cache/*"},
		{Type: "Notify", Title: "*"},
		{Type: "Package", Title: "curl"},
	}
	if len(targets[0].Exclude) != len(wantExclude) {
		t.Fatalf("Exclude = %+v, want %+v", targets[0].Exclude, wantExclude)
	}
	for i, r := range wantExclude {
		if targets[0].Exclude[i] != r {
			t.Errorf("Exclude[%d] = %+v, want %+v", i, targets[0].Exclude[i], r)
		}
	}

	wantRedact := []config.RedactionSelector{
		{Type: "File", Parameter: "content"},
		{Type: "Exec", Parameter: "password"},
	}
	if len(targets[0].Redact) != len(wantRedact) {
		t.Fatalf("Redact = %+v, want %+v", targets[0].Redact, wantRedact)
	}
	for i, r := range wantRedact {
		if targets[0].Redact[i] != r {
			t.Errorf("Redact[%d] = %+v, want %+v", i, targets[0].Redact[i], r)
		}
	}
}

func TestResolveTargets_AccumulatesMultipleErrors(t *testing.T) {
	tf := config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: validDefaults(),
		Targets: []config.Target{
			{Certname: "web-01.example.test"},
			{Certname: "web-01.example.test"}, // duplicate
			{Certname: "bad/certname"},        // invalid certname
			{
				Certname: "web-02.example.test",
				Candidate: &config.CandidateConfig{
					Environment: "", // missing
					CatalogAPI:  config.CatalogAPIv4,
				},
			},
		},
	}
	_, err := ResolveTargets(tf, "/config/dir", CommandCompare)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if len(ve.Problems()) < 3 {
		t.Errorf("Problems() = %v, want at least 3 accumulated problems", ve.Problems())
	}
}
