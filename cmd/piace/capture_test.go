package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example42/piace/internal/exitcode"
)

// newMTLSFixture generates a throwaway CA/leaf certificate pair for a
// PuppetDB test double, writing the CA bundle/client cert/private key to
// files under dir so they can be referenced from a --services YAML file
// exactly like a real deployment would.
func newMTLSFixture(t *testing.T, dir, host string) (caBundle, clientCert, privateKey string, serverTLSCert tls.Certificate) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "piace test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("creating CA certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parsing CA certificate: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		leafTemplate.IPAddresses = []net.IP{ip}
	} else {
		leafTemplate.DNSNames = []string{host}
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("creating leaf certificate: %v", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	leafCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshaling leaf key: %v", err)
	}
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})

	caBundle = filepath.Join(dir, "ca.pem")
	clientCert = filepath.Join(dir, "client.pem")
	privateKey = filepath.Join(dir, "client.key")
	if err := os.WriteFile(caBundle, caPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(ca): %v", err)
	}
	if err := os.WriteFile(clientCert, leafCertPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(cert): %v", err)
	}
	if err := os.WriteFile(privateKey, leafKeyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(key): %v", err)
	}

	serverTLSCert, err = tls.X509KeyPair(leafCertPEM, leafKeyPEM)
	if err != nil {
		t.Fatalf("building server tls.Certificate: %v", err)
	}
	return caBundle, clientCert, privateKey, serverTLSCert
}

// TestRun_CaptureFacts_EndToEnd exercises `piace capture facts` through
// run() against a fake mTLS PuppetDB server and a real target/services
// YAML file, verifying the full wiring (config resolution -> transport ->
// puppetdb.Adapter -> capture.Workflow -> snapshot.Write) produces a
// valid, loadable snapshot file and exits 0.
func TestRun_CaptureFacts_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	caBundle, clientCert, privateKey, serverTLSCert := newMTLSFixture(t, dir, "127.0.0.1")

	pool := x509.NewCertPool()
	caPEM, err := os.ReadFile(caBundle)
	if err != nil {
		t.Fatalf("ReadFile(ca): %v", err)
	}
	pool.AppendCertsFromPEM(caPEM)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"certname": "web-01.example.test",
			"environment": "production",
			"producer_timestamp": "2015-06-04T15:27:56.893Z",
			"producer": "compiler-01.example.test",
			"hash": "93253d31af6d718cf81f5bc028be2a671f23ed78",
			"facts": {"href": "/x", "data": []}
		}`))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	defer srv.Close()

	targetsYAML := `
version: 1
defaults:
  candidate:
    environment: feature-123
    catalog_api: v4
  facts:
    source: file
    file: snapshots/facts/{certname}.json
  baseline:
    source: puppetdb
    environment: production
  fail_on_diff: true
targets:
  - certname: web-01.example.test
`
	servicesYAML := `
version: 1
compiler:
  endpoint: ` + srv.URL + `
  ca_bundle: ` + caBundle + `
  client_cert: ` + clientCert + `
  private_key: ` + privateKey + `
puppetdb:
  endpoint: ` + srv.URL + `
  ca_bundle: ` + caBundle + `
  client_cert: ` + clientCert + `
  private_key: ` + privateKey + `
