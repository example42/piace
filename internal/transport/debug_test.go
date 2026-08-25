package transport

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestDescribeBody(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantShape BodyShape
		wantKeys  []string
	}{
		{"empty", "", ShapeEmpty, nil},
		{"v4 envelope", `{"catalog": {"name": "web-01", "resources": []}}`, ShapeObject, []string{"catalog"}},
		{"v3 document", `{"name": "web-01", "version": 1, "resources": [], "edges": []}`, ShapeObject,
			[]string{"name", "version", "resources", "edges"}},
		{"array", `[1, 2, 3]`, ShapeArray, nil},
		{"scalar", `"hello"`, ShapeScalar, nil},
		{"non-json", `{not json`, ShapeNonJSON, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shape, keys, truncated := describeBody([]byte(tc.body))
			if shape != tc.wantShape {
				t.Errorf("shape = %q, want %q", shape, tc.wantShape)
			}
			if strings.Join(keys, ",") != strings.Join(tc.wantKeys, ",") {
				t.Errorf("keys = %v, want %v", keys, tc.wantKeys)
			}
			if truncated {
				t.Error("truncated = true, want false")
			}
		})
	}
}

// TestDescribeBody_NeverReturnsValues asserts the one property that makes
// an Event safe to print to a CI log under requirements.md 3.5: only
// top-level member *names* are collected, never member values, however
// deeply the value nests.
func TestDescribeBody_NeverReturnsValues(t *testing.T) {
	body := `{"catalog": {"resources": [{"parameters": {"password": "s3cret"}}]}}`
	shape, keys, _ := describeBody([]byte(body))
	if shape != ShapeObject {
		t.Fatalf("shape = %q, want %q", shape, ShapeObject)
	}
	if len(keys) != 1 || keys[0] != "catalog" {
		t.Fatalf("keys = %v, want [catalog]", keys)
	}
	for _, k := range keys {
		if strings.Contains(k, "s3cret") {
			t.Errorf("member value leaked into keys: %q", k)
		}
	}
}

func TestDescribeBody_TruncatesManyKeys(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < maxTopLevelKeys+10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"k`)
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(string(rune('a' + i/26)))
		b.WriteString(`": 1`)
	}
	b.WriteString("}")

	shape, keys, truncated := describeBody([]byte(b.String()))
	if shape != ShapeObject {
		t.Fatalf("shape = %q, want %q", shape, ShapeObject)
	}
	if len(keys) != maxTopLevelKeys {
		t.Errorf("len(keys) = %d, want %d", len(keys), maxTopLevelKeys)
	}
	if !truncated {
		t.Error("truncated = false, want true")
	}
}

func TestClient_Do_EmitsObserverEvent(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"catalog": {"name": "web-01"}}`))
	})
	defer srv.Close()

	var events []Event
	client, err := NewClient(fixture.endpointFor(t, srv.URL), WithObserver(func(ev Event) {
		events = append(events, ev)
	}))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req, err := client.NewRequest(context.Background(), http.MethodPost, srv.URL+"/puppet/v4/catalog", strings.NewReader(`{"certname":"web-01"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := client.Do(req, 0); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Method != http.MethodPost || !strings.HasSuffix(ev.URL, "/puppet/v4/catalog") {
		t.Errorf("Method/URL = %s %s", ev.Method, ev.URL)
	}
	if ev.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", ev.StatusCode)
	}
	if ev.Shape != ShapeObject || len(ev.TopLevelKeys) != 1 || ev.TopLevelKeys[0] != "catalog" {
		t.Errorf("Shape/TopLevelKeys = %q/%v, want object/[catalog]", ev.Shape, ev.TopLevelKeys)
	}
	if ev.RequestBodyBytes != int64(len(`{"certname":"web-01"}`)) {
		t.Errorf("RequestBodyBytes = %d", ev.RequestBodyBytes)
	}
	// Without WithBodyCapture, no raw body reaches the observer.
	if ev.RequestBody != nil || ev.ResponseBody != nil {
		t.Error("Event carries raw bodies without WithBodyCapture")
	}
}

func TestClient_Do_BodyCaptureCarriesRawBodies(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"catalog": {"name": "web-01"}}`))
	})
	defer srv.Close()

	var ev Event
	client, err := NewClient(fixture.endpointFor(t, srv.URL),
		WithObserver(func(e Event) { ev = e }), WithBodyCapture(true))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	req, err := client.NewRequest(context.Background(), http.MethodPost, srv.URL+"/puppet/v4/catalog", strings.NewReader(`{"certname":"web-01"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := client.Do(req, 0); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if string(ev.RequestBody) != `{"certname":"web-01"}` {
		t.Errorf("RequestBody = %q", ev.RequestBody)
	}
	if string(ev.ResponseBody) != `{"catalog": {"name": "web-01"}}` {
		t.Errorf("ResponseBody = %q", ev.ResponseBody)
	}
}

// TestClient_Do_EmitsObserverEventOnTransportFailure asserts a request
// that never produced a response is still observed — the case an
// operator running --debug most needs to see.
func TestClient_Do_EmitsObserverEventOnTransportFailure(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {})
	ep := fixture.endpointFor(t, srv.URL)
	srv.Close() // nothing is listening now

	var ev Event
	seen := false
	client, err := NewClient(ep, WithObserver(func(e Event) { ev, seen = e, true }))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := client.NewRequest(context.Background(), http.MethodGet, srv.URL+"/pdb/query/v4", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := client.Do(req, 0); err == nil {
		t.Fatal("Do succeeded against a closed server")
	}
	if !seen {
		t.Fatal("no Event emitted for a transport failure")
	}
	if ev.StatusCode != 0 || ev.Err == nil {
		t.Errorf("Event = %+v, want StatusCode 0 and a non-nil Err", ev)
	}
}
