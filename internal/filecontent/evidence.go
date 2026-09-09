package filecontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"

	"github.com/example42/piace/internal/model"
)

const contentDigestAlgorithm = "sha256"

// Side binds one resource to the catalog evidence it belongs to.
type Side struct {
	Resource model.Resource
	Context  model.ContentContext
}

// NeedsEvidence also selects unchanged references: a source string is not an
// identity for the bytes served from an environment.
func NeedsEvidence(r model.Resource) bool {
	return r.Identity.Type == "File" && (r.Parameters["content"] != nil || r.Parameters["source"] != nil ||
		r.Parameters["checksum_value"] != nil || r.StaticContent != nil || r.CapturedContent != nil || r.RecursiveContent)
}

// Membership evidence describes only the catalog side that exists. Removing a
// resource from a catalog does not assert that Puppet will delete its file.
func ResolveMembershipEvidence(ctx context.Context, certname string, kind model.ChangeKind, side Side, retriever ContentRetriever) (model.FileContentEvidence, *model.Diagnostic) {
	digest, source, err := ResolveSide(ctx, certname, side, retriever)
	s := &model.FileSideEvidence{Context: side.Context, Source: source, Verified: err == nil}
	e := model.FileContentEvidence{EvidenceSource: source}
	if kind == model.ChangeResourceAdded {
		e.State, e.After = model.FileContentAdded, s
	} else {
		e.State, e.Before = model.FileContentRemoved, s
	}
	if err != nil {
		e.State = model.FileContentIndeterminate
		reason := "resource membership changed; content evidence for the existing catalog side could not be verified"
		if errors.Is(err, ErrNonByteComparable) {
			reason = "resource membership changed; directory, recursive or non-file byte evidence is unsupported"
		} else if errors.Is(err, errHistoricalEvidence) {
			reason = "resource membership changed; historical catalog has no retained content digest"
		} else if errors.Is(err, errInvalidChecksum) {
			reason = "resource membership changed; invalid content checksum"
		}
		d := verifyContentDiagnostic(model.SeverityError, certname, side.Resource.Identity, reason)
		return e, &d
	}
	e.Algorithm = digest.Algorithm
	if kind == model.ChangeResourceAdded {
		e.AfterDigest = digest.Digest
	} else {
		e.BeforeDigest = digest.Digest
	}
	return e, nil
}

func ResolveFileContentEvidence(ctx context.Context, certname string, identity model.ResourceIdentity, before, after Side, retriever ContentRetriever) (model.FileContentEvidence, *model.Diagnostic) {
	bp, ap := parameters(before.Resource), parameters(after.Resource)
	referenceChanged := !reflect.DeepEqual(bp["source"], ap["source"])
	e := model.FileContentEvidence{
		State:            model.FileContentIndeterminate,
		Before:           &model.FileSideEvidence{Context: before.Context},
		After:            &model.FileSideEvidence{Context: after.Context},
		ReferenceChanged: referenceChanged,
	}
	if nonByteComparable(before.Resource, bp) || nonByteComparable(after.Resource, ap) {
		severity := model.SeverityError
		if referenceChanged {
			e.State, severity = model.FileContentReferenceChanged, model.SeverityWarning
		}
		d := verifyContentDiagnostic(severity, certname, identity, "directory, recursive or non-file source: byte-level content comparison is unsupported; recursive sourceselect rules are not evaluated")
		return e, &d
	}
	b, bs, be := ResolveSide(ctx, certname, before, retriever)
	a, as, ae := ResolveSide(ctx, certname, after, retriever)
	e.Before.Source, e.Before.Verified = bs, be == nil
	e.After.Source, e.After.Verified = as, ae == nil
	if be == nil && ae == nil && b.Algorithm == a.Algorithm {
		e.State = stateFromDigests(b.Digest, a.Digest)
		e.Algorithm, e.BeforeDigest, e.AfterDigest = b.Algorithm, b.Digest, a.Digest
		e.EvidenceSource = bs
		if bs != as {
			e.EvidenceSource = model.FileContentEvidenceMixed
		}
		return e, nil
	}
	if referenceChanged && retriever == nil {
		e.State = model.FileContentReferenceChanged
	}
	reason := "content retrieval or comparison could not establish comparable bytes for this resource"
	if errors.Is(be, errHistoricalEvidence) || errors.Is(ae, errHistoricalEvidence) {
		reason = "historical catalog has no retained content digest; current environment bytes cannot verify historical content"
	} else if errors.Is(be, errInvalidChecksum) || errors.Is(ae, errInvalidChecksum) {
		reason = "invalid content checksum: expected a supported algorithm and a full hexadecimal digest"
	}
	d := verifyContentDiagnostic(model.SeverityError, certname, identity, reason)
	return e, &d
}

