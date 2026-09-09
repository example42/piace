package resolve

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// validateCertname checks the practical path-safety rule shared by
// certname format validation and template-path safety: a certname must
// not contain '/', '\\', a NUL byte, or the substring "..". Nothing
// specifies a fuller RFC or DNS-label certname grammar, so this
// intentionally does not attempt full DNS-label validation; see doc.go
// for that documented assumption.
func validateCertname(certname string) error {
	if certname == "" {
		return fmt.Errorf("certname is empty")
	}
	if strings.ContainsRune(certname, 0) {
		return fmt.Errorf("certname %q contains a NUL byte", certname)
	}
	if strings.ContainsAny(certname, `/\`) {
		return fmt.Errorf("certname %q contains a path separator", certname)
	}
	if strings.Contains(certname, "..") {
		return fmt.Errorf("certname %q contains \"..\"", certname)
	}
	return nil
}

// validateGlobSyntax test-compiles a title glob using the same dialect
// and case-sensitivity as evaluation (Go's path.Match), by
// matching it against a placeholder string and checking only for a
// syntax error. path.Match's return value (matched or not) is irrelevant
// here; only path.ErrBadPattern indicates a malformed pattern.
func validateGlobSyntax(pattern string) error {
	if _, err := path.Match(pattern, "x"); err != nil {
		return fmt.Errorf("invalid title glob %q: %w", pattern, err)
	}
	return nil
}

// certnameToken is the literal template placeholder recognized in a
// facts.file/baseline.file configuration value.
const certnameToken = "{certname}"

// expandCertnameSegment expands certnameToken within a single
// "/"-separated path component, enforcing the rule that the token may
// occur only as an entire path component.
//
// The documented examples fix the exact rule:
// `snapshots/catalogs/{certname}.json` is valid, the component's stem
// being exactly the token with a file extension suffix permitted, while
// `snapshots/{certname}-catalog.json` is invalid, extra text abutting
// the token with no separating extension dot. So the token must appear
// at the start of the component, and whatever follows it there must be
// empty or start with a ".": only a file extension may follow, no other
// prefix or suffix text is permitted, and the token may not repeat
// within one component.
func expandCertnameSegment(seg, certname string) (string, error) {
	if !strings.Contains(seg, certnameToken) {
		return seg, nil
	}
	if seg == certnameToken {
		return certname, nil
	}
	rest := strings.TrimPrefix(seg, certnameToken)
	if rest == seg || !strings.HasPrefix(rest, ".") || strings.Contains(rest, certnameToken) {
		return "", fmt.Errorf(
			"\"{certname}\" must occupy an entire path component, optionally with a file extension (e.g. \"dir/{certname}.json\", not \"dir/{certname}-x.json\")",
		)
	}
	return certname + rest, nil
}

// resolveFilePath resolves a `facts.file`/`baseline.file` value against
// the target-file directory:
//
//   - "{certname}" may occur only as an entire path component (see
//     expandCertnameSegment for the exact rule, including the permitted
//     file-extension suffix);
//   - certname itself is assumed already validated by validateCertname
//     (certname format is validated before any path templating,
//     so a certname cannot inject an extra path separator or "..");
//   - an explicit absolute path is resolved as-is and is exempt from the
//     "must stay beneath the target-file directory" check;
//   - a relative path is joined against targetFileDir, and the joined,
//     cleaned result must not escape targetFileDir.
//
// raw is empty only when the caller has already determined a file value
// is not required (facts.source/baseline.source is not "file"); callers
// must not invoke resolveFilePath for an empty raw value expected to be
// present, and must check presence themselves.
func resolveFilePath(raw, targetFileDir, certname string) (string, error) {
	if raw == "" {
		return "", nil
	}

	segments := strings.Split(raw, "/")
	for i, seg := range segments {
		expanded, err := expandCertnameSegment(seg, certname)
		if err != nil {
			return "", fmt.Errorf("file %q: %w", raw, err)
		}
		segments[i] = expanded
	}
	expandedSlash := strings.Join(segments, "/")
	nativePath := filepath.FromSlash(expandedSlash)

	// Configuration paths use the "/"-separated convention PIACE's own
	// examples use throughout ("snapshots/catalogs/{certname}.json");
	// absoluteness is judged on that convention via path.IsAbs rather than
	// the host OS's filepath.IsAbs, so behavior does not vary by build
	// platform.
	if path.IsAbs(expandedSlash) {
		return filepath.Clean(nativePath), nil
	}

	joined := filepath.Join(targetFileDir, nativePath)

	absDir, err := filepath.Abs(targetFileDir)
	if err != nil {
		return "", fmt.Errorf("file %q: resolving target-file directory: %w", raw, err)
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("file %q: resolving path: %w", raw, err)
	}
	rel, err := filepath.Rel(absDir, absJoined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file %q escapes the target-file directory via path traversal", raw)
	}

	return joined, nil
}

// validateHTTPSEndpoint parses raw as a URL and requires an `https`
// scheme with a non-empty host: PIACE accepts only https endpoints, and
// an unsafe one is rejected rather than normalized.
func validateHTTPSEndpoint(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("endpoint is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("endpoint is not a valid URL")
	}
	if u.User != nil {
		return nil, fmt.Errorf("endpoint userinfo is forbidden")
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("unsafe endpoint: scheme must be https")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("unsafe endpoint: missing host")
	}
	return u, nil
}

// validateTLSPath checks a CA bundle, client certificate or private key
// configuration value for syntactic validity only: non-empty and free of
// NUL bytes, which no filesystem accepts. It intentionally does not
// check existence or readability, which crosses into
// internal/transport's concern once configuration is fully valid and TLS
// transports are built. See doc.go.
func validateTLSPath(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s %q contains a NUL byte", kind, value)
	}
	return nil
}
