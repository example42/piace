package filecontent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestParsePuppetSourceURI(t *testing.T) {
	cases := []struct {
		reference string
		wantPath  string
		wantErr   error
	}{
		{"puppet:///modules/example/data.txt", "modules/example/data.txt", nil},
		{"puppet://compiler.example.test/modules/example/data.txt", "modules/example/data.txt", nil},
		{"puppet:///modules/example/nested/dir/data.txt", "modules/example/nested/dir/data.txt", nil},
		{"puppet:///", "", errUnsupportedSourceScheme},
		{"file:///etc/motd", "", errUnsupportedSourceScheme},
		{"https://example.test/data.txt", "", errUnsupportedSourceScheme},
		{"/etc/motd", "", errUnsupportedSourceScheme},
		{"", "", errUnsupportedSourceScheme},

		// A `source` value is a parameter of a File resource in the
		// candidate catalog, compiled from the change under review, so
		// these are the shapes that must not become a request path. Each
		// one, left alone, would leave the file_content endpoint and reach
		// another path on the compiler under PIACE's catalog-reader
		// identity: net/url does not remove dot segments from a path it is
		// handed, and net/http sends the request line as written.
		{"puppet:///../../pdb/query/v4/catalogs/victim.example.test", "", errUnsafeSourcePath},
		{"puppet:///modules/../../../puppet/v3/environments", "", errUnsafeSourcePath},
		{"puppet://compiler.example.test/../../status/v1/services", "", errUnsafeSourcePath},
		{"puppet:///modules/./example/data.txt", "", errUnsafeSourcePath},
		{"puppet:///modules//example/data.txt", "", errUnsafeSourcePath},
		{"puppet:///modules/example/data.txt\x00.png", "", errUnsafeSourcePath},

		// A ".." inside a segment is an ordinary, if odd, file name and
		// stays retrievable: only a whole segment of ".." traverses.
		{"puppet:///modules/example/..data.txt", "modules/example/..data.txt", nil},
	}
	for _, tc := range cases {
		gotPath, gotErr := parsePuppetSourceURI(tc.reference)
		if !errors.Is(gotErr, tc.wantErr) || gotPath != tc.wantPath {
			t.Errorf("parsePuppetSourceURI(%q) = (%q, %v), want (%q, %v)",
				tc.reference, gotPath, gotErr, tc.wantPath, tc.wantErr)
		}
	}
}

// TestCompilerContentResolver_Digest_RefusesTraversal is the end-to-end
// half of the case above: a traversing `source` must never reach the
// wire at all, so the assertion is that the server's handler is not
// entered, not merely that Digest returned an error.
func TestCompilerContentResolver_Digest_RefusesTraversal(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	requested := false
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		requested = true
		w.WriteHeader(http.StatusOK)
	})
	resolver := newResolver(t, fixture, srv)
	_, err := resolver.Digest(context.Background(),
		"puppet:///../../pdb/query/v4/catalogs/victim.example.test",
		RetrievalContext{Certname: "node.example.test", Environment: "production"})
	if err == nil {
		t.Fatal("Digest accepted a traversing source reference, want an error")
	}
	if !errors.Is(err, errUnsafeSourcePath) {
		t.Errorf("Digest error = %v, want errUnsafeSourcePath", err)
	}
	if requested {
		t.Error("a traversing source reference reached the compiler; it must be refused before any request is issued")
	}
	// The reference itself must not be echoed back: it is catalog data and
	// this error becomes a diagnostic that reaches CI logs and reports.
	if strings.Contains(err.Error(), "victim.example.test") {
		t.Errorf("Digest error quotes the source reference: %v", err)
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
