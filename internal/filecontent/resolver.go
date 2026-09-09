package filecontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/example42/piace/internal/transport"
)

// CompilerContentResolver is the compiler-backed ContentRetriever
// implementation, built against *transport.Client exactly as
// internal/puppetdb's and internal/compiler's adapters are. See doc.go for
// source-selection policy and the scope of the endpoint evidence.
type CompilerContentResolver struct {
	client  *transport.Client
	baseURL *url.URL
}

// NewCompilerContentResolver builds a CompilerContentResolver that issues
// requests against endpoint using client, mirroring
// internal/puppetdb.NewAdapter and internal/compiler.NewAdapter's
// construction pattern.
func NewCompilerContentResolver(client *transport.Client, endpoint *url.URL) *CompilerContentResolver {
	return &CompilerContentResolver{client: client, baseURL: endpoint}
}

// fileContentAcceptHeader is the Accept header sent with every
// file_content request. The file-content indirection serves only the
// binary format, so application/octet-stream is the single acceptable
// value -- offering application/json here would trade a rejected
// missing-Accept request for a rejected unacceptable-format one.
const fileContentAcceptHeader = "application/octet-stream"

// Digest implements ContentRetriever. It resolves reference (a Puppet
// File `source` value) into a DigestEvidence by retrieving the
// referenced bytes through the compiler's documented v3 file_content
// endpoint and hashing them locally with this package's single content
// digest algorithm (contentDigestAlgorithm) -- the retrieved bytes never
// leave this function.
//
// A reference using any URI scheme other than `puppet:` (a bare local
// path, `file:`, or `http(s):`) is not retrievable through this endpoint
// at all (see doc.go); Digest returns a descriptive-but-safe error for
// that case rather than attempting an unsupported request.
func (r *CompilerContentResolver) Digest(ctx context.Context, reference string, rc RetrievalContext) (DigestEvidence, error) {
	mountPath, err := parsePuppetSourceURI(reference)
	if err != nil {
		return DigestEvidence{}, err
	}

	u := *r.baseURL
	u.Path = "/puppet/v3/file_content/" + mountPath
	u.RawPath = ""
	q := url.Values{}
	q.Set("environment", rc.Environment)
	u.RawQuery = q.Encode()
	u.Fragment = ""

	req, err := r.client.NewRequest(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return DigestEvidence{}, fmt.Errorf("filecontent: building content retrieval request: %w", err)
	}
	// Non-optional: the v3 file_content endpoint is served by the
	// compiler's embedded Ruby Puppet request handler, which rejects a
	// request carrying no Accept header ("Missing required Accept
	// header") before serving anything. application/octet-stream is the
	// only content type this endpoint serves (see doc.go).
	req.Header.Set("Accept", fileContentAcceptHeader)

	resp, err := r.client.Do(req, 0)
	if err != nil {
		var te *transport.Error
		if errors.As(err, &te) {
			return DigestEvidence{}, fmt.Errorf("filecontent: retrieving referenced content: %s", te.Message)
		}
		return DigestEvidence{}, fmt.Errorf("filecontent: retrieving referenced content: %s", transport.SafeMessage(err))
	}

	if resp.StatusCode == http.StatusNotFound && sourceAbsent(resp.Body, mountPath) {
		return DigestEvidence{}, ErrSourceNotFound
	}
	if resp.StatusCode != http.StatusOK {
		// resp.Body is deliberately never included in the returned error. The
		// rule that adapters do not preserve raw body text, because a service
		// error can echo values back, applies here exactly as it does everywhere
		// else in this codebase, and this package's stricter rule that retrieved
		// content bytes never cross into any returned or logged string makes it
		// doubly true for a file-content endpoint response.
		return DigestEvidence{}, fmt.Errorf("filecontent: compiler returned status %d instead of 200 retrieving referenced content", resp.StatusCode)
	}

	sum := sha256.Sum256(resp.Body)
	return DigestEvidence{Algorithm: contentDigestAlgorithm, Digest: hex.EncodeToString(sum[:])}, nil
}

// parsePuppetSourceURI recognizes a Puppet File `source` value of the
// documented form `puppet:///<mount-point>/<name>` (see doc.go) and
// returns the `<mount-point>/<name>` path segment the compiler's
// file_content endpoint expects, with its leading slash trimmed. Puppet's
// documented form also permits explicit authorities, but PIACE supports only
// authority-free references through its configured compiler. Explicit source
// authorities fail without a request.
//
// The returned path is the one value in this package that a caller
// concatenates onto a request path, and a `source` value is not operator
// configuration: it is a parameter of a File resource in the candidate
// catalog, which was compiled from the very change under review. So the
// path is checked here rather than trusted. A "." or ".." segment is
// refused outright: net/url neither removes nor escapes dot segments in
// a URL it is handed a path for, and net/http sends the request line as
// written, so `puppet:///../../pdb/query/v4/catalogs/<node>` would
// otherwise leave the file_content endpoint entirely and reach another
// path on the compiler under PIACE's own catalog-reader identity. An
// empty segment (a `//` run) is refused for the same reason: it changes
// which path the compiler resolves while looking like a typo.
//
// A NUL byte is refused as well, since it terminates a path for anything
// downstream written in C and has no business in a Puppet file
// reference.
func parsePuppetSourceURI(reference string) (mountPath string, err error) {
	const scheme = "puppet://"
	if !strings.HasPrefix(reference, scheme) {
		return "", errUnsupportedSourceScheme
	}
	rest := reference[len(scheme):]
	// rest is "<authority><path>" where authority is empty for the
	// documented puppet:///modules/... form (three slashes: scheme "//"
	// plus an empty authority immediately followed by "/"). Split off
	// everything up to and including the first '/' as the (possibly
	// empty) authority.
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return "", errUnsupportedSourceScheme
	}
	if rest[:idx] != "" {
		return "", errUnsupportedSourceAuthority
	}
	path := strings.TrimPrefix(rest[idx:], "/")
	if path == "" {
		return "", errUnsupportedSourceScheme
	}
	if strings.ContainsAny(path, "\x00\\?#") {
		return "", errUnsafeSourcePath
	}
	// Reject encoded separators and traversal too. Puppet escapes source paths
	// for transport, so a literal percent sequence must not bypass validation.
	decoded, err := url.PathUnescape(path)
	if err != nil || strings.ContainsAny(decoded, "\x00\\%") {
		return "", errUnsafeSourcePath
	}
	for _, seg := range strings.Split(strings.TrimSuffix(decoded, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", errUnsafeSourcePath
		}
	}
	// A trailing "/" is a directory reference the endpoint serves nothing
	// for, but it is already handled upstream (see doc.go's "Sources that
	// are not byte-comparable") and reaching here it is only an empty
	// final segment, so the split drops it rather than refusing the whole
	// reference for a reason the reader would not recognize.
	for _, seg := range strings.Split(strings.TrimSuffix(path, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", errUnsafeSourcePath
		}
	}
	return decoded, nil
}

// A router's generic 404 is not evidence that one source is absent. Match
// Puppet's file-content error contract without printing its echoed path.
func sourceAbsent(body []byte, mountPath string) bool {
	var response struct {
		IssueKind string `json:"issue_kind"`
	}
	if json.Unmarshal(body, &response) == nil && response.IssueKind == "RESOURCE_NOT_FOUND" {
		return true
	}
	return strings.TrimSpace(string(body)) == "Not Found: Could not find file_content "+mountPath
}

var _ ContentRetriever = (*CompilerContentResolver)(nil)

var errUnsupportedSourceAuthority = errors.New("filecontent: explicit source authorities are unsupported; use the configured compiler with an authority-free puppet source")
