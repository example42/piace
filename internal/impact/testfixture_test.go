package impact

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
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/transport"
)

// --- minimal local mTLS test fixture ---
//
// internal/transport's tlsFixture/newMTLSTestServer helpers are
// unexported test-only helpers in package transport, not reachable from
// this package's test package. internal/puppetdb/adapter_test.go,
// internal/compiler/testfixture_test.go, and
// internal/filecontent/testfixture_test.go already established the
// convention of a minimal local copy for exactly this reason; this is
// that same convention applied here.

type tlsFixture struct {
	caBundle      string
	clientCert    string
	privateKey    string
	serverTLSCert tls.Certificate
}

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
	if err := os.WriteFile(caBundle, caPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(ca): %v", err)
	}
	if err := os.WriteFile(clientCert, leafCertPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(cert): %v", err)
	}
	if err := os.WriteFile(privateKey, leafKeyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(key): %v", err)
	}

	serverTLSCert, err := tls.X509KeyPair(leafCertPEM, leafKeyPEM)
	if err != nil {
		t.Fatalf("building server tls.Certificate: %v", err)
	}

	return &tlsFixture{
		caBundle:      caBundle,
		clientCert:    clientCert,
		privateKey:    privateKey,
		serverTLSCert: serverTLSCert,
	}
}

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

func (f *tlsFixture) endpoint(t *testing.T, rawURL string) resolve.Endpoint {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", rawURL, err)
	}
	return resolve.Endpoint{
		URL:        u,
		CABundle:   f.caBundle,
		ClientCert: f.clientCert,
		PrivateKey: f.privateKey,
	}
}

// newMTLSTestServer starts an httptest.Server requiring and verifying the
// fixture's client certificate.
func newMTLSTestServer(t *testing.T, fixture *tlsFixture, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = fixture.serverTLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// newQuerier builds a *transport.Client and *Querier pointed at srv
// using fixture's certificates.
func newQuerier(t *testing.T, fixture *tlsFixture, srv *httptest.Server) *Querier {
	t.Helper()
	ep := fixture.endpoint(t, srv.URL)
	client, err := transport.NewClient(ep)
	if err != nil {
		t.Fatalf("transport.NewClient: %v", err)
	}
	return NewQuerier(client, ep.URL)
}
