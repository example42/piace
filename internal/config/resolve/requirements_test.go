package resolve

import (
	"strings"
	"testing"

	"github.com/example42/piace/internal/config"
)

// containsProblem reports whether err's accumulated text mentions want.
// Resolution reports every problem it finds in one error, so a test
// asserting on one of them looks inside that combined text.
func containsProblem(err error, want string) bool {
	return err != nil && strings.Contains(err.Error(), want)
}

// minimalTargetFile is a target file carrying only what the command
// under test needs, so a rule that quietly requires more fails here.
func minimalTargetFile(defaults config.Defaults) config.TargetFile {
	return config.TargetFile{
		Version:  config.TargetFileVersion,
		Defaults: defaults,
		Targets:  []config.Target{{Certname: "web-01.example.test"}},
	}
}

// TestResolveTargets_PresenceIsCommandScoped covers the requirement
// matrix: each command's minimal target file resolves, and the fields it
// does not read stay empty rather than being invented.
func TestResolveTargets_PresenceIsCommandScoped(t *testing.T) {
	factsToFile := config.FactsConfig{Source: config.FactSourceFile, File: "snapshots/facts/{certname}.json"}
	baselineToFile := config.BaselineConfig{Source: config.BaselineSourceFile, File: "snapshots/catalogs/{certname}.json"}

	cases := map[string]struct {
		cmd      Command
		defaults config.Defaults
	}{
		"capture facts needs only a fact destination": {
			cmd:      CommandCaptureFacts,
			defaults: config.Defaults{Facts: factsToFile},
		},
		"capture catalog needs no candidate environment": {
			cmd: CommandCaptureCatalog,
			defaults: config.Defaults{
				Candidate: config.CandidateConfig{CatalogAPI: config.CatalogAPIv4},
				Facts:     config.FactsConfig{Source: config.FactSourcePuppetDB},
				Baseline:  baselineToFile,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			targets, err := ResolveTargets(minimalTargetFile(tc.defaults), "/config/dir", tc.cmd)
			if err != nil {
				t.Fatalf("ResolveTargets: %v", err)
			}
			if len(targets) != 1 {
				t.Fatalf("targets = %+v", targets)
			}
			if targets[0].Candidate.Environment != "" {
				t.Errorf("Candidate.Environment = %q, want empty: nothing supplied one",
					targets[0].Candidate.Environment)
			}
		})
	}
}

