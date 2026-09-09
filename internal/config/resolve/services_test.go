package resolve

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/example42/piace/internal/config"
)

func validServicesFile() config.ServicesFile {
	return config.ServicesFile{
		Version: config.ServicesFileVersion,
		Compiler: config.ServiceEndpoint{
			Endpoint:   "https://compiler.example.test:8140",
			CABundle:   "/etc/piace/compiler-ca.pem",
			ClientCert: "/etc/piace/compiler-client.pem",
			PrivateKey: "/etc/piace/compiler-client.key",
		},
		PuppetDB: config.ServiceEndpoint{
			Endpoint:   "https://puppetdb.example.test:8081",
			CABundle:   "/etc/piace/puppetdb-ca.pem",
			ClientCert: "/etc/piace/puppetdb-client.pem",
			PrivateKey: "/etc/piace/puppetdb-client.key",
		},
	}
}

func TestResolveServices_Valid(t *testing.T) {
	svc, err := ResolveServices(validServicesFile(), "/etc/piace", ServiceSet{Compiler: true, PuppetDB: true})
	if err != nil {
		t.Fatalf("ResolveServices: %v", err)
	}
	if svc.Compiler.URL == nil || svc.Compiler.URL.Host != "compiler.example.test:8140" {
		t.Errorf("Compiler.URL = %+v", svc.Compiler.URL)
	}
	if svc.PuppetDB.URL == nil || svc.PuppetDB.URL.Scheme != "https" {
		t.Errorf("PuppetDB.URL = %+v", svc.PuppetDB.URL)
	}
	if svc.Compiler.ClientCert != "/etc/piace/compiler-client.pem" {
		t.Errorf("Compiler.ClientCert = %q", svc.Compiler.ClientCert)
	}
}

func TestResolveServices_UnsupportedVersion(t *testing.T) {
	sf := validServicesFile()
	sf.Version = 2
	_, err := ResolveServices(sf, "/etc/piace", ServiceSet{Compiler: true, PuppetDB: true})
	if err == nil {
		t.Fatal("expected error for unsupported version, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error = %v", err)
	}
}

func TestResolveServices_NonHTTPSEndpointRejected(t *testing.T) {
	tests := []string{
		"http://compiler.example.test:8140",
		"ftp://compiler.example.test",
		"compiler.example.test:8140", // no scheme
		"",
	}
	for _, endpoint := range tests {
		t.Run(endpoint, func(t *testing.T) {
			sf := validServicesFile()
			sf.Compiler.Endpoint = endpoint
			_, err := ResolveServices(sf, "/etc/piace", ServiceSet{Compiler: true, PuppetDB: true})
			if err == nil {
				t.Fatalf("expected error for endpoint %q, got nil", endpoint)
			}
		})
	}
}

func TestResolveServices_EmptyTLSPathsRejected(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.ServiceEndpoint)
	}{
		{name: "empty ca_bundle", mutate: func(e *config.ServiceEndpoint) { e.CABundle = "" }},
		{name: "empty client_cert", mutate: func(e *config.ServiceEndpoint) { e.ClientCert = "" }},
		{name: "empty private_key", mutate: func(e *config.ServiceEndpoint) { e.PrivateKey = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sf := validServicesFile()
			tc.mutate(&sf.Compiler)
			_, err := ResolveServices(sf, "/etc/piace", ServiceSet{Compiler: true, PuppetDB: true})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestResolveServices_AccumulatesErrorsAcrossBothSections(t *testing.T) {
	sf := validServicesFile()
	sf.Compiler.Endpoint = "http://insecure.example.test"
	sf.PuppetDB.CABundle = ""
	_, err := ResolveServices(sf, "/etc/piace", ServiceSet{Compiler: true, PuppetDB: true})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if len(ve.Problems()) < 2 {
		t.Errorf("Problems() = %v, want at least 2 problems (one per section)", ve.Problems())
	}
	joined := strings.Join(ve.Problems(), " | ")
	if !strings.Contains(joined, "compiler") || !strings.Contains(joined, "puppetdb") {
		t.Errorf("Problems() = %v, want mentions of both compiler and puppetdb", ve.Problems())
	}
}

func TestResolveServices_RelativePathsResolveAgainstTheServicesFile(t *testing.T) {
	sf := validServicesFile()
	sf.Compiler.CABundle = "ca.pem"
	sf.Compiler.ClientCert = "tls/reader.pem"

	svc, err := ResolveServices(sf, "/srv/ci/piace", ServiceSet{Compiler: true, PuppetDB: true})
	if err != nil {
		t.Fatalf("ResolveServices: %v", err)
	}
	if got, want := svc.Compiler.CABundle, filepath.Join("/srv/ci/piace", "ca.pem"); got != want {
		t.Errorf("CABundle = %q, want %q", got, want)
	}
	if got, want := svc.Compiler.ClientCert, filepath.Join("/srv/ci/piace", "tls/reader.pem"); got != want {
		t.Errorf("ClientCert = %q, want %q", got, want)
	}
	if got, want := svc.Compiler.PrivateKey, "/etc/piace/compiler-client.key"; got != want {
		t.Errorf("PrivateKey = %q, want %q: an absolute path is taken as written", got, want)
	}
}

func TestResolveServices_EnvNamedTLSPaths(t *testing.T) {
	t.Setenv("PIACE_CA_BUNDLE", "/run/piace/ca.pem")

	sf := validServicesFile()
	sf.Compiler.CABundle = ""
	sf.Compiler.CABundleEnv = "PIACE_CA_BUNDLE"

	svc, err := ResolveServices(sf, "/srv/ci/piace", ServiceSet{Compiler: true, PuppetDB: true})
	if err != nil {
		t.Fatalf("ResolveServices: %v", err)
	}
	if got, want := svc.Compiler.CABundle, "/run/piace/ca.pem"; got != want {
		t.Errorf("CABundle = %q, want %q", got, want)
	}
}

func TestResolveServices_TLSReferenceErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		mutate  func(*config.ServiceEndpoint)
		wantMsg string
	}{
		{
			name:    "both forms named",
			env:     map[string]string{"PIACE_CA_BUNDLE": "/run/piace/ca.pem"},
			mutate:  func(e *config.ServiceEndpoint) { e.CABundleEnv = "PIACE_CA_BUNDLE" },
			wantMsg: "not both",
		},
		{
			name:    "neither form named",
			mutate:  func(e *config.ServiceEndpoint) { e.PrivateKey = "" },
			wantMsg: "set private_key or private_key_env",
		},
		{
			name:    "variable unset",
			mutate:  func(e *config.ServiceEndpoint) { e.ClientCert = ""; e.ClientCertEnv = "PIACE_UNSET_CERT" },
			wantMsg: "is unset or empty",
		},
		{
			name:    "variable holds a relative path",
			env:     map[string]string{"PIACE_CA_BUNDLE": "ca.pem"},
			mutate:  func(e *config.ServiceEndpoint) { e.CABundle = ""; e.CABundleEnv = "PIACE_CA_BUNDLE" },
			wantMsg: "must hold an absolute path",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			sf := validServicesFile()
			tc.mutate(&sf.Compiler)
			_, err := ResolveServices(sf, "/etc/piace", ServiceSet{Compiler: true, PuppetDB: true})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantMsg)
			}
		})
	}
}
