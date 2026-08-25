package filecontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/example42/piace/internal/transport"
)

// CompilerContentResolver is the compiler-backed ContentRetriever
// implementation: design.md's named "ContentResolver.Digest(reference,
// context) -> DigestEvidence" interface, built against *transport.Client
// (task 3) exactly as internal/puppetdb's and internal/compiler's
// adapters are. See doc.go's "ContentResolver and its documented,
// unverified endpoint assumption" section for the exact request shape
// this type issues and why it is flagged unverified.
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
	mountPath, ok := parsePuppetSourceURI(reference)
	if !ok {
		return DigestEvidence{}, fmt.Errorf("filecontent: source reference does not use a supported puppet:// scheme for compiler-mediated retrieval")
	}

	u := *r.baseURL
	u.Path = "/puppet/v3/file_content/" + mountPath
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// resp.Body is deliberately never included in the returned error:
		// design.md's Error Handling section applies here exactly as it
		// does to every other adapter in this codebase ("They do not
		// preserve raw body text by default, because service errors can
		// echo values"), and this package's stricter rule -- retrieved
		// content bytes never cross into any returned/logged string --
		// makes that doubly true for a file-content endpoint response.
		return DigestEvidence{}, fmt.Errorf("filecontent: compiler returned a non-2xx status (%d) retrieving referenced content", resp.StatusCode)
	}

	sum := sha256.Sum256(resp.Body)
	return DigestEvidence{Algorithm: contentDigestAlgorithm, Digest: hex.EncodeToString(sum[:])}, nil
}

// parsePuppetSourceURI recognizes a Puppet File `source` value of the
// documented form `puppet:///<mount-point>/<name>` (see doc.go) and
// returns the `<mount-point>/<name>` path segment the compiler's
// file_content endpoint expects, with its leading slash trimmed. Puppet's
// documented form also permits an explicit server authority
// (`puppet://<server>/<mount-point>/<name>`); PIACE always retrieves
// through its own configured compiler endpoint regardless of any server
// name embedded in the reference, so an authority component (if present)
// is accepted but ignored rather than rejected.
func parsePuppetSourceURI(reference string) (mountPath string, ok bool) {
	const scheme = "puppet://"
	if !strings.HasPrefix(reference, scheme) {
		return "", false
	}
	rest := reference[len(scheme):]
	// rest is "<authority><path>" where authority is empty for the
	// documented puppet:///modules/... form (three slashes: scheme "//"
	// plus an empty authority immediately followed by "/"). Split off
	// everything up to and including the first '/' as the (possibly
	// empty) authority.
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return "", false
	}
	path := strings.TrimPrefix(rest[idx:], "/")
	if path == "" {
		return "", false
	}
	return path, true
}

var _ ContentRetriever = (*CompilerContentResolver)(nil)
