package impact

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/example42/piace/internal/model"
)

const pkgNginx = "Package[nginx]"

func nginx() model.ResourceIdentity {
	return model.ResourceIdentity{Type: "Package", Title: "nginx"}
}

func certnameRows(names ...string) string {
	rows := make([]map[string]string, 0, len(names))
	for _, n := range names {
		rows = append(rows, map[string]string{"certname": n})
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// capturedRequest records what the querier actually sent.
type capturedRequest struct {
	path    string
	query   string
	limit   string
	orderBy string
	method  string
}

func serveRows(t *testing.T, captured *capturedRequest, body string) *Querier {
	t.Helper()
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.query = r.URL.Query().Get("query")
		captured.limit = r.URL.Query().Get("limit")
		captured.orderBy = r.URL.Query().Get("order_by")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	return newQuerier(t, fixture, srv)
}

func limits(resultLimit int) Limits {
	return Limits{Timeout: 5 * time.Second, ResultLimit: resultLimit}
}

func TestEstimate_SendsTheSpecifiedQueryToTheRootEndpoint(t *testing.T) {
	var captured capturedRequest
	q := serveRows(t, &captured, certnameRows("web-02", "web-01"))

	estimate, diag := q.Estimate(context.Background(), nginx(), limits(10))
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}

	if captured.method != http.MethodGet {
		t.Errorf("method = %s, want GET", captured.method)
	}
	if captured.path != "/pdb/query/v4" {
		t.Errorf("path = %q, want the root query endpoint", captured.path)
	}
	wantPQL := `resources[certname] { type = "Package" and title = "nginx" }`
	if captured.query != wantPQL {
		t.Errorf("query =\n  %s\nwant\n  %s", captured.query, wantPQL)
	}
	// the reported PQL is exactly what was sent.
	if estimate.PQL != captured.query {
		t.Errorf("reported PQL %q != sent PQL %q", estimate.PQL, captured.query)
	}
}

// limit = result_limit + 1, plus certname ordering.
func TestEstimate_SendsLimitPlusOneAndCertnameOrdering(t *testing.T) {
	var captured capturedRequest
	q := serveRows(t, &captured, certnameRows("web-01"))

	estimate, _ := q.Estimate(context.Background(), nginx(), limits(10))

	if captured.limit != "11" {
		t.Errorf("limit = %q, want 11 (result_limit+1)", captured.limit)
	}
	if captured.orderBy != `[{"field":"certname","order":"asc"}]` {
		t.Errorf("order_by = %q", captured.orderBy)
	}
	if estimate.Request.Path != "/pdb/query/v4" || estimate.Request.Limit != 11 {
		t.Errorf("request options not preserved: %+v", estimate.Request)
	}
	if estimate.Request.OrderBy != captured.orderBy {
		t.Errorf("reported order_by %q != sent %q", estimate.Request.OrderBy, captured.orderBy)
	}
	if estimate.ResultLimit != 10 || estimate.Timeout != "5s" {
		t.Errorf("limits not preserved: %+v", estimate)
	}
}

func TestEstimate_SortsCertnamesLocally(t *testing.T) {
	var captured capturedRequest
	q := serveRows(t, &captured, certnameRows("web-03", "web-01", "web-02"))

	estimate, diag := q.Estimate(context.Background(), nginx(), limits(10))
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	want := []string{"web-01", "web-02", "web-03"}
	if len(estimate.Certnames) != 3 {
		t.Fatalf("certnames = %v", estimate.Certnames)
	}
	for i, w := range want {
		if estimate.Certnames[i] != w {
			t.Errorf("certnames = %v, want %v", estimate.Certnames, want)
			break
		}
	}
	if estimate.Truncated {
		t.Error("an under-limit result must not be truncated")
	}
	if estimate.ResultCount != 3 {
		t.Errorf("result count = %d, want 3", estimate.ResultCount)
	}
	if estimate.Status != model.ImpactStatusCompleted {
		t.Errorf("status = %s", estimate.Status)
	}
}

// reaching the limit marks the estimate truncated and
// reports a deterministic sample of exactly ResultLimit certnames.
func TestEstimate_TruncatesAtResultLimit(t *testing.T) {
	var captured capturedRequest
	// 4 rows returned for result_limit 3 (the query asked for 4).
	q := serveRows(t, &captured, certnameRows("web-04", "web-01", "web-03", "web-02"))

	estimate, diag := q.Estimate(context.Background(), nginx(), limits(3))
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if !estimate.Truncated {
		t.Error("expected truncated")
	}
	if len(estimate.Certnames) != 3 {
		t.Fatalf("sample = %v, want exactly 3", estimate.Certnames)
	}
	if estimate.Certnames[0] != "web-01" || estimate.Certnames[2] != "web-03" {
		t.Errorf("sample = %v, want the lexicographically first three", estimate.Certnames)
	}
	// ResultCount is what the bounded query returned, not a total.
	if estimate.ResultCount != 4 {
		t.Errorf("result count = %d, want 4 (limit+1 received)", estimate.ResultCount)
	}
}

// Exactly result_limit rows is not truncation.
func TestEstimate_ExactlyAtLimitIsNotTruncated(t *testing.T) {
	var captured capturedRequest
	q := serveRows(t, &captured, certnameRows("web-01", "web-02", "web-03"))

	estimate, _ := q.Estimate(context.Background(), nginx(), limits(3))
	if estimate.Truncated {
		t.Errorf("exactly result_limit rows must not be truncated: %+v", estimate)
	}
	if len(estimate.Certnames) != 3 {
		t.Errorf("certnames = %v", estimate.Certnames)
	}
}

func TestEstimate_EmptyResultIsACompletedEstimate(t *testing.T) {
	var captured capturedRequest
	q := serveRows(t, &captured, "[]")

	estimate, diag := q.Estimate(context.Background(), nginx(), limits(10))
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if estimate.Status != model.ImpactStatusCompleted {
		t.Errorf("status = %s, want completed", estimate.Status)
	}
	if estimate.ResultCount != 0 || len(estimate.Certnames) != 0 || estimate.Truncated {
		t.Errorf("expected an empty completed estimate: %+v", estimate)
	}
}

func TestEstimate_DeduplicatesRepeatedCertnames(t *testing.T) {
	var captured capturedRequest
	q := serveRows(t, &captured, certnameRows("web-01", "web-01", "web-02"))

	estimate, _ := q.Estimate(context.Background(), nginx(), limits(10))
	if estimate.ResultCount != 2 {
		t.Errorf("result count = %d, want 2 distinct certnames", estimate.ResultCount)
	}
}

func TestEstimate_NonSuccessStatusIsAFailedEstimateWithNoBodyEcho(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"PQL parse error near title = \"super-secret-node\""}`)
	})
	q := newQuerier(t, fixture, srv)

	estimate, diag := q.Estimate(context.Background(), nginx(), limits(10))
	if estimate.Status != model.ImpactStatusFailed {
		t.Errorf("status = %s, want failed", estimate.Status)
	}
	if estimate.FailureReason == "" {
		t.Error("expected a failure reason")
	}
	if strings.Contains(estimate.FailureReason, "super-secret-node") {
		t.Errorf("failure reason echoes the response body: %q", estimate.FailureReason)
	}
	if diag == nil {
		t.Fatal("expected a diagnostic")
	}
	if diag.Operation != model.OperationEstimateImpact || diag.Severity != model.SeverityError {
		t.Errorf("diagnostic = %+v", diag)
	}
	if diag.Source != pkgNginx {
		t.Errorf("diagnostic source = %q, want %q", diag.Source, pkgNginx)
	}
	// The failed estimate still records what it tried.
	if estimate.PQL == "" || estimate.Request.Path == "" {
		t.Errorf("a failed estimate must still report its query scope: %+v", estimate)
	}
}

func TestEstimate_MalformedResponseIsAFailedEstimate(t *testing.T) {
	for _, body := range []string{`{"not":"an array"}`, `[{"other":"field"}]`, `not json at all`} {
		var captured capturedRequest
		q := serveRows(t, &captured, body)
		estimate, diag := q.Estimate(context.Background(), nginx(), limits(10))
		if estimate.Status != model.ImpactStatusFailed {
			t.Errorf("body %q: status = %s, want failed", body, estimate.Status)
		}
		if diag == nil {
			t.Errorf("body %q: expected a diagnostic", body)
		}
		if len(estimate.Certnames) != 0 {
			t.Errorf("body %q: a malformed response must not yield certnames: %v", body, estimate.Certnames)
		}
	}
}

// A per-query deadline is the resolved impact timeout, and exceeding it
// is a distinct timeout status.
func TestEstimate_DeadlineExceededIsATimeoutStatus(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	release := make(chan struct{})
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		<-release
		fmt.Fprint(w, "[]")
	})
	t.Cleanup(func() { close(release) })
	q := newQuerier(t, fixture, srv)

	estimate, diag := q.Estimate(context.Background(), nginx(), Limits{Timeout: 50 * time.Millisecond, ResultLimit: 10})

	if estimate.Status != model.ImpactStatusTimeout {
		t.Errorf("status = %s, want timeout", estimate.Status)
	}
	if estimate.Timeout != "50ms" {
		t.Errorf("timeout = %q", estimate.Timeout)
	}
	if diag == nil || diag.Operation != model.OperationEstimateImpact {
		t.Errorf("diagnostic = %+v", diag)
	}
}

// An identity whose title cannot be encoded is skipped with a reported
// diagnostic and never sent as a query.
func TestEstimate_UnencodableIdentityFailsWithoutARequest(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	requested := false
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		requested = true
		fmt.Fprint(w, "[]")
	})
	q := newQuerier(t, fixture, srv)

	identity := model.ResourceIdentity{Type: "File", Title: "/etc/we\x00ird"}
	estimate, diag := q.Estimate(context.Background(), identity, limits(10))

	if requested {
		t.Error("no request may be issued for an unencodable identity")
	}
	if estimate.Status != model.ImpactStatusFailed {
		t.Errorf("status = %s, want failed", estimate.Status)
	}
	if estimate.PQL != "" {
		t.Errorf("no PQL should be reported when none could be built: %q", estimate.PQL)
	}
	if diag == nil {
		t.Fatal("expected a diagnostic")
	}
	if strings.Contains(diag.Message, "\x00") {
		t.Errorf("diagnostic echoes the offending bytes: %q", diag.Message)
	}
	if diag.Source != identity.String() {
		t.Errorf("diagnostic source = %q, want the identity", diag.Source)
	}
}

// The estimate is never phrased or shaped as a prediction, and never
// carries anything but certnames.
func TestEstimate_ReportsOnlySafeFields(t *testing.T) {
	var captured capturedRequest
	q := serveRows(t, &captured, `[{"certname":"web-01","parameters":{"password":"hunter2"},"file":"/etc/x.pp"}]`)

	estimate, _ := q.Estimate(context.Background(), nginx(), limits(10))
	encoded, err := json.Marshal(estimate)
	if err != nil {
		t.Fatalf("marshaling estimate: %v", err)
	}
	for _, leak := range []string{"hunter2", "password", "/etc/x.pp"} {
		if strings.Contains(string(encoded), leak) {
			t.Errorf("estimate leaks %q from the response: %s", leak, encoded)
		}
	}
}

// An absent certname key and an explicitly empty one are different
// wire-level failures and get different diagnostics; both fail the
// estimate rather than yielding a silently short result.
func TestEstimate_DistinguishesAbsentFromEmptyCertname(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"absent key", `[{"other":"field"}]`, "without a certname field"},
		{"empty value", `[{"certname":""}]`, "with an empty certname"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured capturedRequest
			q := serveRows(t, &captured, tc.body)
			estimate, diag := q.Estimate(context.Background(), nginx(), limits(10))
			if estimate.Status != model.ImpactStatusFailed {
				t.Fatalf("status = %s, want failed", estimate.Status)
			}
			if !strings.Contains(estimate.FailureReason, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", estimate.FailureReason, tc.want)
			}
			if diag == nil {
				t.Error("expected a diagnostic")
			}
		})
	}
}

// A body of `null` decodes into a nil slice without error, so without
// the shape check it would arrive as "no node is affected". An impact
// estimate reporting zero nodes is a claim a reader acts on, and it must
// not be produced by a response that said nothing.
//
// A deployed PuppetDB 8.15.0, measured on 2026-09-09, returns `[]` with
// HTTP 200 for a query matching nothing, so this rejects a body that
// service does not send rather than one it does.
func TestEstimate_NullBodyIsMalformedNotEmpty(t *testing.T) {
	for _, body := range []string{"null", " null ", `{"certname":"web-01"}`, `"[]"`, ""} {
		var captured capturedRequest
		q := serveRows(t, &captured, body)

		estimate, diag := q.Estimate(context.Background(), nginx(), limits(10))
		if diag == nil {
			t.Errorf("body %q produced no diagnostic", body)
		}
		if estimate.Status != model.ImpactStatusFailed {
			t.Errorf("body %q: status = %s, want failed", body, estimate.Status)
		}
		if estimate.ResultCount != 0 || len(estimate.Certnames) != 0 {
			t.Errorf("body %q: a malformed response produced an estimate: %+v", body, estimate)
		}
	}
}
