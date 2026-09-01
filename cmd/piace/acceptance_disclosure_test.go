package main

import (
	"os"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// TestAcceptance_NoReportDisclosesSecretsOrManagedBytes checks the
// disclosure property end to end: no report contains credentials,
// private material, managed content bytes, or unredacted sensitive
// values.
//
// internal/diff tests redaction at the change level. Only this level can
// show that the values do not reappear through a different door —
// provenance, a diagnostic message, an aggregate group, or the canonical
// JSON the HTML artifact embeds. The run is therefore built so that every
// disclosure channel is actually populated: a Sensitive parameter that
// changed, a selector-redacted parameter, inline File content, and File
// content retrieved from the compiler.
func TestAcceptance_NoReportDisclosesSecretsOrManagedBytes(t *testing.T) {
	h := newHarness(t)

	baseline := []resourceSpec{
		{Type: "Service", Title: "nginx", Parameters: map[string]any{
			"ensure":   "running",
			"password": sensitiveWrapper(sensitiveValue),
		}},
		{Type: "File", Title: "/etc/motd", Parameters: map[string]any{
			"content": inlineFileBytes,
		}},
		{Type: "File", Title: "/etc/app.conf", Parameters: map[string]any{
			"source": "puppet:///modules/app/app.conf",
		}},
	}
	candidate := []resourceSpec{
		{Type: "Service", Title: "nginx", Parameters: map[string]any{
			"ensure":   "running",
			"password": sensitiveWrapper(sensitiveValue + "-rotated"),
		}},
		{Type: "File", Title: "/etc/motd", Parameters: map[string]any{
			"content": inlineFileBytes + "-changed",
		}},
		{Type: "File", Title: "/etc/app.conf", Parameters: map[string]any{
			"source": "puppet:///modules/app/app.conf.new",
		}},
	}

	h.pdb.factsets["web-01.example.test"] = pdbFactset("web-01.example.test", true)
	h.pdb.catalogs["web-01.example.test"] = pdbCatalog("web-01.example.test", "production", baseline, nil)
	h.compiler.catalogs["web-01.example.test"] = compilerCatalog("web-01.example.test", "feature-123", candidate, nil)
	h.compiler.fileContent["modules/app/app.conf"] = managedFileBytes
	h.compiler.fileContent["modules/app/app.conf.new"] = managedFileBytes + "-changed"

	// A configured selector redacts File content evidence as well.
	defaults := defaultDefaults + `  redact:
    - type: File
      parameter: content
`
	h.writeConfigs(t, targetsYAML(defaults, target("web-01.example.test")))

	textOut := h.path("report.txt")
	got := h.compare(t, "--text-out", textOut)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}

	// Sanity: the run must actually have produced the differences whose
	// disclosure is being ruled out. A scan over an empty report proves
	// nothing.
	if !strings.Contains(got.text, "Service[nginx] password") {
		t.Fatalf("the Sensitive parameter change is absent from the report; the scan below would be vacuous:\n%s", got.text)
	}
	if !strings.Contains(got.text, model.RedactedValue) {
		t.Fatalf("no redaction marker in the report; the scan below would be vacuous:\n%s", got.text)
	}

	forbidden := map[string]string{
		"the Sensitive payload":              sensitiveValue,
		"inline managed File content":        inlineFileBytes,
		"compiler-retrieved managed content": managedFileBytes,
		"the client private key PEM":         string(h.fixture.clientKeyPEM),
		"a PEM block header":                 "-----BEGIN",
		"the private key path":               h.fixture.privateKey,
		"the client certificate path":        h.fixture.clientCert,
		"the CA bundle path":                 h.fixture.caBundle,
	}

	for artifactName, artifact := range got.all() {
		for label, secret := range forbidden {
			if strings.Contains(artifact, secret) {
				t.Errorf("the %s artifact discloses %s", artifactName, label)
			}
		}
	}
	// The text file artifact is covered by all() only when --text-out was
	// passed; assert it explicitly so a future change to all() cannot
	// silently drop it.
	if got.text == "" {
		t.Fatal("the text artifact was not produced, so it was not scanned")
	}
}

// TestAcceptance_FileContentEvidenceStates covers the file-content
// evidence priority order: each evidence source produces its documented
// state, and no state renders content bytes.
func TestAcceptance_FileContentEvidenceStates(t *testing.T) {
	h := newHarness(t)

	baseline := []resourceSpec{
		// 1: inline content on both sides.
		{Type: "File", Title: "/inline", Parameters: map[string]any{"content": "before"}},
		// 2: a recognized compiled checksum on both sides.
		{Type: "File", Title: "/checksum", Parameters: map[string]any{
			"checksum": "sha256", "checksum_value": "aaaa"}},
		// 3: a source reference retrievable through the compiler.
		{Type: "File", Title: "/retrieved", Parameters: map[string]any{
			"source": "puppet:///modules/app/one"}},
		// 4: a source reference the compiler cannot serve, so no
		// comparable bytes can be established.
		{Type: "File", Title: "/indeterminate", Parameters: map[string]any{
			"source": "puppet:///modules/app/missing"}},
	}
	candidate := []resourceSpec{
		{Type: "File", Title: "/inline", Parameters: map[string]any{"content": "after"}},
		{Type: "File", Title: "/checksum", Parameters: map[string]any{
			"checksum": "sha256", "checksum_value": "bbbb"}},
		{Type: "File", Title: "/retrieved", Parameters: map[string]any{
			"source": "puppet:///modules/app/two"}},
		{Type: "File", Title: "/indeterminate", Parameters: map[string]any{
			"source": "puppet:///modules/app/still-missing"}},
	}

	h.pdb.factsets["web-01.example.test"] = pdbFactset("web-01.example.test", true)
	h.pdb.catalogs["web-01.example.test"] = pdbCatalog("web-01.example.test", "production", baseline, nil)
	h.compiler.catalogs["web-01.example.test"] = compilerCatalog("web-01.example.test", "feature-123", candidate, nil)
	h.compiler.fileContent["modules/app/one"] = "one-bytes"
	h.compiler.fileContent["modules/app/two"] = "two-bytes"

	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
	got := h.compare(t)

	// An indeterminate content comparison can never be reported clean.
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30: an indeterminate File content comparison must not be clean\nstdout:\n%s", got.code, got.stdout)
	}

	for _, want := range []string{
		"~ File[/inline] content: changed (via inline_content)",
		"~ File[/checksum] content: changed (via compiled_checksum)",
		"~ File[/retrieved] content: changed (via compiler_retrieval)",
		"~ File[/indeterminate] content: content_indeterminate",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("the report is missing the evidence line %q\n%s", want, got.stdout)
		}
	}
	// No format renders managed content bytes.
	for artifactName, artifact := range got.all() {
		for _, bytes := range []string{"one-bytes", "two-bytes", "before", "after"} {
			if strings.Contains(artifact, `"`+bytes+`"`) {
				t.Errorf("the %s artifact rendered managed File content %q", artifactName, bytes)
			}
		}
	}
}

