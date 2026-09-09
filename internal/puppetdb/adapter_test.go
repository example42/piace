package puppetdb

import (
	"context"
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

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/transport"
)

// --- minimal local mTLS test fixture ---
//
// internal/transport's tlsFixture/newMTLSTestServer helpers are unexported
// test-only helpers in package transport, not reachable from this
// package's test package, so this is a minimal local equivalent.

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

// newMTLSTestServer starts an httptest.Server requiring and verifying
// the fixture's client certificate, and additionally asserts, via
// t.Errorf rather than t.Fatalf since it runs in the server goroutine,
// that every request it receives uses GET. That is the
// no-write-endpoint-is-ever-attempted check, applied uniformly across
// every test in this file rather than as one dedicated test.
func newMTLSTestServer(t *testing.T, fixture *tlsFixture, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("received non-GET request: %s %s", r.Method, r.URL.Path)
		}
		handler(w, r)
	}))
	srv.TLS = fixture.serverTLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// newAdapter builds a *transport.Client and *Adapter pointed at srv using
// fixture's certificates.
func newAdapter(t *testing.T, fixture *tlsFixture, srv *httptest.Server) *Adapter {
	t.Helper()
	ep := fixture.endpoint(t, srv.URL)
	client, err := transport.NewClient(ep)
	if err != nil {
		t.Fatalf("transport.NewClient: %v", err)
	}
	return NewAdapter(client, ep.URL)
}

func puppetdbTarget(certname, baselineEnv string) resolve.Target {
	return resolve.Target{
		Certname: certname,
		Facts:    resolve.Facts{Source: config.FactSourcePuppetDB},
		Baseline: resolve.Baseline{
			Source:      config.BaselineSourcePuppetDB,
			Environment: baselineEnv,
		},
	}
}

func TestAdapter_Load_SuccessfulFactsetRetrieval(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	const body = `{
		"certname": "web-01.example.test",
		"environment": "production",
		"timestamp": "2015-06-04T15:27:56.979Z",
		"producer_timestamp": "2015-06-04T15:27:56.893Z",
		"producer": "compiler-01.example.test",
		"hash": "93253d31af6d718cf81f5bc028be2a671f23ed78",
		"facts": {"href": "/pdb/query/v4/factsets/web-01.example.test/facts", "data": []}
	}`
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pdb/query/v4/factsets/web-01.example.test" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})
	adapter := newAdapter(t, fixture, srv)

	fs, prov, diag := adapter.Load(context.Background(), puppetdbTarget("web-01.example.test", "production"))
	if diag != nil {
		t.Fatalf("Load returned diagnostic: %+v", diag)
	}
	if fs.Certname != "web-01.example.test" {
		t.Errorf("Certname = %q", fs.Certname)
	}
	want := model.SourceProvenance{
		Kind:              model.SourceKindPuppetDB,
		Certname:          "web-01.example.test",
		Environment:       "production",
		ProducerTimestamp: "2015-06-04T15:27:56.893Z",
		CatalogIdentity:   "93253d31af6d718cf81f5bc028be2a671f23ed78",
		Producer:          "compiler-01.example.test",
	}
	if prov != want {
		t.Errorf("provenance = %+v, want %+v", prov, want)
	}
}

func TestAdapter_LoadBaseline_SuccessfulCatalogRetrieval(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	const body = `{
		"certname": "web-01.example.test",
		"version": "e4c339f",
		"environment": "production",
		"hash": "62cdc40a78750144b1e1ee06638ac2dd0eeb9a46",
		"transaction_uuid": "53b72442-3b73-11e3-94a8-1b34ef7fdc95",
		"catalog_uuid": null,
		"code_id": null,
		"producer_timestamp": "2014-10-13T20:46:00.000Z",
		"producer": "compiler-01.example.test",
		"resources": {"href": "/x", "data": []},
		"edges": {"href": "/x", "data": []}
	}`
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pdb/query/v4/catalogs/web-01.example.test" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})
	adapter := newAdapter(t, fixture, srv)

	cat, prov, diag := adapter.LoadBaseline(context.Background(), puppetdbTarget("web-01.example.test", "production"))
	if diag != nil {
		t.Fatalf("LoadBaseline returned diagnostic: %+v", diag)
	}
	if cat.Certname != "web-01.example.test" || cat.Version != "e4c339f" {
		t.Errorf("catalog = %+v", cat)
	}
	want := model.SourceProvenance{
		Kind:              model.SourceKindPuppetDB,
		Certname:          "web-01.example.test",
		Environment:       "production",
		ProducerTimestamp: "2014-10-13T20:46:00.000Z",
		CatalogIdentity:   "62cdc40a78750144b1e1ee06638ac2dd0eeb9a46",
		Producer:          "compiler-01.example.test",
	}
	if prov != want {
		t.Errorf("provenance = %+v, want %+v", prov, want)
	}
}

