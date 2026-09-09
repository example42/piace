package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/example42/piace/internal/config/resolve"
)

func TestEndpointUserinfoRejectedBeforeTLSLoading(t *testing.T) {
	for _, raw := range []string{"https://user:secret@example.test", "https://user@example.test"} {
		u, _ := url.Parse(raw)
		if _, err := NewClient(resolve.Endpoint{URL: u}); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe constructor result: %v", err)
		}
	}
}

func TestInitialRequestEnforcesConfiguredAuthority(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) { t.Error("rejected request reached server") })
	client, err := NewClient(fixture.endpointFor(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"userinfo", "authority", "scheme", "host header"} {
		t.Run(kind, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
			switch kind {
			case "userinfo":
				req.URL.User = url.UserPassword("user", "secret")
			case "authority":
				req.URL.Host = "different.invalid:8140"
			case "scheme":
				req.URL.Scheme = "http"
			case "host header":
				req.Host = "different.invalid"
			}
			if _, err := client.Do(req, 0); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe result: %v", err)
			}
		})
	}
}

func TestRedirectUserinfoRejectedBeforeAuthorization(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/start" {
			t.Error("redirect request reached server")
			return
		}
		http.Redirect(w, r, "https://user:secret@"+r.Host+"/next", http.StatusFound)
	})
	client, err := NewClient(fixture.endpointFor(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := client.NewRequest(context.Background(), http.MethodGet, srv.URL+"/start", nil)
	if _, err := client.Do(req, 0); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe redirect result: %v", err)
	}
}

func TestObservableMetadataIsBoundedAndEscaped(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/"+strings.Repeat("x", 10000))
		json.NewEncoder(w).Encode(map[string]any{"evil\nline\r\x1b" + strings.Repeat("x", 10000): "secret-body"})
	})
	var ev Event
	client, err := NewClient(fixture.endpointFor(t, srv.URL), WithObserver(func(e Event) { ev = e }))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := client.NewRequest(context.Background(), http.MethodGet, srv.URL+"/?token=secret-query&api-version=2024-10-21", nil)
	if _, err := client.Do(req, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ev.URL, "secret-query") || !strings.Contains(ev.URL, "api-version=2024-10-21") {
		t.Fatal("unsafe URL projection")
	}
	if len(ev.ContentType) > 256 || len(ev.TopLevelKeys) != 1 || len(ev.TopLevelKeys[0]) > 256 || strings.ContainsAny(ev.TopLevelKeys[0], "\n\r\x1b") {
		t.Fatal("unbounded or injectable metadata")
	}
}

func TestTransportErrorDoesNotDiscloseQuery(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {})
	client, err := NewClient(fixture.endpointFor(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	req, _ := client.NewRequest(context.Background(), http.MethodGet, srv.URL+"/?secret=private", nil)
	_, err = client.Do(req, 0)
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe error: %v", err)
	}
}
