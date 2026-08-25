package config

import (
	"encoding/json"
	"testing"
)

// TestServicesFile_JSONRoundTrip verifies the versioned services-file
// schema marshals and unmarshals via encoding/json without field loss.
func TestServicesFile_JSONRoundTrip(t *testing.T) {
	original := ServicesFile{
		Version: ServicesFileVersion,
		Compiler: ServiceEndpoint{
			Endpoint:   "https://compiler.example.test:8140",
			CABundle:   "/etc/piace/compiler-ca.pem",
			ClientCert: "/etc/piace/compiler-client.pem",
			PrivateKey: "/etc/piace/compiler-client.key",
		},
		PuppetDB: ServiceEndpoint{
			Endpoint:   "https://puppetdb.example.test:8081",
			CABundle:   "/etc/piace/puppetdb-ca.pem",
			ClientCert: "/etc/piace/puppetdb-client.pem",
			PrivateKey: "/etc/piace/puppetdb-client.key",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ServicesFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded != original {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", decoded, original)
	}
	if decoded.Version != ServicesFileVersion {
		t.Errorf("Version = %d, want %d", decoded.Version, ServicesFileVersion)
	}
}
