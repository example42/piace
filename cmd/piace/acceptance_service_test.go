package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// recorder collects every request path a fake service received, so a
// test can assert on the exact set of endpoints PIACE contacted rather
// than on the absence of a symptom.
type recorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *recorder) record(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
}

// sortedPaths returns the deduplicated, sorted set of recorded paths.
func (r *recorder) sortedPaths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool, len(r.paths))
	out := make([]string, 0, len(r.paths))
	for _, p := range r.paths {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.paths)
}

// fakePuppetDB serves the two read endpoints internal/puppetdb uses plus
// the root query endpoint internal/impact uses. Every behavior a test
// needs to vary is a field, so the tests read as a matrix rather than as
// a pile of handlers.
type fakePuppetDB struct {
	recorder
	// factsets and catalogs are keyed by certname. A missing key produces
	// PuppetDB's documented empty-result shape, which the adapter reports
	// as "no factset/catalog found".
	factsets map[string]any
	catalogs map[string]any
	// factsetStatus and catalogStatus override the HTTP status for a
	// certname when set.
	factsetStatus map[string]int
	catalogStatus map[string]int
	// impactCertnames is the certname list the root query endpoint
	// returns; impactStatus overrides its HTTP status when non-zero.
	impactCertnames []string
	impactStatus    int
	// impactQueries records the exact query/limit/order_by parameters the
	// estimator sent, so the assumptions internal/impact documents can be
	// asserted at the wire.
	impactQueries []map[string]string
}

func newFakePuppetDB() *fakePuppetDB {
	return &fakePuppetDB{
		factsets:      map[string]any{},
		catalogs:      map[string]any{},
		factsetStatus: map[string]int{},
		catalogStatus: map[string]int{},
	}
}

func (f *fakePuppetDB) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r.URL.Path)
		switch {
		case r.URL.Path == "/pdb/query/v4":
			f.serveQuery(w, r)
		case strings.HasPrefix(r.URL.Path, "/pdb/query/v4/factsets/"):
			certname := strings.TrimPrefix(r.URL.Path, "/pdb/query/v4/factsets/")
			f.serveEntity(w, certname, f.factsets, f.factsetStatus)
		case strings.HasPrefix(r.URL.Path, "/pdb/query/v4/catalogs/"):
			certname := strings.TrimPrefix(r.URL.Path, "/pdb/query/v4/catalogs/")
			f.serveEntity(w, certname, f.catalogs, f.catalogStatus)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
}

func (f *fakePuppetDB) serveEntity(w http.ResponseWriter, certname string, store map[string]any, statuses map[string]int) {
	if status, ok := statuses[certname]; ok {
		w.WriteHeader(status)
		writeJSON(w, map[string]any{"error": "forced status"})
		return
	}
	entity, ok := store[certname]
	if !ok {
		// PuppetDB's documented not-found shape for a single-entity
		// query: HTTP 200 with no certname in the body.
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, entity)
}

func (f *fakePuppetDB) serveQuery(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.impactQueries = append(f.impactQueries, map[string]string{
		"query":    r.URL.Query().Get("query"),
		"limit":    r.URL.Query().Get("limit"),
		"order_by": r.URL.Query().Get("order_by"),
	})
	f.mu.Unlock()

	if f.impactStatus != 0 {
		w.WriteHeader(f.impactStatus)
		writeJSON(w, map[string]any{"error": "forced status"})
		return
	}
	rows := make([]map[string]string, 0, len(f.impactCertnames))
	for _, certname := range f.impactCertnames {
		rows = append(rows, map[string]string{"certname": certname})
	}
	writeJSON(w, rows)
}

