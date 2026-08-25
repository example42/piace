package transport

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

// newMTLSTestServer starts an httptest.Server configured for mutual TLS
// using fixture's certificates, and returns it already started.
func newMTLSTestServer(t *testing.T, fixture *tlsFixture, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = fixture.serverTLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestNewClient_SuccessfulMTLSRoundTrip(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 {
			t.Errorf("server saw no client certificate")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	ep := fixture.endpointFor(t, srv.URL)
	client, err := NewClient(ep)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req, 5*time.Second)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if string(resp.Body) != "ok" {
		t.Errorf("Body = %q, want %q", resp.Body, "ok")
	}
}

func TestNewClient_RejectsNonHTTPSEndpoint(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	ep := fixture.endpointFor(t, "http://127.0.0.1:9999")
	_, err := NewClient(ep)
	if err == nil {
		t.Fatal("expected error for non-https endpoint, got nil")
	}
	te, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if te.Kind != KindConfig {
		t.Errorf("Kind = %q, want %q", te.Kind, KindConfig)
	}
}

func TestNewClient_RejectsEmptyHost(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	ep := fixture.endpointFor(t, "https:///no-host-path")
	_, err := NewClient(ep)
	if err == nil {
		t.Fatal("expected error for empty host, got nil")
	}
}

func TestNewClient_TLSVersionEnforced(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	tlsCfg := fixture.serverTLSConfig()
	// Force the server to only ever negotiate TLS 1.1 or below, which the
	// hardened client (MinVersion = TLS 1.2) must refuse to speak to.
	tlsCfg.MaxVersion = tls.VersionTLS11
	srv.TLS = tlsCfg
	srv.StartTLS()
	defer srv.Close()

	ep := fixture.endpointFor(t, srv.URL)
	client, err := NewClient(ep)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = client.Do(req, 5*time.Second)
	if err == nil {
		t.Fatal("expected a TLS handshake failure, got nil error")
	}
	te, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if te.Kind != KindTLS {
		t.Errorf("Kind = %q, want %q", te.Kind, KindTLS)
	}
}

func TestClient_RedirectSameAuthorityAllowed(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	var srv *httptest.Server
	srv = newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, srv.URL+"/target", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("landed"))
	})

	ep := fixture.endpointFor(t, srv.URL)
	client, err := NewClient(ep)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL+"/redirect", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req, 5*time.Second)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(resp.Body) != "landed" {
		t.Errorf("resp = %+v, want 200/landed", resp)
	}
}

func TestClient_RedirectDifferentAuthorityRejected(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.test/target", http.StatusFound)
	})

	ep := fixture.endpointFor(t, srv.URL)
	client, err := NewClient(ep)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = client.Do(req, 5*time.Second)
	if err == nil {
		t.Fatal("expected redirect rejection error, got nil")
	}
	te, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if te.Kind != KindRedirectRejected {
		t.Errorf("Kind = %q, want %q", te.Kind, KindRedirectRejected)
	}
}

func TestClient_ResponseBodySizeLimitEnforced(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	const overLimit = 1024
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, overLimit))
	})

	ep := fixture.endpointFor(t, srv.URL)
	client, err := NewClient(ep, WithMaxResponseBodyBytes(100))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = client.Do(req, 5*time.Second)
	if err == nil {
		t.Fatal("expected response-too-large error, got nil")
	}
	te, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if te.Kind != KindResponseTooLarge {
		t.Errorf("Kind = %q, want %q", te.Kind, KindResponseTooLarge)
	}
}

func TestClient_ResponseBodyExactlyAtLimitSucceeds(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	const exactLimit = 100
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, exactLimit))
	})

	ep := fixture.endpointFor(t, srv.URL)
	client, err := NewClient(ep, WithMaxResponseBodyBytes(exactLimit))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req, 5*time.Second)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(resp.Body) != exactLimit {
		t.Errorf("len(Body) = %d, want %d", len(resp.Body), exactLimit)
	}
}

func TestClient_RequestDeadlineTriggersOperationalError(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	ep := fixture.endpointFor(t, srv.URL)
	client, err := NewClient(ep)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = client.Do(req, 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	te, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if te.Kind != KindTimeout {
		t.Errorf("Kind = %q, want %q", te.Kind, KindTimeout)
	}
	if !IsOperational(err) {
		t.Errorf("IsOperational(err) = false, want true")
	}
}

func TestNewClient_MalformedCAIsOperationalError(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	writeFixtureFile(t, fixture.caBundle, []byte("not a pem file"))

	ep := fixture.endpointFor(t, "https://127.0.0.1:1")
	_, err := NewClient(ep)
	if err == nil {
		t.Fatal("expected error for malformed CA bundle, got nil")
	}
	te, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if te.Kind != KindConfig {
		t.Errorf("Kind = %q, want %q", te.Kind, KindConfig)
	}
}

func TestNewClient_MissingCertFileIsOperationalError(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	ep := fixture.endpointFor(t, "https://127.0.0.1:1")
	ep.ClientCert = ep.ClientCert + ".does-not-exist"

	_, err := NewClient(ep)
	if err == nil {
		t.Fatal("expected error for missing client cert file, got nil")
	}
	te, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if te.Kind != KindConfig {
		t.Errorf("Kind = %q, want %q", te.Kind, KindConfig)
	}
}

func TestClients_AreIndependentEvenWithIdenticalCertFiles(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ep := fixture.endpointFor(t, srv.URL)
	compiler, err := NewClient(ep)
	if err != nil {
		t.Fatalf("NewClient(compiler): %v", err)
	}
	puppetdb, err := NewClient(ep)
	if err != nil {
		t.Fatalf("NewClient(puppetdb): %v", err)
	}
	if compiler.httpClient == puppetdb.httpClient {
		t.Error("compiler and puppetdb share the same *http.Client")
	}
	if compiler.httpClient.Transport == puppetdb.httpClient.Transport {
		t.Error("compiler and puppetdb share the same *http.Transport")
	}
	ct := compiler.httpClient.Transport.(*http.Transport)
	pt := puppetdb.httpClient.Transport.(*http.Transport)
	if ct.TLSClientConfig == pt.TLSClientConfig {
		t.Error("compiler and puppetdb share the same *tls.Config")
	}
}
