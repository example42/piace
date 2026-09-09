package resolve

import (
	"os"
	"path/filepath"
	"testing"
)

const validTargetsYAML = `
version: 1
defaults:
  candidate:
    environment: feature-123
    catalog_api: v4
  facts:
    source: puppetdb
  baseline:
    source: file
    environment: production
    file: snapshots/catalogs/{certname}.json
  fail_on_diff: true
targets:
  - certname: web-01.example.test
`

const validServicesYAML = `
version: 1
compiler:
  endpoint: https://compiler.example.test:8140
  ca_bundle: compiler-ca.pem
  client_cert: compiler-client.pem
  private_key: compiler-client.key
puppetdb:
  endpoint: https://puppetdb.example.test:8081
  ca_bundle: puppetdb-ca.pem
  client_cert: puppetdb-client.pem
  private_key: puppetdb-client.key
`

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", p, err)
	}
	return p
}

func TestLoad_ValidFilesResolveTargetsRelativeToTargetFileDir(t *testing.T) {
	dir := t.TempDir()
	targetsPath := writeTempFile(t, dir, "targets.yaml", validTargetsYAML)
	servicesPath := writeTempFile(t, dir, "services.yaml", validServicesYAML)

	cfg, err := Load(CommandCompare, targetsPath, servicesPath, Overrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Targets) != 1 {
		t.Fatalf("len(Targets) = %d, want 1", len(cfg.Targets))
	}
	wantFile := filepath.Join(dir, "snapshots/catalogs/web-01.example.test.json")
	if cfg.Targets[0].Baseline.File != wantFile {
		t.Errorf("Baseline.File = %q, want %q", cfg.Targets[0].Baseline.File, wantFile)
	}
	if cfg.Services.Compiler.URL == nil {
		t.Errorf("Services.Compiler.URL is nil")
	}
}

func TestLoad_AccumulatesErrorsFromBothFiles(t *testing.T) {
	dir := t.TempDir()
	invalidTargets := `
version: 1
targets:
  - certname: ""
`
	invalidServices := `
version: 1
compiler:
  endpoint: http://insecure.example.test
  ca_bundle: ca.pem
  client_cert: client.pem
  private_key: client.key
puppetdb:
  endpoint: https://puppetdb.example.test:8081
  ca_bundle: ca.pem
  client_cert: client.pem
  private_key: client.key
`
	targetsPath := writeTempFile(t, dir, "targets.yaml", invalidTargets)
	servicesPath := writeTempFile(t, dir, "services.yaml", invalidServices)

	_, err := Load(CommandCompare, targetsPath, servicesPath, Overrides{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if len(ve.Problems()) < 2 {
		t.Errorf("Problems() = %v, want problems from both files", ve.Problems())
	}
}

func TestLoad_MissingFileIsError(t *testing.T) {
	dir := t.TempDir()
	servicesPath := writeTempFile(t, dir, "services.yaml", validServicesYAML)
	_, err := Load(CommandCompare, filepath.Join(dir, "does-not-exist.yaml"), servicesPath, Overrides{})
	if err == nil {
		t.Fatal("expected error for missing target file, got nil")
	}
}