// ResolveSide's order applies independently, so inline and captured/static
// evidence can be compared without retrieving either historical side.
func ResolveSide(ctx context.Context, certname string, side Side, retriever ContentRetriever) (DigestEvidence, model.FileContentEvidenceSource, error) {
	p := parameters(side.Resource)
	if nonByteComparable(side.Resource, p) {
		return DigestEvidence{}, "", ErrNonByteComparable
	}
	if content, ok := getStringParam(p, "content"); ok {
		return DigestEvidence{Algorithm: contentDigestAlgorithm, Digest: hashLocalContent(content)}, model.FileContentEvidenceInline, nil
	}
	if value, present := p["checksum_value"]; present && value != nil {
		v, ok := value.(string)
		algorithm := "sha256"
		if raw, present := p["checksum"]; present {
			var valid bool
			algorithm, valid = raw.(string)
			if !valid {
				return DigestEvidence{}, "", errInvalidChecksum
			}
		}
		if !ok {
			return DigestEvidence{}, "", errInvalidChecksum
		}
		d, err := validateDigest(algorithm, v)
		return d, model.FileContentEvidenceCompiledChecksum, err
	}
	if metadata := side.Resource.StaticContent; metadata != nil {
		d, err := validateDigest(metadata.Checksum.Type, metadata.Checksum.Value)
		return d, model.FileContentEvidenceStaticMetadata, err
	}
	if captured := side.Resource.CapturedContent; captured != nil {
		d, err := validateDigest(captured.Algorithm, captured.Digest)
		return d, model.FileContentEvidenceCaptured, err
	}
	if side.Context.Historical {
		return DigestEvidence{}, "", errHistoricalEvidence
	}
	refs, err := references(p["source"])
	if err != nil {
		return DigestEvidence{}, "", err
	}
	if retriever == nil {
		return DigestEvidence{}, "", errRetrieverUnavailable
	}
	if side.Context.Environment == "" {
		return DigestEvidence{}, "", errMissingEnvironment
	}
	rc := RetrievalContext{Certname: certname, Identity: side.Resource.Identity, Environment: side.Context.Environment}
	for _, ref := range refs {
		d, err := retriever.Digest(ctx, ref, rc)
		if errors.Is(err, ErrSourceNotFound) {
			continue
		}
		if err != nil {
			return DigestEvidence{}, "", err
		}
		d, err = validateDigest(d.Algorithm, d.Digest)
		return d, model.FileContentEvidenceCompilerRetrieval, err
	}
	return DigestEvidence{}, "", ErrSourceNotFound
}

func validateDigest(algorithm, value string) (DigestEvidence, error) {
	algorithm = strings.ToLower(algorithm)
	lengths := map[string]int{"md5": 32, "sha256": 64, "sha224": 56, "sha384": 96, "sha512": 128}
	n, ok := lengths[algorithm]
	if !ok {
		return DigestEvidence{}, errInvalidChecksum
	}
	if strings.HasPrefix(value, "{") {
		end := strings.IndexByte(value, '}')
		if end < 0 || strings.ToLower(value[1:end]) != algorithm {
			return DigestEvidence{}, errInvalidChecksum
		}
		value = value[end+1:]
	}
	if len(value) != n {
		return DigestEvidence{}, errInvalidChecksum
	}
	if _, err := hex.DecodeString(value); err != nil {
		return DigestEvidence{}, errInvalidChecksum
	}
	return DigestEvidence{Algorithm: algorithm, Digest: strings.ToLower(value)}, nil
}

func references(value any) ([]string, error) {
	switch v := value.(type) {
	case string:
		if v != "" {
			return []string{v}, nil
		}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, errNoContentOrSource
			}
			out = append(out, s)
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, errNoContentOrSource
}

func parameters(r model.Resource) map[string]any {
	out := make(map[string]any, len(r.Parameters))
	for k, v := range r.Parameters {
		for {
			m, ok := v.(map[string]any)
			if !ok || m["__ptype"] != "Sensitive" {
				break
			}
			v = m["__pvalue"]
		}
		out[k] = v
	}
	return out
}

func nonByteComparable(r model.Resource, p map[string]any) bool {
	if r.RecursiveContent || isDirectoryOrRecursive(p) {
		return true
	}
	if m := r.StaticContent; m != nil && m.Type != "file" {
		return true
	}
	if ensure, ok := getStringParam(p, "ensure"); ok && (ensure == "absent" || ensure == "link") {
		return true
	}
	return false
}

func isDirectoryOrRecursive(p map[string]model.Value) bool {
	if ensure, _ := getStringParam(p, "ensure"); strings.EqualFold(ensure, "directory") {
		return true
	}
	switch v := p["recurse"].(type) {
	case bool:
		return v
	case string:
		return v != "" && !strings.EqualFold(v, "false") && v != "0"
	case model.Number:
		return string(v) != "0"
	}
	return false
}

func getStringParam(p map[string]model.Value, key string) (string, bool) {
	s, ok := p[key].(string)
	return s, ok
}

func stateFromDigests(before, after string) model.FileContentState {
	if before == after {
		return model.FileContentUnchanged
	}
	return model.FileContentChanged
}

func hashLocalContent(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func verifyContentDiagnostic(severity model.DiagnosticSeverity, certname string, identity model.ResourceIdentity, reason string) model.Diagnostic {
	return model.Diagnostic{Severity: severity, Operation: model.OperationVerifyContent, Certname: certname, Message: identity.String() + ": " + reason}
}

var (
	ErrSourceNotFound     = errors.New("filecontent: source was not found")
	errHistoricalEvidence = errors.New("filecontent: historical content evidence is unavailable")
	errInvalidChecksum    = errors.New("filecontent: invalid checksum")
	ErrNonByteComparable  = errors.New("filecontent: directory, recursive or non-file byte comparison is unsupported")
	errMissingEnvironment = errors.New("filecontent: retrieval requires an environment")
)
