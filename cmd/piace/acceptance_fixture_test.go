package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example42/piace/internal/exitcode"
)

// Literal values the disclosure scan looks for. They are deliberately
// distinctive so a match in a rendered artifact is unambiguous.
const (
	sensitiveValue   = "s3cr3t-acceptance-sensitive-value"
	managedFileBytes = "MANAGED-FILE-BYTES-acceptance-only"
	inlineFileBytes  = "INLINE-FILE-BYTES-acceptance-only"
)

// fixedTimestamp is the invocation timestamp every acceptance run
// records, so two runs over identical served fixtures differ in nothing.
var fixedTimestamp = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

// harness is one acceptance environment: an mTLS PuppetDB, an mTLS
// compiler, a third service that must never be contacted, a temporary
// config directory, and a fixed clock.
type harness struct {
	dir            string
	fixture        *tlsFixture
	pdb            *fakePuppetDB
	compiler       *fakeCompiler
	pdbServer      *httptest.Server
	compilerServer *httptest.Server
	forbidden      *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fixture := newTLSFixture(t)
	pdb := newFakePuppetDB()
	compiler := newFakeCompiler()

	previous := clock
	clock = func() time.Time { return fixedTimestamp }
	t.Cleanup(func() { clock = previous })

	return &harness{
		dir:            t.TempDir(),
		fixture:        fixture,
		pdb:            pdb,
		compiler:       compiler,
		pdbServer:      startTLS(t, fixture, pdb.handler()),
		compilerServer: startTLS(t, fixture, compiler.handler()),
		forbidden:      startForbiddenTLS(t, fixture),
	}
}

// writeConfigs writes services.yaml and targets.yaml. TLS paths are
// absolute: internal/transport resolves them against the process working
// directory, not against the services file (see acceptance_test.go's
// TestAcceptance_TLSPathsResolveAgainstWorkingDirectory).
func (h *harness) writeConfigs(t *testing.T, targetsYAML string) {
	t.Helper()
	services := fmt.Sprintf(`version: 1
compiler:
  endpoint: %s
  ca_bundle: %s
  client_cert: %s
  private_key: %s
puppetdb:
  endpoint: %s
  ca_bundle: %s
  client_cert: %s
  private_key: %s
`, h.compilerServer.URL, h.fixture.caBundle, h.fixture.clientCert, h.fixture.privateKey,
		h.pdbServer.URL, h.fixture.caBundle, h.fixture.clientCert, h.fixture.privateKey)

	writeFixtureFile(t, filepath.Join(h.dir, "services.yaml"), []byte(services))
	writeFixtureFile(t, filepath.Join(h.dir, "targets.yaml"), []byte(targetsYAML))
}

func (h *harness) path(name string) string { return filepath.Join(h.dir, name) }

// artifacts is one compare run's outputs.
type artifacts struct {
	code   exitcode.Code
	stdout string
	stderr string
	json   string
	html   string
	text   string
}

// all returns every rendered artifact as one slice, for scans that must
// cover all three formats.
func (a artifacts) all() map[string]string {
	out := map[string]string{"stdout": a.stdout, "json": a.json, "html": a.html}
	if a.text != "" {
		out["text"] = a.text
	}
	return out
}

// compare runs `piace compare` through the CLI entry point, always
// requesting the JSON and HTML artifacts, and returns everything the run
// produced. Driving run() rather than compare.Workflow is the point of
// this suite: it exercises PEM loading, real mTLS handshakes, HTTP status
// and JSON decoding in the adapters, artifact writing, and the process
// exit code.
func (h *harness) compare(t *testing.T, extra ...string) artifacts {
	t.Helper()
	suffix := fmt.Sprintf("%d", h.pdb.count())
	jsonOut := h.path("report" + suffix + ".json")
	htmlOut := h.path("report" + suffix + ".html")

	args := append([]string{
		"compare",
		"--targets", h.path("targets.yaml"),
		"--services", h.path("services.yaml"),
		"--json-out", jsonOut,
		"--html-out", htmlOut,
	}, extra...)

	stdout, stderr, code := captureRun(t, args)

	result := artifacts{code: code, stdout: stdout, stderr: stderr}
	result.json = readIfExists(t, jsonOut)
	result.html = readIfExists(t, htmlOut)
	for i, arg := range extra {
		if arg == "--text-out" && i+1 < len(extra) {
			result.text = readIfExists(t, extra[i+1])
		}
	}
	return result
}

// captureRun invokes run() with stdout/stderr redirected to temporary
// files. run takes *os.File (it writes to the process's real streams in
// production), so capture goes through files rather than buffers.
func captureRun(t *testing.T, args []string) (stdout, stderr string, code exitcode.Code) {
	t.Helper()
	outFile, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer errFile.Close()

	code = run(args, outFile, errFile)
	return readFile(t, outFile.Name()), readFile(t, errFile.Name()), code
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(data)
}

func readIfExists(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(data)
}

