package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
	"github.com/example42/piace/internal/transport"
)

func factsetWithTrusted(certname, environment string, trusted bool) puppetdb.Factset {
	data := []factEntry{
		{Name: "os", Value: json.RawMessage(`"linux"`)},
	}
	if trusted {
		data = append(data, factEntry{
			Name: "trusted",
			Value: json.RawMessage(fmt.Sprintf(
				`{"certname":%q,"authenticated":"remote","extensions":{}}`, certname)),
		})
	}
	facts, _ := json.Marshal(expandedFacts{Href: "/x", Data: data})
	return puppetdb.Factset{
		Certname:          certname,
		Environment:       environment,
		ProducerTimestamp: "2026-01-01T00:00:00Z",
		Producer:          "compiler-01.example.test",
		Hash:              "h",
		Facts:             facts,
	}
}

func v4Target(certname, environment string, allowFallback, compilerLookup bool) resolve.Target {
	return resolve.Target{
		Certname: certname,
		Candidate: resolve.Candidate{
			Environment:                environment,
			CatalogAPI:                 config.CatalogAPIv4,
			AllowV3Fallback:            allowFallback,
			TrustedFactsCompilerLookup: compilerLookup,
		},
		Facts:    resolve.Facts{Source: config.FactSourcePuppetDB},
		Baseline: resolve.Baseline{Source: config.BaselineSourcePuppetDB, Environment: environment},
	}
}

func v3Target(certname, environment string) resolve.Target {
	return resolve.Target{
		Certname:  certname,
		Candidate: resolve.Candidate{Environment: environment, CatalogAPI: config.CatalogAPIv3},
		Facts:     resolve.Facts{Source: config.FactSourcePuppetDB},
		Baseline:  resolve.Baseline{Source: config.BaselineSourcePuppetDB, Environment: environment},
	}
}

func wireCatalogBody(name, environment string) []byte {
	body, _ := json.Marshal(map[string]any{
		"name":             name,
		"version":          "1",
		"environment":      environment,
		"code_id":          nil,
		"catalog_uuid":     "827a74c8-cf98-44da-9ff7-18c5e4bee41e",
		"transaction_uuid": "aff261a2-1a34-4647-8c20-ff662ec11c4c",
		"resources":        []any{},
		"edges":            []any{},
	})
	return body
}

func TestAdapter_RequestCandidate_V3Success(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/puppet/v3/catalog/web-01.example.test" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write(wireCatalogBody("web-01.example.test", "production"))
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", false)
	cat, prov, warnings, diag := adapter.RequestCandidate(context.Background(), v3Target("web-01.example.test", "production"), fs)
	if diag != nil {
		t.Fatalf("RequestCandidate returned diagnostic: %+v", diag)
	}
	if warnings != nil {
		t.Errorf("warnings = %v, want nil (v3 warning is carried in provenance)", warnings)
	}
	if cat.Certname != "web-01.example.test" || cat.Environment != "production" {
		t.Errorf("catalog = %+v", cat)
	}
	if prov.EffectiveAPI != config.CatalogAPIv3 || prov.RequestedAPI != config.CatalogAPIv3 {
		t.Errorf("provenance API = %+v", prov)
	}
	if prov.FellBackFromV4 {
		t.Error("FellBackFromV4 = true for a directly configured v3 request")
	}
	if prov.V3Warning != model.V3TrustedFactWarning {
		t.Errorf("V3Warning = %q, want the non-suppressible warning text", prov.V3Warning)
	}
}

func TestAdapter_RequestCandidate_V4Success_ProvidedTrustedFacts(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	var gotBody v4Request
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/puppet/v4/catalog" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write(wireCatalogBody("web-01.example.test", "production"))
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", true)
	cat, prov, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", false, false), fs)
	if diag != nil {
		t.Fatalf("RequestCandidate returned diagnostic: %+v", diag)
	}
	if cat.Certname != "web-01.example.test" {
		t.Errorf("catalog = %+v", cat)
	}
	if prov.TrustedFactsSource != model.TrustedFactsProvided {
		t.Errorf("TrustedFactsSource = %q, want %q", prov.TrustedFactsSource, model.TrustedFactsProvided)
	}
	if prov.V3Warning != "" {
		t.Errorf("V3Warning = %q, want empty for a v4 request", prov.V3Warning)
	}
	if gotBody.TrustedFacts == nil {
		t.Fatal("request body did not carry trusted_facts")
	}
	if gotBody.Persistence.Facts || gotBody.Persistence.Catalog {
		t.Errorf("Persistence = %+v, want {false, false}", gotBody.Persistence)
	}
}