// TestResolveTargets_CompareStillRequiresEverything keeps the relaxation
// scoped: the same minimal files a capture command accepts are rejected
// for compare, which reads the missing fields.
func TestResolveTargets_CompareStillRequiresEverything(t *testing.T) {
	defaults := config.Defaults{Facts: config.FactsConfig{Source: config.FactSourceFile, File: "f.json"}}
	_, err := ResolveTargets(minimalTargetFile(defaults), "/config/dir", CommandCompare)
	if err == nil {
		t.Fatal("compare accepted a target file with no candidate and no baseline")
	}
	for _, want := range []string{"candidate.environment", "candidate.catalog_api", "baseline.environment", "baseline.source"} {
		if !containsProblem(err, want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestResolveTargets_PresentValuesAreValidatedForEveryCommand is the
// other half of the contract: only presence is command-scoped. A value
// that is there is checked the same way whoever is reading it, so a
// capture run cannot pass with a target file a comparison would reject
// as nonsense.
func TestResolveTargets_PresentValuesAreValidatedForEveryCommand(t *testing.T) {
	for _, cmd := range []Command{CommandCompare, CommandCaptureFacts, CommandCaptureCatalog} {
		t.Run(string(cmd), func(t *testing.T) {
			defaults := config.Defaults{
				Candidate: config.CandidateConfig{Environment: "feature-1", CatalogAPI: "v9"},
				Facts:     config.FactsConfig{Source: config.FactSourceFile, File: "../escape.json"},
				Baseline:  config.BaselineConfig{Source: "invented", Environment: "production"},
			}
			_, err := ResolveTargets(minimalTargetFile(defaults), "/config/dir", cmd)
			if err == nil {
				t.Fatal("accepted invalid values because the command does not read them")
			}
			for _, want := range []string{"catalog_api", "baseline.source", "escape"} {
				if !containsProblem(err, want) {
					t.Errorf("the error does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// TestCommand_Services derives the endpoints each command needs from the
// target file, which is what lets a services file name only the
// endpoints a run actually contacts.
func TestCommand_Services(t *testing.T) {
	fileFacts := config.FactsConfig{Source: config.FactSourceFile, File: "f.json"}
	fileBaseline := config.BaselineConfig{Source: config.BaselineSourceFile, Environment: "production", File: "c.json"}
	pdbFacts := config.FactsConfig{Source: config.FactSourcePuppetDB}
	pdbBaseline := config.BaselineConfig{Source: config.BaselineSourcePuppetDB, Environment: "production"}

	cases := map[string]struct {
		cmd      Command
		defaults config.Defaults
		want     ServiceSet
	}{
		"compare over files only": {
			cmd:      CommandCompare,
			defaults: config.Defaults{Facts: fileFacts, Baseline: fileBaseline},
			want:     ServiceSet{Compiler: true},
		},
		"compare with a puppetdb baseline": {
			cmd:      CommandCompare,
			defaults: config.Defaults{Facts: fileFacts, Baseline: pdbBaseline},
			want:     ServiceSet{Compiler: true, PuppetDB: true},
		},
		"compare with puppetdb facts": {
			cmd:      CommandCompare,
			defaults: config.Defaults{Facts: pdbFacts, Baseline: fileBaseline},
			want:     ServiceSet{Compiler: true, PuppetDB: true},
		},
		"compare over files with the impact estimate enabled": {
			cmd: CommandCompare,
			defaults: config.Defaults{Facts: fileFacts, Baseline: fileBaseline,
				ImpactEstimate: config.ImpactEstimateConfig{Enabled: boolPtr(true)}},
			want: ServiceSet{Compiler: true, PuppetDB: true},
		},
		"capture facts never compiles": {
			cmd:      CommandCaptureFacts,
			defaults: config.Defaults{Facts: fileFacts},
			want:     ServiceSet{PuppetDB: true},
		},
		"capture catalog from a file-backed factset": {
			cmd:      CommandCaptureCatalog,
			defaults: config.Defaults{Facts: fileFacts, Baseline: fileBaseline},
			want:     ServiceSet{Compiler: true},
		},
		"capture catalog from PuppetDB facts": {
			cmd:      CommandCaptureCatalog,
			defaults: config.Defaults{Facts: pdbFacts, Baseline: fileBaseline},
			want:     ServiceSet{Compiler: true, PuppetDB: true},
		},
		"capture catalog skipping every target": {
			cmd:      CommandCaptureCatalog,
			defaults: config.Defaults{Facts: pdbFacts, Baseline: pdbBaseline},
			want:     ServiceSet{Compiler: true},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.cmd.services(minimalTargetFile(tc.defaults)); got != tc.want {
				t.Errorf("services = %+v, want %+v", got, tc.want)
			}
		})
	}

	// One target overriding the defaults is the case that matters most:
	// this answer and puppetdb.SelectFactSource's per-target choice are
	// two expressions of one rule, and a run whose configuration says no
	// PuppetDB client is needed while one target then asks for one has no
	// adapter to ask.
	t.Run("one target overrides file-backed defaults", func(t *testing.T) {
		tf := config.TargetFile{
			Version:  config.TargetFileVersion,
			Defaults: config.Defaults{Facts: fileFacts, Baseline: fileBaseline},
			Targets: []config.Target{
				{Certname: "web-01.example.test"},
				{Certname: "web-02.example.test", Facts: &pdbFacts},
			},
		}
		want := ServiceSet{Compiler: true, PuppetDB: true}
		if got := CommandCompare.services(tf); got != want {
			t.Errorf("services = %+v, want %+v: one target selects PuppetDB facts", got, want)
		}
	})

	t.Run("one target overrides puppetdb defaults away", func(t *testing.T) {
		tf := config.TargetFile{
			Version:  config.TargetFileVersion,
			Defaults: config.Defaults{Facts: fileFacts, Baseline: fileBaseline},
			Targets: []config.Target{
				{Certname: "web-01.example.test", Baseline: &pdbBaseline},
			},
		}
		want := ServiceSet{Compiler: true, PuppetDB: true}
		if got := CommandCompare.services(tf); got != want {
			t.Errorf("services = %+v, want %+v: the target's own baseline is PuppetDB", got, want)
		}
	})
}

// TestResolveServices_UnneededSectionIsNotRequired verifies a services
// file naming only the compiler resolves for a run that needs only the
// compiler, and that the unused section comes back zero rather than
// half-populated.
func TestResolveServices_UnneededSectionIsNotRequired(t *testing.T) {
	sf := config.ServicesFile{
		Version: config.ServicesFileVersion,
		Compiler: config.ServiceEndpoint{
			Endpoint:   "https://compiler.example.test:8140",
			CABundle:   "/etc/piace/ca.pem",
			ClientCert: "/etc/piace/cert.pem",
			PrivateKey: "/etc/piace/key.pem",
		},
	}
	svc, err := ResolveServices(sf, "/etc/piace", ServiceSet{Compiler: true})
	if err != nil {
		t.Fatalf("ResolveServices: %v", err)
	}
	if svc.Compiler.URL == nil {
		t.Error("the compiler endpoint was not resolved")
	}
	if svc.PuppetDB != (Endpoint{}) {
		t.Errorf("PuppetDB = %+v, want the zero endpoint for an unrequired service", svc.PuppetDB)
	}

	if _, err := ResolveServices(sf, "/etc/piace", ServiceSet{Compiler: true, PuppetDB: true}); err == nil {
		t.Error("a run that needs PuppetDB accepted a services file that does not configure it")
	}
}

// TestResolveServices_Timeout covers the optional per-service request
// deadline: absent means the transport's default, present must be a
// valid positive duration.
func TestResolveServices_Timeout(t *testing.T) {
	base := config.ServiceEndpoint{
		Endpoint:   "https://compiler.example.test:8140",
		CABundle:   "/etc/piace/ca.pem",
		ClientCert: "/etc/piace/cert.pem",
		PrivateKey: "/etc/piace/key.pem",
	}
	need := ServiceSet{Compiler: true}

	svc, err := ResolveServices(config.ServicesFile{Version: 1, Compiler: base}, "/etc/piace", need)
	if err != nil {
		t.Fatalf("ResolveServices: %v", err)
	}
	if svc.Compiler.Timeout != 0 {
		t.Errorf("Timeout = %s, want zero when the file names none", svc.Compiler.Timeout)
	}

	withTimeout := base
	withTimeout.Timeout = "90s"
	svc, err = ResolveServices(config.ServicesFile{Version: 1, Compiler: withTimeout}, "/etc/piace", need)
	if err != nil {
		t.Fatalf("ResolveServices: %v", err)
	}
	if svc.Compiler.Timeout.String() != "1m30s" {
		t.Errorf("Timeout = %s, want 90s", svc.Compiler.Timeout)
	}

	for _, bad := range []string{"nonsense", "0s", "-5s"} {
		invalid := base
		invalid.Timeout = bad
		if _, err := ResolveServices(config.ServicesFile{Version: 1, Compiler: invalid}, "/etc/piace", need); err == nil {
			t.Errorf("accepted timeout %q", bad)
		}
	}
}
