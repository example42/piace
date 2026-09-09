package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUserinfoRejectedAndEndpointCopied(t *testing.T) {
	u, _ := url.Parse("https://user:private@example.test")
	if _, err := New(u, "token", time.Second); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe constructor result: %v", err)
	}
	u.User = nil
	c, err := New(u, "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("user", "private")
	u.Host = "attacker.invalid"
	if c.url.User != nil || c.Authority() != "example.test" {
		t.Fatal("caller mutated client's validated endpoint")
	}
}

func TestQuerySentButNotDisclosedOnFailure(t *testing.T) {
	var observedQuery, observedAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedQuery, observedAuth = r.URL.Query().Get("token"), r.Header.Get("Authorization")
		w.WriteHeader(503)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/?token=private&api-version=2024-10-21")
	var event Event
	c, err := New(u, "legitimate-token", time.Second, WithObserver(func(e Event) { event = e }))
	if err != nil {
		t.Fatal(err)
	}
	c.SetHTTPClient(srv.Client())
	_, err = c.Complete(context.Background(), sampleRequest())
	if observedQuery != "private" || observedAuth != "Bearer legitimate-token" {
		t.Fatal("required request metadata was lost")
	}
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(event.URL, "private") || !strings.Contains(event.URL, "api-version=2024-10-21") {
		t.Fatal("unsafe error or debug URL")
	}
	srv.Close()
	_, err = c.Complete(context.Background(), sampleRequest())
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(event.Err.Error(), "private") {
		t.Fatal("transport failure disclosed query")
	}
}

func TestRedirectUserinfoNeverReachesService(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/start" {
			t.Error("followed userinfo redirect")
			return
		}
		http.Redirect(w, r, "https://user:private@"+r.Host+"/next", http.StatusFound)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/start")
	c, err := New(u, "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetHTTPClient(srv.Client())
	if _, err := c.Complete(context.Background(), sampleRequest()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe redirect result: %v", err)
	}
}