// fakeCompiler serves the v3/v4 catalog endpoints and the v3 file_content
// endpoint.
type fakeCompiler struct {
	recorder
	// v4Status, when non-zero, is returned for POST /puppet/v4/catalog
	// instead of a catalog. 404/501 is what internal/compiler treats as a
	// verified-unsupported response eligible for v3 fallback.
	v4Status int
	// v3Status behaves the same way for POST /puppet/v3/catalog/:certname.
	v3Status int
	// catalogs is keyed by certname and holds the compiler wire-format
	// catalog *document* to return. serveCatalog wraps it in whichever
	// envelope the answering endpoint uses.
	catalogs map[string]any
	// rawBodies is keyed by certname and, when set, is returned verbatim
	// with status 200 in place of any catalog, with no endpoint envelope
	// applied. It exists so a test can exercise the adapter's
	// semantic-rejection probe, which reads the outer response body before
	// any envelope is unwrapped.
	rawBodies map[string]any
	// fileContent is keyed by the mount path segment the resolver builds
	// from a `puppet://` source reference.
	fileContent map[string]string
	// v4Bodies records each decoded v4 request body, so a test can assert on
	// trusted_facts handling and on the persistence flags a candidate
	// request must never set.
	v4Bodies []map[string]any
}

func newFakeCompiler() *fakeCompiler {
	return &fakeCompiler{catalogs: map[string]any{}, rawBodies: map[string]any{}, fileContent: map[string]string{}}
}

func (f *fakeCompiler) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r.URL.Path)
		switch {
		case r.URL.Path == "/puppet/v4/catalog":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.v4Bodies = append(f.v4Bodies, body)
			f.mu.Unlock()
			if f.v4Status != 0 {
				w.WriteHeader(f.v4Status)
				if f.v4Status != http.StatusNotFound {
					writeJSON(w, map[string]any{"error": "forced status"})
				}
				return
			}
			certname, _ := body["certname"].(string)
			f.serveCatalog(w, certname, apiV4)
		case strings.HasPrefix(r.URL.Path, "/puppet/v3/catalog/"):
			if f.v3Status != 0 {
				w.WriteHeader(f.v3Status)
				writeJSON(w, map[string]any{"error": "forced status"})
				return
			}
			f.serveCatalog(w, strings.TrimPrefix(r.URL.Path, "/puppet/v3/catalog/"), apiV3)
		case strings.HasPrefix(r.URL.Path, "/puppet/v3/file_content/"):
			content, ok := f.fileContent[strings.TrimPrefix(r.URL.Path, "/puppet/v3/file_content/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				writeJSON(w, map[string]any{"issue_kind": "RESOURCE_NOT_FOUND"})
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, content)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
}

// catalogAPI selects which endpoint's response envelope serveCatalog
// wraps a catalog document in. The two endpoints differ: v3 returns the
// document as the whole body, v4 returns `{"catalog": <document>}` (see
// internal/compiler/doc.go for the primary sources). Serving one shape
// from both endpoints, as this fixture originally did, hides that
// difference from every acceptance test that exercises the v4 path.
type catalogAPI int

const (
	apiV3 catalogAPI = iota
	apiV4
)

func (f *fakeCompiler) serveCatalog(w http.ResponseWriter, certname string, api catalogAPI) {
	if raw, ok := f.rawBodies[certname]; ok {
		writeJSON(w, raw)
		return
	}
	catalog, ok := f.catalogs[certname]
	if !ok {
		http.Error(w, "no catalog", http.StatusNotFound)
		return
	}
	if api == apiV4 {
		writeJSON(w, map[string]any{"catalog": catalog})
		return
	}
	writeJSON(w, catalog)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// startTLS starts an mTLS httptest server for handler using fixture's CA,
// registering its shutdown with t.
func startTLS(t *testing.T, fixture *tlsFixture, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = fixture.serverTLSConfig()
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// startForbiddenTLS starts a third mTLS service that fails the test if
// it is ever contacted. It exists to make the claim that PIACE opens
// network connections only to the configured compiler and PuppetDB
// endpoints provable rather than merely asserted: an absence claim needs
// a witness that would have observed the violation.
func startForbiddenTLS(t *testing.T, fixture *tlsFixture) *httptest.Server {
	t.Helper()
	return startTLS(t, fixture, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("PIACE contacted an endpoint outside its configured services: %s %s", r.Method, r.URL.Path)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
}
