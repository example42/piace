package resolve

import (
	"os"
	"path/filepath"
	"testing"
)

func writeServices(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing services file: %v", err)
	}
	return path
}

// Slice 6.1: `piace explain` needs no mTLS identity and constructs no
// compiler or PuppetDB client, so a services file naming no Puppet
// infrastructure at all is valid for it.
func TestAServicesFileWithOnlyAnInferenceSectionLoads(t *testing.T) {
	t.Setenv("PIACE_TEST_TOKEN", "s3cret")
	path := writeServices(t, `
version: 1
inference:
  endpoint: https://api.example.com/v1/chat/completions
  model: some-model
  token_env: PIACE_TEST_TOKEN
`)

	in, err := LoadInferenceFile(path)
	if err != nil {
		t.Fatalf("LoadInferenceFile: %v", err)
	}
	if in.Token != "s3cret" {
		t.Errorf("Token = %q", in.Token)
	}
	if in.Authority != "api.example.com" {
		t.Errorf("Authority = %q", in.Authority)
	}
	if in.Timeout != DefaultInferenceTimeout {
		t.Errorf("Timeout = %v, want the default", in.Timeout)
	}
	if !in.Assess.Pseudonymize || !in.Assess.StructuredOutput {
		t.Errorf("defaults are not on: %+v", in.Assess)
	}
}

// Slice 6.2: a token is referenced, never written. There is no field to
// put one in, so an attempt is an unknown field and is refused.
func TestInferenceCredentialsAreReferencedNeverInlined(t *testing.T) {
	t.Setenv("PIACE_TEST_TOKEN", "s3cret")
	for name, content := range map[string]string{
		"inline token": `
version: 1
inference:
  endpoint: https://api.example.com/v1/chat/completions
  model: m
  token: "sk-inline-secret"
`,
		"no token reference": `
version: 1
inference:
  endpoint: https://api.example.com/v1/chat/completions
  model: m
`,
		"both token references": `
version: 1
inference:
  endpoint: https://api.example.com/v1/chat/completions
  model: m
  token_env: PIACE_TEST_TOKEN
  token_file: /etc/piace/token
`,
		"unset environment variable": `
version: 1
inference:
  endpoint: https://api.example.com/v1/chat/completions
  model: m
  token_env: PIACE_TEST_DEFINITELY_UNSET
`,
		"insecure endpoint": `
version: 1
inference:
  endpoint: http://api.example.com/v1/chat/completions
  model: m
  token_env: PIACE_TEST_TOKEN
`,
		"no model": `
version: 1
inference:
  endpoint: https://api.example.com/v1/chat/completions
  token_env: PIACE_TEST_TOKEN
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadInferenceFile(writeServices(t, content)); err == nil {
				t.Errorf("LoadInferenceFile accepted %s", name)
			}
		})
	}
}

// Slice 6.3: the inference section is optional for everything else. A
// services file carrying one still resolves for `piace compare`, which
// ignores it and contacts no inference service.
func TestCompareIgnoresTheInferenceSection(t *testing.T) {
	t.Setenv("PIACE_TEST_TOKEN", "s3cret")
	path := writeServices(t, `
version: 1
compiler:
  endpoint: https://compiler.example.test:8140
  ca_bundle:   /etc/piace/ca.pem
  client_cert: /etc/piace/reader.pem
  private_key: /etc/piace/reader.key
puppetdb:
  endpoint: https://puppetdb.example.test:8081
  ca_bundle:   /etc/piace/ca.pem
  client_cert: /etc/piace/reader.pem
  private_key: /etc/piace/reader.key
inference:
  endpoint: https://api.example.com/v1/chat/completions
  model: some-model
  token_env: PIACE_TEST_TOKEN
`)

	svc, err := LoadServicesFile(path)
	if err != nil {
		t.Fatalf("LoadServicesFile: %v", err)
	}
	if svc.Compiler.URL == nil || svc.PuppetDB.URL == nil {
		t.Fatalf("compiler/puppetdb did not resolve: %+v", svc)
	}
	if _, err := LoadInferenceFile(path); err != nil {
		t.Errorf("LoadInferenceFile on the same file: %v", err)
	}
}

// An absent inference section is refused for explain, and refused by
// name: a missing block should not read as a missing endpoint.
func TestExplainRefusesAServicesFileWithNoInferenceSection(t *testing.T) {
	path := writeServices(t, "version: 1\n")
	if _, err := LoadInferenceFile(path); err == nil {
		t.Fatal("LoadInferenceFile accepted a file with no inference section")
	}
}
