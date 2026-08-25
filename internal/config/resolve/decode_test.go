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
