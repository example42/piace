package transport

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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example42/piace/internal/config/resolve"
)

// tlsFixture is a temporary, in-test self-signed CA plus one leaf
// certificate/key pair signed by it, written to PEM files. Tests use this
// instead of checked-in certificate files, per the task brief.
type tlsFixture struct {
	dir        string
	caBundle   string // path to CA certificate PEM
	clientCert string // path to leaf certificate PEM (signed by the CA)
	privateKey string // path to leaf private key PEM
	// serverTLSCert is a full server-side certificate (leaf cert + private
	// key) usable directly as the httptest.Server's TLS certificate; it is
	// signed by the same CA so the test client trusts it.
	serverTLSCert tls.Certificate
}

// newTLSFixture generates a fresh ECDSA CA and one leaf certificate valid
// for host, then writes the CA bundle, leaf certificate, and leaf private
// key to PEM files in a temporary directory.
func newTLSFixture(t *testing.T, host string) *tlsFixture {
	t.Helper()
	dir := t.TempDir()

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

	caBundle := filepath.Join(dir, "ca.pem")
	clientCert := filepath.Join(dir, "client.pem")
	privateKey := filepath.Join(dir, "client.key")
	writeFixtureFile(t, caBundle, caPEM)
	writeFixtureFile(t, clientCert, leafCertPEM)
	writeFixtureFile(t, privateKey, leafKeyPEM)

	serverTLSCert, err := tls.X509KeyPair(leafCertPEM, leafKeyPEM)
	if err != nil {
		t.Fatalf("building server tls.Certificate: %v", err)
	}

	return &tlsFixture{
		dir:           dir,
		caBundle:      caBundle,
		clientCert:    clientCert,
		privateKey:    privateKey,
		serverTLSCert: serverTLSCert,
	}
}

func writeFixtureFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// endpointFor builds a resolve.Endpoint for rawURL using this fixture's CA
// bundle, client certificate, and private key paths.
func (f *tlsFixture) endpointFor(t *testing.T, rawURL string) resolve.Endpoint {
	t.Helper()
	u := mustParseURL(t, rawURL)
	return resolve.Endpoint{
		URL:        u,
		CABundle:   f.caBundle,
		ClientCert: f.clientCert,
		PrivateKey: f.privateKey,
	}
}

// serverTLSConfig returns a TLS config an httptest.Server can use so its
// certificate is trusted by a Client built from this fixture, and so it
// requires and verifies the client certificate this fixture issues (a real
// mTLS handshake, not just server-side TLS).
func (f *tlsFixture) serverTLSConfig() *tls.Config {
	pool := x509.NewCertPool()
	caPEM, err := os.ReadFile(f.caBundle)
	if err != nil {
		panic(err)
	}
	pool.AppendCertsFromPEM(caPEM)
	return &tls.Config{
		Certificates: []tls.Certificate{f.serverTLSCert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
}
