package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// stubService is a stand-in inference service, in the pattern of
// internal/capture's compiler stub. No test in PIACE contacts a real
// inference service.
type stubService struct {
	server   *httptest.Server
	lastReq  map[string]any
	lastAuth string
	status   int
	body     string
}

func newStubService(t *testing.T) *stubService {
	t.Helper()
	s := &stubService{status: http.StatusOK, body: `{"choices":[{"message":{"content":"{\"run\":{}}"}}]}`}
	s.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &s.lastReq)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		io.WriteString(w, s.body)
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubService) client(t *testing.T, token string) *Client {
	t.Helper()
	u, err := url.Parse(s.server.URL)
	if err != nil {
		t.Fatalf("parsing stub URL: %v", err)
	}
	c, err := New(u, token, 5*time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.HTTPClient = s.server.Client()
	return c
}

func sampleRequest() Request {
	return Request{
		Model:     "test-model",
		MaxTokens: 4000,
		Messages:  []Message{{Role: "system", Content: "task"}, {Role: "user", Content: "data"}},
	}
}

// Slice 5.1: the bearer token reaches the service.
func TestClientSendsTheBearerToken(t *testing.T) {
	s := newStubService(t)
	if _, err := s.client(t, "s3cret").Complete(context.Background(), sampleRequest()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if s.lastAuth != "Bearer s3cret" {
		t.Errorf("Authorization = %q", s.lastAuth)
	}
}

// Slice 5.4: request options come from the caller and reach the wire.
func TestClientSendsTheConfiguredRequestOptions(t *testing.T) {
	s := newStubService(t)
	if _, err := s.client(t, "t").Complete(context.Background(), sampleRequest()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if s.lastReq["model"] != "test-model" {
		t.Errorf("model = %v", s.lastReq["model"])
	}
	if s.lastReq["max_tokens"] != float64(4000) {
		t.Errorf("max_tokens = %v", s.lastReq["max_tokens"])
	}
	if s.lastReq["temperature"] != float64(0) || s.lastReq["seed"] != float64(0) {
		t.Errorf("temperature/seed = %v/%v", s.lastReq["temperature"], s.lastReq["seed"])
	}
}

// Slice 5.2: https only, and no empty token.
func TestNewRejectsAnUnsafeEndpoint(t *testing.T) {
	for name, raw := range map[string]string{
		"http":         "http://api.example.com/v1/chat/completions",
		"no scheme":    "api.example.com/v1/chat/completions",
		"other scheme": "ftp://api.example.com/",
	} {
		t.Run(name, func(t *testing.T) {
			u, _ := url.Parse(raw)
			if _, err := New(u, "token", time.Second); err == nil {
				t.Errorf("New accepted %s", raw)
			}
		})
	}

	u, _ := url.Parse("https://api.example.com/v1/chat/completions")
	if _, err := New(u, "", time.Second); err == nil {
		t.Error("New accepted an empty token")
	}
}

// Slice 5.5: a rejected request names the status and echoes no body.
func TestClientReportsAStatusWithoutEchoingTheBody(t *testing.T) {
	s := newStubService(t)
	s.status = http.StatusTooManyRequests
	s.body = `{"error":{"message":"org proj-9f3 over quota, contact billing@customer.example"}}`

	_, err := s.client(t, "t").Complete(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("Complete accepted a 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error does not name the status: %v", err)
	}
	if strings.Contains(err.Error(), "billing@customer.example") || strings.Contains(err.Error(), "proj-9f3") {
		t.Errorf("error echoes the service's response body: %v", err)
	}
}

// The assistant's message content is what an assessment is parsed from;
// anything else in the envelope is the service's business, not PIACE's.
func TestClientReturnsTheMessageContent(t *testing.T) {
	s := newStubService(t)
	s.body = `{"choices":[{"message":{"role":"assistant","content":"{\"run\":{\"risk\":\"low\"}}"}}]}`

	got, err := s.client(t, "t").Complete(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if string(got) != `{"run":{"risk":"low"}}` {
		t.Errorf("Complete = %s", got)
	}
}

func TestClientRejectsAnEnvelopeWithNoContent(t *testing.T) {
	for name, body := range map[string]string{
		"no choices":    `{"choices":[]}`,
		"not an object": `[]`,
		"empty content": `{"choices":[{"message":{"content":""}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := newStubService(t)
			s.body = body
			if _, err := s.client(t, "t").Complete(context.Background(), sampleRequest()); err == nil {
				t.Errorf("Complete accepted %s", name)
			}
		})
	}
}

// Slice 5.4: the deadline is the caller's, and exceeding it is an
// ordinary error rather than a hang.
func TestClientHonoursItsTimeout(t *testing.T) {
	// The handler waits, but not indefinitely: httptest.Server.Close
	// blocks on outstanding requests, so a handler that never returns
	// hangs the whole package rather than failing one test.
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)

	u, _ := url.Parse(slow.URL)
	c, err := New(u, "t", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.HTTPClient = slow.Client()

	if _, err := c.Complete(context.Background(), sampleRequest()); err == nil {
		t.Error("Complete returned before its deadline elapsed")
	}
}
