package resolve

import (
	"strings"
	"testing"
)

func TestDecodeTargetFile_RejectsUnknownFields(t *testing.T) {
	yaml := `
version: 1
defaults:
  candidate:
    environment: feature-123
    catalog_api: v4
targets:
  - certname: web-01.example.test
    unexpected_field: oops
`
	_, err := decodeTargetFile(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestDecodeTargetFile_AcceptsKnownShape(t *testing.T) {
	yaml := `
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
targets:
  - certname: web-01.example.test
`
	tf, err := decodeTargetFile(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("decodeTargetFile: %v", err)
	}
	if tf.Version != 1 {
		t.Errorf("Version = %d, want 1", tf.Version)
	}
	if len(tf.Targets) != 1 || tf.Targets[0].Certname != "web-01.example.test" {
		t.Errorf("Targets = %+v", tf.Targets)
	}
}

func TestDecodeServicesFile_RejectsUnknownFields(t *testing.T) {
	yaml := `
version: 1
compiler:
  endpoint: https://compiler.example.test:8140
  ca_bundle: /etc/piace/ca.pem
  client_cert: /etc/piace/client.pem
  private_key: /etc/piace/client.key
  bearer_token: nope
puppetdb:
  endpoint: https://puppetdb.example.test:8081
  ca_bundle: /etc/piace/ca.pem
  client_cert: /etc/piace/client.pem
  private_key: /etc/piace/client.key
`
	_, err := decodeServicesFile(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for unknown field bearer_token, got nil")
	}
}

func TestDecodeServicesFile_AcceptsKnownShape(t *testing.T) {
	yaml := `
version: 1
compiler:
  endpoint: https://compiler.example.test:8140
  ca_bundle: /etc/piace/ca.pem
  client_cert: /etc/piace/client.pem
  private_key: /etc/piace/client.key
puppetdb:
  endpoint: https://puppetdb.example.test:8081
  ca_bundle: /etc/piace/ca.pem
  client_cert: /etc/piace/client.pem
  private_key: /etc/piace/client.key
`
	sf, err := decodeServicesFile(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("decodeServicesFile: %v", err)
	}
	if sf.Compiler.Endpoint != "https://compiler.example.test:8140" {
		t.Errorf("Compiler.Endpoint = %q", sf.Compiler.Endpoint)
	}
}

// TestDecode_RejectsASecondDocument: a YAML decoder reads the first
// document of a stream and leaves the rest, so a file whose real
// configuration sits after a `---` separator would otherwise be read as
// whatever came before it, with no indication that anything was skipped.
func TestDecode_RejectsASecondDocument(t *testing.T) {
	targets := `version: 1
defaults:
  candidate:
    environment: production
    catalog_api: v4
  facts:
    source: puppetdb
  baseline:
    source: puppetdb
    environment: production
targets:
  - certname: web-01.example.test
---
version: 1
targets:
  - certname: the-one-you-meant.example.test
`
	if _, err := decodeTargetFile(strings.NewReader(targets)); err == nil {
		t.Fatal("accepted a target file carrying two documents")
	} else if !strings.Contains(err.Error(), "more than one YAML document") {
		t.Errorf("error = %v, want it to name the second document", err)
	}

	services := `version: 1
compiler:
  endpoint: https://compiler.example.test:8140
---
version: 1
`
	if _, err := decodeServicesFile(strings.NewReader(services)); err == nil {
		t.Fatal("accepted a services file carrying two documents")
	}
}

func TestDecode_RejectsAnEmptyFile(t *testing.T) {
	if _, err := decodeTargetFile(strings.NewReader("")); err == nil {
		t.Fatal("accepted an empty target file")
	}
}

func TestDecode_RejectsAnOversizedFile(t *testing.T) {
	// Valid YAML, just far too much of it: the point is that the limit is
	// applied before the decoder allocates what the file contains.
	var b strings.Builder
	b.WriteString("version: 1\ntargets:\n")
	for b.Len() <= MaxConfigBytes {
		b.WriteString("  - certname: node-with-a-reasonably-long-name.example.test\n")
	}
	if _, err := decodeTargetFile(strings.NewReader(b.String())); err == nil {
		t.Fatal("accepted a target file past the size limit")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want it to name the limit", err)
	}
}
