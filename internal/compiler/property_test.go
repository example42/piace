package compiler

import (
	"context"
	"math/rand"
	"net/http"
	"testing"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
)

// isZeroCatalog reports whether cat has every field at its zero value.
// puppetdb.Catalog embeds json.RawMessage ([]byte) fields, which are not
// comparable with ==, so this checks field-by-field instead of `cat !=
// puppetdb.Catalog{}`.
func isZeroCatalog(cat puppetdb.Catalog) bool {
	return cat.Certname == "" && cat.Version == "" && cat.Environment == "" &&
		cat.Hash == "" && cat.TransactionUUID == "" && cat.CatalogUUID == "" &&
		cat.CodeID == "" && cat.ProducerTimestamp == "" && cat.Producer == "" &&
		len(cat.Resources) == 0 && len(cat.Edges) == 0
}

// randomIdentifier generates a short random-ish but deterministic (given
// rng) certname/environment-shaped string, so each property iteration
// exercises a different concrete value without relying on any external
// PBT library (none is vendored in this module; see go.mod).
func randomIdentifier(rng *rand.Rand, prefix string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789-"
	n := 4 + rng.Intn(12)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return prefix + string(b) + ".example.test"
}

// TestProperty_CandidateIdentityIntegrity is a property-based test over
// randomly generated (requested certname and environment, returned
// certname and environment) pairs: RequestCandidate must accept the
// candidate catalog if and only if both the returned name and
// environment exactly equal what was requested. That is candidate
// identity integrity applied at the adapter layer that produces the only
// candidate PIACE ever compares.
func TestProperty_CandidateIdentityIntegrity(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	for i := 0; i < 200; i++ {
		requestedCertname := randomIdentifier(rng, "node-")
		requestedEnv := randomIdentifier(rng, "env-")

		// Independently perturb the returned identity/environment so
		// roughly half of all iterations exercise a genuine mismatch on
		// one or both axes, and the rest exercise an exact match.
		returnedCertname := requestedCertname
		if rng.Intn(2) == 0 {
			returnedCertname = randomIdentifier(rng, "node-")
		}
		returnedEnv := requestedEnv
		if rng.Intn(2) == 0 {
			returnedEnv = randomIdentifier(rng, "env-")
		}

		wantAccept := returnedCertname == requestedCertname && returnedEnv == requestedEnv

		// Alternate the candidate API across iterations: identity integrity is a
		// property of both endpoints, and the two do not share a response
		// envelope, since v4 wraps the catalog document and v3 does not (see
		// doc.go), so exercising only one would leave the other's identity check
		// unproven.
		useV4 := i%2 == 0
		body := wireCatalogBody(returnedCertname, returnedEnv)
		target := v3Target(requestedCertname, requestedEnv)
		if useV4 {
			body = v4CatalogBody(returnedCertname, returnedEnv)
			target = v4Target(requestedCertname, requestedEnv, false, false)
		}

		fixture := newTLSFixture(t, "127.0.0.1")
		srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write(body)
		})
		adapter := newAdapter(t, fixture, srv)

		fs := factsetWithTrusted(requestedCertname, requestedEnv, useV4)

		cat, _, _, diag := adapter.RequestCandidate(context.Background(), target, fs)

		if wantAccept {
			if diag != nil {
				t.Fatalf("iteration %d: requested (%q, %q), returned (%q, %q): expected acceptance, got diagnostic %+v",
					i, requestedCertname, requestedEnv, returnedCertname, returnedEnv, diag)
			}
			if cat.Certname != requestedCertname || cat.Environment != requestedEnv {
				t.Fatalf("iteration %d: accepted catalog identity (%q, %q) does not match request (%q, %q)",
					i, cat.Certname, cat.Environment, requestedCertname, requestedEnv)
			}
		} else {
			if diag == nil {
				t.Fatalf("iteration %d: requested (%q, %q), returned (%q, %q): expected a diagnostic, got none (catalog=%+v)",
					i, requestedCertname, requestedEnv, returnedCertname, returnedEnv, cat)
			}
			if diag.Operation != model.OperationRequestCandidate {
				t.Errorf("iteration %d: Operation = %q, want %q", i, diag.Operation, model.OperationRequestCandidate)
			}
			if !isZeroCatalog(cat) {
				t.Errorf("iteration %d: expected a zero-value Catalog alongside a diagnostic, got %+v", i, cat)
			}
		}

		srv.Close()
	}
}

