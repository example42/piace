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
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tlsFixture is a temporary, in-test self-signed CA plus one leaf
// certificate/key pair signed by it, written to PEM files. It mirrors
// internal/transport's own fixture; a _test.go helper cannot be shared
// across packages, so the generation is repeated rather than promoted
// into a non-test package that would then ship in the release binary.
type tlsFixture struct {
	dir        string
	caBundle   string
	clientCert string
	privateKey string
	// clientKeyPEM and clientCertPEM are the raw PEM bytes, retained so
	// the disclosure scan can search a rendered report for the actual
	// private material rather than only for its path.
	clientCertPEM []byte
	clientKeyPEM  []byte
	serverTLSCert tls.Certificate
}

func newTLSFixture(t *testing.T) *tlsFixture {
	t.Helper()
	dir := t.TempDir()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "piace acceptance CA"},
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
		Subject:      pkix.Name{CommonName: "piace-catalog-reader"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		// The acceptance services listen on 127.0.0.1; the leaf must be
		// valid for that address or the client's own hostname
		// verification (task 3) rejects the handshake.
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:    []string{"localhost"},
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

	f := &tlsFixture{
		dir:           dir,
		caBundle:      filepath.Join(dir, "ca.pem"),
		clientCert:    filepath.Join(dir, "reader.pem"),
		privateKey:    filepath.Join(dir, "reader.key"),
		clientCertPEM: leafCertPEM,
		clientKeyPEM:  leafKeyPEM,
	}
	writeFixtureFile(t, f.caBundle, caPEM)
	writeFixtureFile(t, f.clientCert, leafCertPEM)
	writeFixtureFile(t, f.privateKey, leafKeyPEM)

	serverTLSCert, err := tls.X509KeyPair(leafCertPEM, leafKeyPEM)
	if err != nil {
		t.Fatalf("building server tls.Certificate: %v", err)
	}
	f.serverTLSCert = serverTLSCert
	return f
}

func writeFixtureFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// serverTLSConfig returns a TLS config requiring and verifying a client
// certificate issued by this fixture's CA, so every acceptance request is
// a real mTLS handshake rather than server-side TLS only.
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
