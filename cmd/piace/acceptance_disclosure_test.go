package main

import (
	"os"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// TestAcceptance_NoReportDisclosesSecretsOrManagedBytes is task 12's
// final acceptance condition and design.md's Property 5 checked end to
// end: "no report contains credentials, private material, managed content
// bytes, or unredacted sensitive values."
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

	// A configured selector redacts File content evidence as well, per
	// requirements.md 8.8 and design.md section 7.2.
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

// TestAcceptance_FileContentEvidenceStates covers requirements.md
// 5.5-5.8 and design.md section 7.2's priority order: each evidence
// source produces its documented state, and no state renders content
// bytes.
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

	// An indeterminate content comparison can never be reported clean
	// (requirements.md 5.7/10.5, design.md section 7.2).
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
	// requirements.md 5.8: no format renders managed content bytes.
	for artifactName, artifact := range got.all() {
		for _, bytes := range []string{"one-bytes", "two-bytes", "before", "after"} {
			if strings.Contains(artifact, `"`+bytes+`"`) {
				t.Errorf("the %s artifact rendered managed File content %q", artifactName, bytes)
			}
		}
	}
}

// TestAcceptance_TLSPathsResolveAgainstWorkingDirectory documents an
// operator trap found while validating task 11, asserted here as the
// behavior actually shipped rather than silently accepted.
//
// design.md section 3.2 rule 5 resolves a relative *snapshot* path
// against the target-file directory. Nothing in the design says the same
// about the TLS paths in the services file, and internal/transport
// resolves them against the process working directory — so a CI job that
// runs `piace` from a directory other than the one holding services.yaml
// must use absolute TLS paths. This asymmetry is reported as a finding
// for a future task; task 12 does not change task 2/3 behavior.
func TestAcceptance_TLSPathsResolveAgainstWorkingDirectory(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())

	// Copy the CA/cert/key beside the services file and reference them by
	// bare filename, the way an operator reasonably would.
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
	if got.code != exitcode.OperationalError {
		t.Fatalf("exit = %d, want 30: relative TLS paths resolve against the working directory, not the services file", got.code)
	}
	if !strings.Contains(got.stderr, "no such file or directory") {
		t.Errorf("stderr does not explain the unresolved TLS path:\n%s", got.stderr)
	}
}