// TestProperty_V3WarningAlwaysEmitted is a property-based test: for
// every randomly generated target requesting catalog_api v3 directly, a
// successful RequestCandidate call always attaches
// model.V3TrustedFactWarning to the returned provenance, regardless of
// certname, environment, or trusted-fact factset content. This locks the
// rule that every v3 request attaches a prominent, non-suppressible
// warning, across many inputs, since the warning must never depend on
// incidental request content.
func TestProperty_V3WarningAlwaysEmitted(t *testing.T) {
	rng := rand.New(rand.NewSource(2))

	for i := 0; i < 100; i++ {
		certname := randomIdentifier(rng, "node-")
		env := randomIdentifier(rng, "env-")
		hasTrusted := rng.Intn(2) == 0

		fixture := newTLSFixture(t, "127.0.0.1")
		srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write(wireCatalogBody(certname, env))
		})
		adapter := newAdapter(t, fixture, srv)

		fs := factsetWithTrusted(certname, env, hasTrusted)
		target := v3Target(certname, env)

		_, prov, _, diag := adapter.RequestCandidate(context.Background(), target, fs)
		if diag != nil {
			t.Fatalf("iteration %d: unexpected diagnostic: %+v", i, diag)
		}
		if prov.V3Warning != model.V3TrustedFactWarning {
			t.Fatalf("iteration %d (certname=%q env=%q hasTrusted=%v): V3Warning = %q, want the non-suppressible warning text",
				i, certname, env, hasTrusted, prov.V3Warning)
		}
		if prov.EffectiveAPI != config.CatalogAPIv3 {
			t.Errorf("iteration %d: EffectiveAPI = %q, want %q", i, prov.EffectiveAPI, config.CatalogAPIv3)
		}

		srv.Close()
	}
}

// TestProperty_FallbackOnlyOnEligibleV4Response is a property-based
// test over randomly generated v4 failure status codes: a v4-to-v3
// fallback must occur if and only if AllowV3Fallback is true AND the v4
// response is an empty-body 404 eligible under explicit policy. Every
// other status is a forbidden fallback trigger: authentication,
// authorization, timeout, malformed response, and identity or
// environment mismatch. This iterates every status code in a
// representative set combined with both AllowV3Fallback settings.
func TestProperty_FallbackOnlyOnEligibleV4Response(t *testing.T) {
	statusCodes := []int{
		http.StatusBadRequest,          // 400 - malformed request, never fallback
		http.StatusUnauthorized,        // 401 - authentication, never fallback
		http.StatusForbidden,           // 403 - authorization, never fallback
		http.StatusNotFound,            // 404 - empty-body fallback candidate
		http.StatusInternalServerError, // 500 - generic server error, never fallback
		http.StatusNotImplemented,      // 501 - not a supported fallback signal
		http.StatusServiceUnavailable,  // 503 - never fallback
	}
	unsupported := map[int]bool{
		http.StatusNotFound: true,
	}

	rng := rand.New(rand.NewSource(3))

	for _, status := range statusCodes {
		for _, allowFallback := range []bool{true, false} {
			certname := randomIdentifier(rng, "node-")
			env := randomIdentifier(rng, "env-")

			v3Called := false
			fixture := newTLSFixture(t, "127.0.0.1")
			srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/puppet/v4/catalog":
					w.WriteHeader(status)
				case "/puppet/v3/catalog/" + certname:
					v3Called = true
					w.WriteHeader(http.StatusOK)
					w.Write(wireCatalogBody(certname, env))
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
			})
			adapter := newAdapter(t, fixture, srv)

			fs := factsetWithTrusted(certname, env, true)
			target := v4Target(certname, env, allowFallback, false)

			_, _, _, diag := adapter.RequestCandidate(context.Background(), target, fs)

			wantFallback := allowFallback && unsupported[status]
			if v3Called != wantFallback {
				t.Errorf("status=%d allowFallback=%v: v3Called = %v, want %v",
					status, allowFallback, v3Called, wantFallback)
			}
			if wantFallback && diag != nil {
				t.Errorf("status=%d allowFallback=%v: expected successful fallback, got diagnostic %+v",
					status, allowFallback, diag)
			}
			if !wantFallback && diag == nil {
				t.Errorf("status=%d allowFallback=%v: expected a diagnostic (no fallback), got none",
					status, allowFallback)
			}

			srv.Close()
		}
	}
}
