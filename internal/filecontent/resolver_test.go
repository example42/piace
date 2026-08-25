package filecontent

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestParsePuppetSourceURI(t *testing.T) {
	cases := []struct {
		reference string
		wantPath  string
		wantOK    bool
	}{
		{"puppet:///modules/example/data.txt", "modules/example/data.txt", true},
		{"puppet://compiler.example.test/modules/example/data.txt", "modules/example/data.txt", true},
		{"puppet:///modules/example/nested/dir/data.txt", "modules/example/nested/dir/data.txt", true},
		{"puppet:///", "", false},
		{"file:///etc/motd", "", false},
		{"https://example.test/data.txt", "", false},
		{"/etc/motd", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		gotPath, gotOK := parsePuppetSourceURI(tc.reference)
		if gotOK != tc.wantOK || gotPath != tc.wantPath {
			t.Errorf("parsePuppetSourceURI(%q) = (%q, %v), want (%q, %v)",
				tc.reference, gotPath, gotOK, tc.wantPath, tc.wantOK)
		}
	}
}

// TestCompilerContentResolver_Digest_Success verifies a successful
// retrieval hashes the response body locally with sha256 and never
// returns the raw bytes.
func TestCompilerContentResolver_Digest_Success(t *testing.T) {
	const raw = "the quick brown fox"
	fixture := newTLSFixture(t, "127.0.0.1")
	var gotPath, gotQuery, gotAccept string
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAccept = r.Header.Get("Accept")
		// Mirrors the compiler's embedded Ruby Puppet request handler,
		// which rejects any /puppet/v3/ request with no Accept header
		// before serving anything (see doc.go).
		if gotAccept == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Bad Request: Missing required Accept header"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(raw))
	})
	resolver := newResolver(t, fixture, srv)

	digest, err := resolver.Digest(context.Background(), "puppet:///modules/example/data.txt",
		RetrievalContext{Certname: "web-01", Environment: "production"})
	if err != nil {
		t.Fatalf("Digest returned error: %v", err)
	}
	if digest.Algorithm != "sha256" {
		t.Errorf("Algorithm = %q, want sha256", digest.Algorithm)
	}
	wantDigest := hashLocalContent(raw)
	if digest.Digest != wantDigest {
		t.Errorf("Digest = %q, want %q", digest.Digest, wantDigest)
	}
	if gotPath != "/puppet/v3/file_content/modules/example/data.txt" {
		t.Errorf("request path = %q, want /puppet/v3/file_content/modules/example/data.txt", gotPath)
	}
	if !strings.Contains(gotQuery, "environment=production") {
		t.Errorf("request query = %q, want environment=production", gotQuery)
	}
	if gotAccept != "application/octet-stream" {
		t.Errorf("Accept = %q, want application/octet-stream", gotAccept)
	}
	if strings.Contains(digest.Digest, raw) {
		t.Errorf("digest leaks raw content: %q", digest.Digest)
	}
}

// TestCompilerContentResolver_Digest_NotFound verifies a non-2xx
// response is a plain error, never a digest, and the error text never
// carries response body content.
func TestCompilerContentResolver_Digest_NotFound(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found: Could not find file_content modules/example/missing.txt"))
	})
	resolver := newResolver(t, fixture, srv)

	_, err := resolver.Digest(context.Background(), "puppet:///modules/example/missing.txt",
		RetrievalContext{Certname: "web-01", Environment: "production"})
	if err == nil {
		t.Fatal("expected an error for a 404 response, got nil")
	}
	if strings.Contains(err.Error(), "missing.txt") && strings.Contains(err.Error(), "Not Found: Could not find") {
		t.Errorf("error text echoes response body: %v", err)
	}
}

// TestCompilerContentResolver_Digest_UnsupportedScheme verifies a
// non-puppet:// source reference is rejected before any request is
// made.
func TestCompilerContentResolver_Digest_UnsupportedScheme(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP request should be made for an unsupported source scheme")
	})
	resolver := newResolver(t, fixture, srv)

	_, err := resolver.Digest(context.Background(), "https://example.test/data.txt",
		RetrievalContext{Certname: "web-01", Environment: "production"})
	if err == nil {
		t.Fatal("expected an error for an unsupported source scheme, got nil")
	}
}