`
	targetsPath := filepath.Join(dir, "targets.yaml")
	servicesPath := filepath.Join(dir, "services.yaml")
	if err := os.WriteFile(targetsPath, []byte(targetsYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(targets): %v", err)
	}
	if err := os.WriteFile(servicesPath, []byte(servicesYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(services): %v", err)
	}

	stdoutFile, stderrFile := newCaptureFiles(t, dir)
	got := run([]string{"capture", "facts", "--targets", targetsPath, "--services", servicesPath}, stdoutFile, stderrFile)
	if got != exitcode.Success {
		t.Fatalf("run(capture facts) = %d, want %d\nstderr:\n%s", got, exitcode.Success, readCaptureFile(t, stderrFile))
	}

	snapshotPath := filepath.Join(dir, "snapshots", "facts", "web-01.example.test.json")
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("Stat(snapshot): %v", err)
	}
}

// TestRun_CaptureCatalog_EndToEnd_CompilationFailureReportedNotCrash
// verifies `piace capture catalog` reports a target failure (rather than
// crashing or silently succeeding, and never writing a snapshot) through
// the full run() entry point when the compiler cannot satisfy the
// request. This fixture's fake server only ever returns a PuppetDB
// factset-shaped body, never a valid v4 catalog response nor a "trusted"
// fact PIACE could forward, so the target's v4 request fails internal/compiler's
// trusted-fact policy (internal/compiler) before any catalog is accepted.
func TestRun_CaptureCatalog_EndToEnd_CompilationFailureReportedNotCrash(t *testing.T) {
	dir := t.TempDir()
	caBundle, clientCert, privateKey, serverTLSCert := newMTLSFixture(t, dir, "127.0.0.1")

	pool := x509.NewCertPool()
	caPEM, err := os.ReadFile(caBundle)
	if err != nil {
		t.Fatalf("ReadFile(ca): %v", err)
	}
	pool.AppendCertsFromPEM(caPEM)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"certname": "web-01.example.test",
			"environment": "production",
			"producer_timestamp": "2015-06-04T15:27:56.893Z",
			"producer": "compiler-01.example.test",
			"hash": "93253d31af6d718cf81f5bc028be2a671f23ed78",
			"facts": {"href": "/x", "data": []}
		}`))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	defer srv.Close()

	targetsYAML := `
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
	servicesYAML := `
version: 1
compiler:
  endpoint: ` + srv.URL + `
  ca_bundle: ` + caBundle + `
  client_cert: ` + clientCert + `
  private_key: ` + privateKey + `
puppetdb:
  endpoint: ` + srv.URL + `
  ca_bundle: ` + caBundle + `
  client_cert: ` + clientCert + `
  private_key: ` + privateKey + `
`
	targetsPath := filepath.Join(dir, "targets.yaml")
	servicesPath := filepath.Join(dir, "services.yaml")
	if err := os.WriteFile(targetsPath, []byte(targetsYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(targets): %v", err)
	}
	if err := os.WriteFile(servicesPath, []byte(servicesYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(services): %v", err)
	}

	stdoutFile, stderrFile := newDiscardFiles(t)
	got := run([]string{"capture", "catalog", "--targets", targetsPath, "--services", servicesPath, "--environment", "feature-123"}, stdoutFile, stderrFile)
	if got != exitcode.OperationalError {
		t.Fatalf("run(capture catalog) = %d, want %d", got, exitcode.OperationalError)
	}

	snapshotPath := filepath.Join(dir, "snapshots", "catalogs", "web-01.example.test.json")
	if _, err := os.Stat(snapshotPath); err == nil {
		t.Error("expected no snapshot to be written when the compiler is not implemented")
	}
}

// newDiscardFiles opens /dev/null (or the OS equivalent) twice for use as
// run()'s stdout/stderr in a test, since run's signature requires *os.File
// rather than io.Writer.
func newDiscardFiles(t *testing.T) (stdout, stderr *os.File) {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { f.Close() })
	return f, f
}

// newCaptureFiles opens two real temp files under dir for use as run()'s
// stdout/stderr, so a failing test can print what the CLI actually wrote.
func newCaptureFiles(t *testing.T, dir string) (stdout, stderr *os.File) {
	t.Helper()
	out, err := os.Create(filepath.Join(dir, "stdout.log"))
	if err != nil {
		t.Fatalf("creating stdout capture file: %v", err)
	}
	errF, err := os.Create(filepath.Join(dir, "stderr.log"))
	if err != nil {
		t.Fatalf("creating stderr capture file: %v", err)
	}
	t.Cleanup(func() { out.Close(); errF.Close() })
	return out, errF
}

func readCaptureFile(t *testing.T, f *os.File) string {
	t.Helper()
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", f.Name(), err)
	}
	return string(data)
}