// --- wire-shape builders -------------------------------------------------

// pdbFactset builds a PuppetDB factset response carrying a valid Puppet
// trusted-fact structure, so a v4 request has a `provided` trusted-fact
// source without any compiler lookup.
func pdbFactset(certname string, withTrusted bool) map[string]any {
	facts := []map[string]any{
		{"name": "osfamily", "value": "RedHat"},
		{"name": "memorysize_mb", "value": 2048},
	}
	if withTrusted {
		facts = append(facts, map[string]any{
			"name":  "trusted",
			"value": map[string]any{"certname": certname, "authenticated": "remote", "extensions": map[string]any{}},
		})
	}
	return map[string]any{
		"certname":           certname,
		"environment":        "production",
		"timestamp":          "2026-08-20T00:00:00Z",
		"producer_timestamp": "2026-08-20T00:00:00Z",
		"producer":           "puppet.example.test",
		"hash":               "sha256:factset-" + certname,
		"facts":              map[string]any{"href": "/pdb/query/v4/factsets/" + certname + "/facts", "data": facts},
	}
}

// resourceSpec describes one catalog resource in test terms.
type resourceSpec struct {
	Type       string
	Title      string
	Parameters map[string]any
}

// edgeSpec describes one catalog edge in test terms.
type edgeSpec struct {
	SourceType, SourceTitle string
	TargetType, TargetTitle string
}

// pdbCatalog builds a PuppetDB catalog response in the documented
// {href, data} expansion shape.
func pdbCatalog(certname, environment string, resources []resourceSpec, edges []edgeSpec) map[string]any {
	resourceData := make([]map[string]any, 0, len(resources))
	for _, r := range resources {
		resourceData = append(resourceData, map[string]any{
			"certname": certname, "type": r.Type, "title": r.Title,
			"parameters": nonNilParams(r.Parameters),
			// Fields requirements.md 5.9 requires dropped from the
			// semantic diff, served here so the drop is exercised.
			"tags": []string{"class", strings.ToLower(r.Type)},
			"file": "/etc/puppetlabs/code/site.pp", "line": 42, "exported": false,
		})
	}
	edgeData := make([]map[string]any, 0, len(edges))
	for _, e := range edges {
		edgeData = append(edgeData, map[string]any{
			"relationship": "contains",
			"source_type":  e.SourceType, "source_title": e.SourceTitle,
			"target_type": e.TargetType, "target_title": e.TargetTitle,
		})
	}
	return map[string]any{
		"certname": certname, "environment": environment,
		"version": "1755000000", "hash": "sha256:catalog-" + certname,
		"transaction_uuid": "11111111-1111-1111-1111-111111111111",
		"catalog_uuid":     "22222222-2222-2222-2222-222222222222",
		"code_id":          "abc123", "producer_timestamp": "2026-08-20T00:00:00Z",
		"producer":  "puppet.example.test",
		"resources": map[string]any{"href": "/resources", "data": resourceData},
		"edges":     map[string]any{"href": "/edges", "data": edgeData},
	}
}

// compilerCatalog builds a compiler catalog response in the documented
// catalog wire format v8 plain-array shape, keyed on "name" rather than
// "certname".
func compilerCatalog(certname, environment string, resources []resourceSpec, edges []edgeSpec) map[string]any {
	resourceList := make([]map[string]any, 0, len(resources))
	for _, r := range resources {
		resourceList = append(resourceList, map[string]any{
			"type": r.Type, "title": r.Title, "parameters": nonNilParams(r.Parameters),
			"tags": []string{"class", strings.ToLower(r.Type)},
			"file": "/etc/puppetlabs/code/site.pp", "line": 42, "exported": false,
		})
	}
	edgeList := make([]map[string]any, 0, len(edges))
	for _, e := range edges {
		edgeList = append(edgeList, map[string]any{
			"source":       map[string]any{"type": e.SourceType, "title": e.SourceTitle},
			"target":       map[string]any{"type": e.TargetType, "title": e.TargetTitle},
			"relationship": "contains",
		})
	}
	return map[string]any{
		"name": certname, "environment": environment, "version": 1755000001,
		"code_id": "def456", "catalog_uuid": "33333333-3333-3333-3333-333333333333",
		"transaction_uuid": "44444444-4444-4444-4444-444444444444",
		"resources":        resourceList, "edges": edgeList,
	}
}

func nonNilParams(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return p
}

// sensitiveWrapper is the Pcore generic-data encoding of a Puppet
// `Sensitive` value that internal/diff's doc.go documents.
//
// IMPORTANT: that shape is derived from Puppet's Ruby serializer source,
// not from a captured live response. Serving it here proves PIACE redacts
// the shape it assumes; it does NOT confirm the assumption. Task 12's
// "confirm against a rich-data-enabled compiler" remains outstanding.
func sensitiveWrapper(value string) map[string]any {
	return map[string]any{"__ptype": "Sensitive", "__pvalue": value}
}
