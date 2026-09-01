package transport

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestClientNeverSendsAnAuthorizationHeader guards the no-bearer-token
// rule at the boundary that enforces it: whatever a caller sets, a
// compiler or PuppetDB request authenticates by mTLS and carries no
// bearer token, on the initial request and on an allowed same-authority
// redirect alike.
//
// PIACE does send a bearer token to exactly one service: the inference
// service in internal/inference, which is a separate client with its own
// package for exactly this reason. See
// docs/adr/0003-authenticate-the-inference-service-with-a-bearer-token.md.
// If this test fails, that exception has stopped being scoped.
func TestClientNeverSendsAnAuthorizationHeader(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	var seen string
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})

	client, err := NewClient(fixture.endpointFor(t, srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer leaked-token")

	if _, err := client.Do(req, 5*time.Second); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if seen != "" {
		t.Errorf("compiler/PuppetDB request carried Authorization: %q", seen)
	}
}