func TestAdapter_RequestCandidate_V4Success_CompilerLookupOmitsField(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	var raw map[string]json.RawMessage
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write(wireCatalogBody("web-01.example.test", "production"))
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", false)
	_, prov, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", false, true), fs)
	if diag != nil {
		t.Fatalf("RequestCandidate returned diagnostic: %+v", diag)
	}
	if prov.TrustedFactsSource != model.TrustedFactsCompilerLookup {
		t.Errorf("TrustedFactsSource = %q, want %q", prov.TrustedFactsSource, model.TrustedFactsCompilerLookup)
	}
	if _, ok := raw["trusted_facts"]; ok {
		t.Error("request body carries trusted_facts, want the field omitted for compiler-lookup behavior")
	}
}

func TestAdapter_RequestCandidate_V4FailsWithoutTrustedFactsOrLookup(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP request should be made when neither trusted-fact source is available")
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", false)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", false, false), fs)
	if diag == nil {
		t.Fatal("expected a diagnostic when neither trusted-fact source is available, got nil")
	}
	if diag.Operation != model.OperationRequestCandidate {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationRequestCandidate)
	}
}

func TestAdapter_RequestCandidate_V4UnsupportedFallsBackToV3WhenAllowed(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	v3Seen := false
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/puppet/v4/catalog":
			w.WriteHeader(http.StatusNotFound)
		case "/puppet/v3/catalog/web-01.example.test":
			v3Seen = true
			w.WriteHeader(http.StatusOK)
			w.Write(wireCatalogBody("web-01.example.test", "production"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", true)
	cat, prov, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", true, false), fs)
	if diag != nil {
		t.Fatalf("RequestCandidate returned diagnostic: %+v", diag)
	}
	if !v3Seen {
		t.Fatal("expected a v3 fallback request, none observed")
	}
	if cat.Certname != "web-01.example.test" {
		t.Errorf("catalog = %+v", cat)
	}
	if prov.RequestedAPI != config.CatalogAPIv4 || prov.EffectiveAPI != config.CatalogAPIv3 {
		t.Errorf("provenance API = %+v", prov)
	}
	if !prov.FellBackFromV4 {
		t.Error("FellBackFromV4 = false, want true")
	}
	if prov.V3Warning != model.V3TrustedFactWarning {
		t.Errorf("V3Warning = %q, want the non-suppressible warning text", prov.V3Warning)
	}
}

func TestAdapter_RequestCandidate_V4UnsupportedNoFallbackWhenNotAllowed(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/puppet/v3/catalog/web-01.example.test" {
			t.Error("v3 fallback request made despite allow_v3_fallback being false")
		}
		w.WriteHeader(http.StatusNotFound)
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", true)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", false, false), fs)
	if diag == nil {
		t.Fatal("expected a diagnostic for an unsupported-v4 response with fallback disabled, got nil")
	}
}

func TestAdapter_RequestCandidate_V4AuthFailureNeverFallsBackEvenIfAllowed(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/puppet/v3/catalog/web-01.example.test" {
			t.Error("v3 fallback request made after a v4 authentication/authorization failure")
		}
		w.WriteHeader(http.StatusForbidden)
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", true)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", true, false), fs)
	if diag == nil {
		t.Fatal("expected a diagnostic for a v4 403 response, got nil")
	}
}

func TestAdapter_RequestCandidate_V4TimeoutNeverFallsBackEvenIfAllowed(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	block := make(chan struct{})
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/puppet/v3/catalog/web-01.example.test" {
			t.Error("v3 fallback request made after a v4 timeout")
		}
		// Block until this test unblocks it below, well past the client's
		// short deadline, so the v4 request fails with a transport-level
		// timeout rather than ever producing an HTTP response. This must
		// be unblocked before newMTLSTestServer's registered srv.Close()
		// cleanup runs (Close waits for in-flight handlers), so it is
		// unblocked explicitly at the end of the test body rather than
		// left to t.Cleanup ordering.
		<-block
	})
	ep := fixture.endpoint(t, srv.URL)
	client, err := transport.NewClient(ep, transport.WithTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("transport.NewClient: %v", err)
	}
	adapter := NewAdapter(client, ep.URL)

	fs := factsetWithTrusted("web-01.example.test", "production", true)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", true, false), fs)
	close(block)
	if diag == nil {
		t.Fatal("expected a diagnostic for a v4 request timeout, got nil")
	}
	if diag.Operation != model.OperationRequestCandidateTransport {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationRequestCandidateTransport)
	}
}

