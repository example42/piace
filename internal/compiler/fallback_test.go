package compiler

import (
	"context"
	"github.com/example42/piace/internal/transport"
	"net/http"
	"strings"
	"testing"
)

func TestFallbackRejectsAmbiguousErrorBodies(t *testing.T) {
	for _, body := range []string{`{"kind":"environment-not-found"}`, `{"error":"unauthorized"}`, `{bad json`, `<html>Not Found</html>`} {
		if isFallbackEligibleV4(&transport.Response{StatusCode: 404, Body: []byte(body)}) {
			t.Fatal("non-generic error triggered fallback")
		}
	}
	for _, status := range []int{401, 403, 500, 501, 503} {
		if isFallbackEligibleV4(&transport.Response{StatusCode: status}) {
			t.Fatalf("status %d triggered fallback", status)
		}
	}
}

func TestFailedV3StillReportsPersistenceEffects(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	adapter := newAdapter(t, fixture, srv)
	_, p, _, d := adapter.RequestCandidate(context.Background(), v3Target("web-01.example.test", "production"), factsetWithTrusted("web-01.example.test", "production", true))
	if d == nil || p.EffectiveAPI != "v3" || !strings.Contains(p.V3Warning, "Persistence warning") {
		t.Fatalf("failed request lost side effects: %+v %+v", p, d)
	}
}