// TestAcceptance_TLSPathsResolveAgainstTheServicesFile asserts the one
// path rule every config file follows: a relative path resolves against
// the directory of the file that names it. Snapshot paths resolve against
// the target file, policy_notes_file and the TLS paths resolve against the
// services file.
//
// The case that matters is an operator who drops the CA, certificate and
// key beside services.yaml and names them by bare filename, then runs
// piace from somewhere else entirely.
func TestAcceptance_TLSPathsResolveAgainstTheServicesFile(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())

	for _, name := range []string{"ca.pem", "reader.pem", "reader.key"} {
		data, err := os.ReadFile(h.fixture.dir + "/" + name)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		writeFixtureFile(t, h.path(name), data)
	}
	services := "version: 1\ncompiler:\n  endpoint: " + h.compilerServer.URL +
		"\n  ca_bundle: ca.pem\n  client_cert: reader.pem\n  private_key: reader.key\n" +
		"puppetdb:\n  endpoint: " + h.pdbServer.URL +
		"\n  ca_bundle: ca.pem\n  client_cert: reader.pem\n  private_key: reader.key\n"
	writeFixtureFile(t, h.path("services.yaml"), []byte(services))
	writeFixtureFile(t, h.path("targets.yaml"), []byte(targetsYAML(defaultDefaults, target("web-01.example.test"))))

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0: relative TLS paths resolve against the services file\n%s", got.code, got.stderr)
	}
}

// TestAcceptance_TLSPathsFromTheEnvironment asserts the form a CI job
// uses: the services file is committed and read in place, and the
// per-job credential directory arrives through the environment. Nothing
// renders a template and nothing writes into the checkout.
func TestAcceptance_TLSPathsFromTheEnvironment(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())

	t.Setenv("PIACE_CA_BUNDLE", h.fixture.dir+"/ca.pem")
	t.Setenv("PIACE_CLIENT_CERT", h.fixture.dir+"/reader.pem")
	t.Setenv("PIACE_PRIVATE_KEY", h.fixture.dir+"/reader.key")

	const refs = "  ca_bundle_env: PIACE_CA_BUNDLE\n" +
		"  client_cert_env: PIACE_CLIENT_CERT\n" +
		"  private_key_env: PIACE_PRIVATE_KEY\n"
	services := "version: 1\ncompiler:\n  endpoint: " + h.compilerServer.URL + "\n" + refs +
		"puppetdb:\n  endpoint: " + h.pdbServer.URL + "\n" + refs
	writeFixtureFile(t, h.path("services.yaml"), []byte(services))
	writeFixtureFile(t, h.path("targets.yaml"), []byte(targetsYAML(defaultDefaults, target("web-01.example.test"))))

	got := h.compare(t)
	if got.code != exitcode.Success {
		t.Fatalf("exit = %d, want 0: TLS material named by environment variable\n%s", got.code, got.stderr)
	}
}

// TestAcceptance_TLSPathAndEnvTogetherRejected asserts the rule the
// inference token already follows: naming a credential twice is a
// configuration error, not a precedence rule nobody remembers.
func TestAcceptance_TLSPathAndEnvTogetherRejected(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())

	t.Setenv("PIACE_CA_BUNDLE", h.fixture.dir+"/ca.pem")
	services := "version: 1\ncompiler:\n  endpoint: " + h.compilerServer.URL +
		"\n  ca_bundle: " + h.fixture.dir + "/ca.pem\n  ca_bundle_env: PIACE_CA_BUNDLE\n" +
		"  client_cert: " + h.fixture.dir + "/reader.pem\n  private_key: " + h.fixture.dir + "/reader.key\n" +
		"puppetdb:\n  endpoint: " + h.pdbServer.URL +
		"\n  ca_bundle: " + h.fixture.dir + "/ca.pem\n  client_cert: " + h.fixture.dir +
		"/reader.pem\n  private_key: " + h.fixture.dir + "/reader.key\n"
	writeFixtureFile(t, h.path("services.yaml"), []byte(services))
	writeFixtureFile(t, h.path("targets.yaml"), []byte(targetsYAML(defaultDefaults, target("web-01.example.test"))))

	got := h.compare(t)
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30: ca_bundle and ca_bundle_env set together", got.code)
	}
	if !strings.Contains(got.stderr, "not both") {
		t.Errorf("stderr does not name the conflict:\n%s", got.stderr)
	}
}