func TestAdapter_RequestCandidate_MalformedV4ResponseNeverFallsBack(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/puppet/v3/catalog/web-01.example.test" {
			t.Error("v3 fallback request made after a malformed v4 200 response")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{not valid json`))
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", true)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", true, false), fs)
	if diag == nil {
		t.Fatal("expected a diagnostic for a malformed v4 response, got nil")
	}
}

func TestAdapter_RequestCandidate_IdentityMismatchNeverFallsBack(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/puppet/v3/catalog/web-01.example.test" {
			t.Error("v3 fallback request made after a v4 identity mismatch")
		}
		w.WriteHeader(http.StatusOK)
		w.Write(wireCatalogBody("some-other-node.example.test", "production"))
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", true)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", true, false), fs)
	if diag == nil {
		t.Fatal("expected a diagnostic for a candidate identity mismatch, got nil")
	}
}

func TestAdapter_RequestCandidate_EnvironmentMismatchFails(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(wireCatalogBody("web-01.example.test", "feature-999"))
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", false)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), v3Target("web-01.example.test", "production"), fs)
	if diag == nil {
		t.Fatal("expected a diagnostic for a candidate environment mismatch, got nil")
	}
	if diag.Operation != model.OperationRequestCandidate {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationRequestCandidate)
	}
}

func TestAdapter_RequestCandidate_V3ErrorBodyFails(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error": "Could not find node web-01.example.test"}`))
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", false)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), v3Target("web-01.example.test", "production"), fs)
	if diag == nil {
		t.Fatal("expected a diagnostic for a semantic error-body response, got nil")
	}
}

// TestAdapter_RequestCandidate_ProvenanceNeverCarriesTrustedFactValues
// asserts design.md section 5's "the response provenance records
// `provided` or `compiler_lookup` but never trusted-fact values": for a
// successful v4 request with a provided trusted-fact structure,
// CandidateProvenance carries only the enum classification
// (TrustedFactsSource), never the certname/extensions/authenticated
// content of the trusted fact itself, in any of its string-valued
// fields.
func TestAdapter_RequestCandidate_ProvenanceNeverCarriesTrustedFactValues(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(wireCatalogBody("web-01.example.test", "production"))
	})
	adapter := newAdapter(t, fixture, srv)

	fs := factsetWithTrusted("web-01.example.test", "production", true)
	_, prov, _, diag := adapter.RequestCandidate(context.Background(), v4Target("web-01.example.test", "production", false, false), fs)
	if diag != nil {
		t.Fatalf("RequestCandidate returned diagnostic: %+v", diag)
	}
	if prov.TrustedFactsSource != model.TrustedFactsProvided {
		t.Fatalf("TrustedFactsSource = %q, want %q", prov.TrustedFactsSource, model.TrustedFactsProvided)
	}

	encoded, err := json.Marshal(prov)
	if err != nil {
		t.Fatalf("Marshal(prov): %v", err)
	}
	// The trusted fact built by factsetWithTrusted embeds the target's own
	// certname as its "certname" value and the literal string
	// "authenticated"; neither should leak into the serialized
	// provenance beyond the certname's already-expected appearance
	// nowhere in CandidateProvenance's own fields (it has no Certname
	// field at all), so this checks the trusted-fact-specific literal
	// "authenticated" is absent.
	if bytesContains(encoded, "authenticated") {
		t.Errorf("serialized provenance leaks a trusted-fact value: %s", encoded)
	}
}

func bytesContains(haystack []byte, needle string) bool {
	return len(haystack) > 0 && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if string(haystack[i:i+len(needle)]) == needle {
				return true
			}
		}
		return false
	})()
}

func TestAdapter_RequestCandidate_UnsupportedCatalogAPIFails(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP request should be made for an unsupported catalog_api value")
	})
	adapter := newAdapter(t, fixture, srv)

	target := v3Target("web-01.example.test", "production")
	target.Candidate.CatalogAPI = "v5"
	fs := factsetWithTrusted("web-01.example.test", "production", false)
	_, _, _, diag := adapter.RequestCandidate(context.Background(), target, fs)
	if diag == nil {
		t.Fatal("expected a diagnostic for an unsupported catalog_api, got nil")
	}
}