func TestAdapter_LoadBaseline_EnvironmentMismatchRejected(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	const body = `{
		"certname": "web-01.example.test",
		"version": "e4c339f",
		"environment": "feature-999",
		"hash": "62cdc40a78750144b1e1ee06638ac2dd0eeb9a46",
		"producer_timestamp": "2014-10-13T20:46:00.000Z",
		"producer": "compiler-01.example.test",
		"resources": {"href": "/x", "data": []},
		"edges": {"href": "/x", "data": []}
	}`
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})
	adapter := newAdapter(t, fixture, srv)

	// Baseline environment configured as "production", but PuppetDB's latest
	// catalog for this certname is recorded under "feature-999": this target
	// must fail before diffing.
	_, _, diag := adapter.LoadBaseline(context.Background(), puppetdbTarget("web-01.example.test", "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for baseline environment mismatch, got nil")
	}
	if diag.Operation != model.OperationLoadBaseline {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationLoadBaseline)
	}
	if diag.Certname != "web-01.example.test" {
		t.Errorf("Certname = %q", diag.Certname)
	}
}

func TestAdapter_Load_NotFound(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		// Exercises the documented not-found body shape
		// ({"error": "..."}) accompanied by a 200 status, per doc.go's
		// noted ambiguity about which status code PuppetDB actually uses.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error": "No information is known about factset missing.example.test"}`))
	})
	adapter := newAdapter(t, fixture, srv)

	_, _, diag := adapter.Load(context.Background(), puppetdbTarget("missing.example.test", "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for not-found factset, got nil")
	}
	if diag.Operation != model.OperationLoadFacts {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationLoadFacts)
	}
}

func TestAdapter_LoadBaseline_NotFoundViaHTTPStatus(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "Could not find catalog for missing.example.test"}`))
	})
	adapter := newAdapter(t, fixture, srv)

	_, _, diag := adapter.LoadBaseline(context.Background(), puppetdbTarget("missing.example.test", "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for not-found catalog, got nil")
	}
	if diag.Operation != model.OperationLoadBaseline {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationLoadBaseline)
	}
}

func TestAdapter_Load_MalformedJSON(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{not valid json`))
	})
	adapter := newAdapter(t, fixture, srv)

	fs, prov, diag := adapter.Load(context.Background(), puppetdbTarget("web-01.example.test", "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for malformed JSON, got nil")
	}
	if diag.Operation != model.OperationLoadFacts {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationLoadFacts)
	}
	if fs.Certname != "" || fs.Facts != nil || prov != (model.SourceProvenance{}) {
		t.Errorf("expected zero-value Factset/Provenance on malformed JSON, got %+v / %+v", fs, prov)
	}
}

func TestAdapter_LoadBaseline_MalformedJSON(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json at all`))
	})
	adapter := newAdapter(t, fixture, srv)

	cat, prov, diag := adapter.LoadBaseline(context.Background(), puppetdbTarget("web-01.example.test", "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for malformed JSON, got nil")
	}
	if diag.Operation != model.OperationLoadBaseline {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationLoadBaseline)
	}
	if cat.Certname != "" || cat.Resources != nil || prov != (model.SourceProvenance{}) {
		t.Errorf("expected zero-value Catalog/Provenance on malformed JSON, got %+v / %+v", cat, prov)
	}
}

// TestAdapter_NeverIssuesNonGETRequests exercises Load and LoadBaseline
// together against a server that fails the test if it ever receives
// anything but a GET (see newMTLSTestServer). This is an explicit,
// separately named assertion of the "never issues a PuppetDB write/
// command endpoint request" requirement, on top of the implicit check
// present in every other test in this file.
func TestAdapter_NeverIssuesNonGETRequests(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pdb/query/v4/factsets/web-01.example.test":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"certname":"web-01.example.test","environment":"production","producer_timestamp":"t","producer":"p","hash":"h","facts":{"data":[]}}`))
		case "/pdb/query/v4/catalogs/web-01.example.test":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"certname":"web-01.example.test","environment":"production","producer_timestamp":"t","producer":"p","hash":"h","resources":{},"edges":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	adapter := newAdapter(t, fixture, srv)

	if _, _, diag := adapter.Load(context.Background(), puppetdbTarget("web-01.example.test", "production")); diag != nil {
		t.Fatalf("Load returned diagnostic: %+v", diag)
	}
	if _, _, diag := adapter.LoadBaseline(context.Background(), puppetdbTarget("web-01.example.test", "production")); diag != nil {
		t.Fatalf("LoadBaseline returned diagnostic: %+v", diag)
	}
}
