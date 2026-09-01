package filecontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/example42/piace/internal/model"
)

// contentDigestAlgorithm is the single hashing algorithm this package
// uses throughout: for step 1 (inline content), for a side resolved
// locally in step 3, and the algorithm a ContentRetriever is contractually
// expected to report for a retrieved side (see interfaces.go's
// ContentRetriever doc comment and resolver.go's CompilerContentResolver).
// Keeping exactly one algorithm across every resolution path means a
// before/after digest pair is always directly comparable whenever both
// sides resolve successfully; see resolveSide's algorithm-mismatch
// handling in ResolveFileContentEvidence for the (contract-violation)
// fallback when a supplied ContentRetriever does not honor this.
const contentDigestAlgorithm = "sha256"

// contentParameter and sourceParameter are the two Puppet File parameter
// names this package inspects for content-bearing evidence. checksumParameter
// and checksumValueParameter are the two compiled-checksum parameter names;
// see doc.go's "Identifying a recognized compatible checksum" section.
const (
	contentParameter       = "content"
	sourceParameter        = "source"
	checksumParameter      = "checksum"
	checksumValueParameter = "checksum_value"
)

// ensureParameter and recurseParameter are the two Puppet File parameters
// that decide whether a `source` reference names a single file at all.
// They are not content-bearing themselves -- the differ never collapses
// them into a content change -- but they gate step 3, because the
// compiler's file_content endpoint serves a file's bytes and nothing
// else; see doc.go's "Sources that are not byte-comparable" section.
const (
	ensureParameter  = "ensure"
	recurseParameter = "recurse"
)

// directoryEnsureValue is the `ensure` value naming a directory, and
// recursiveRecurseValues is the set of `recurse` string values that turn
// a File resource into a recursive directory copy. Puppet documents
// `recurse`'s allowed values as true, false, remote, and inf ("inf" and a
// numeric depth being the deprecated spellings of unlimited/limited
// recursion); every spelling other than an explicit false means the
// `source` names a directory tree.
const directoryEnsureValue = "directory"

var recursiveRecurseValues = map[string]bool{
	"true":   true,
	"remote": true,
	"inf":    true,
}

// recognizedChecksumAlgorithms is the exact set of `checksum` algorithm
// names Puppet's `checksum_value` parameter documentation restricts
// itself to ("Only md5, sha256, sha224, sha384 and sha512 are supported
// when specifying this parameter"), lower-cased. mtime/ctime/none and
// every *lite variant are deliberately excluded; see doc.go.
var recognizedChecksumAlgorithms = map[string]bool{
	"md5":    true,
	"sha256": true,
	"sha224": true,
	"sha384": true,
	"sha512": true,
}

// defaultChecksumAlgorithm is Puppet's documented default `checksum`
// value ("The default checksum type is sha256") applied when a File
// resource sets `checksum_value` but omits `checksum` entirely.
const defaultChecksumAlgorithm = "sha256"

// ResolveFileContentEvidence implements the four-step priority order for
// one File resource's content-bearing parameters, comparing before
// (baseline) against after (candidate). certname and identity are used
// only for diagnostic and retrieval context, never echoed back with any
// parameter value; environment is the candidate environment a
// compiler-retrieved reference must be resolved within. retriever may be
// nil, meaning step 3 retrieval is unavailable for this call: see
// doc.go's reference_changed versus content_indeterminate rule, which
// treats a nil retriever as a distinct case from an attempted retrieval
// that failed.
//
// See doc.go for the full priority-order writeup and the
// reference_changed/content_indeterminate distinction; this function is
// the exact implementation of that decision tree.
func ResolveFileContentEvidence(
	ctx context.Context,
	certname string,
	environment string,
	identity model.ResourceIdentity,
	before, after map[string]model.Value,
	retriever ContentRetriever,
) (model.FileContentEvidence, *model.Diagnostic) {
	// Step 1: inline content on both sides.
	beforeContent, beforeHasContent := getStringParam(before, contentParameter)
	afterContent, afterHasContent := getStringParam(after, contentParameter)
	if beforeHasContent && afterHasContent {
		beforeDigest := hashLocalContent(beforeContent)
		afterDigest := hashLocalContent(afterContent)
		return model.FileContentEvidence{
			State:          stateFromDigests(beforeDigest, afterDigest),
			EvidenceSource: model.FileContentEvidenceInline,
			Algorithm:      contentDigestAlgorithm,
			BeforeDigest:   beforeDigest,
			AfterDigest:    afterDigest,
		}, nil
	}

	// Step 2: a recognized compatible compiled checksum on both sides.
	if evidence, ok := resolveCompiledChecksum(before, after); ok {
		return evidence, nil
	}

	// A directory or recursive File resource's `source` names a
	// directory tree, not a file. Step 3 cannot compare it: the
	// compiler's file_content endpoint serves a single file's bytes and
	// rejects a directory reference outright. Resolve it here, before
	// any retrieval is attempted, rather than issuing a request whose
	// failure would be reported as if content evidence had been lost.
	if isDirectoryOrRecursive(before) || isDirectoryOrRecursive(after) {
		evidence, diag := resolveNonByteComparable(certname, identity, before, after)
		return evidence, diag
	}

	// Step 3: retrieve whichever side needs it (a `source` reference,
	// when that side has no literal content), hash locally, and compare.
	rc := RetrievalContext{Certname: certname, Identity: identity, Environment: environment}
	beforeRes := resolveSide(ctx, before, rc, retriever)
	afterRes := resolveSide(ctx, after, rc, retriever)

	if beforeRes.err == nil && afterRes.err == nil &&
		beforeRes.digest.Algorithm != "" && beforeRes.digest.Algorithm == afterRes.digest.Algorithm {
		source := model.FileContentEvidenceCompilerRetrieval
		if beforeRes.hasLiteral && afterRes.hasLiteral {
			// Both sides resolved via literal content after all (e.g. one
			// side's content was empty-string, which getStringParam still
			// treats as present) -- keep this classified as inline rather
			// than compiler_retrieval, since no retrieval occurred.
			source = model.FileContentEvidenceInline
		}
		return model.FileContentEvidence{
			State:          stateFromDigests(beforeRes.digest.Digest, afterRes.digest.Digest),
			EvidenceSource: source,
			Algorithm:      beforeRes.digest.Algorithm,
			BeforeDigest:   beforeRes.digest.Digest,
			AfterDigest:    afterRes.digest.Digest,
		}, nil
	}

	// Step 4: comparable bytes could not be established for both sides.
	state, reason := classifyUnresolvedState(retriever, beforeRes, afterRes)
	diag := verifyContentDiagnostic(model.SeverityError, certname, identity, reason)
	return model.FileContentEvidence{State: state}, &diag
}

// stateFromDigests compares two hex digest strings and returns the
// corresponding FileContentState.
func stateFromDigests(before, after string) model.FileContentState {
	if before == after {
		return model.FileContentUnchanged
	}
	return model.FileContentChanged
}

// hashLocalContent hashes s with this package's single content digest
// algorithm and returns the lower-case hex digest. Only the returned
// digest ever leaves this function's caller's stack into a returned
// FileContentEvidence; s itself never does.
func hashLocalContent(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// resolveCompiledChecksum implements step 2: both sides must expose a
// non-empty checksum_value and agree on a checksum algorithm that is one
// of the documented checksum_value-compatible types
// (recognizedChecksumAlgorithms). See doc.go for why a mismatched or
// unrecognized algorithm is never treated as step 2 evidence.
func resolveCompiledChecksum(before, after map[string]model.Value) (model.FileContentEvidence, bool) {
	beforeValue, beforeOK := getStringParam(before, checksumValueParameter)
	afterValue, afterOK := getStringParam(after, checksumValueParameter)
	if !beforeOK || !afterOK || beforeValue == "" || afterValue == "" {
		return model.FileContentEvidence{}, false
	}

	beforeAlgo := getChecksumAlgorithm(before)
	afterAlgo := getChecksumAlgorithm(after)
	if beforeAlgo != afterAlgo || !recognizedChecksumAlgorithms[beforeAlgo] {
		return model.FileContentEvidence{}, false
	}

	return model.FileContentEvidence{
		State:          stateFromDigests(beforeValue, afterValue),
		EvidenceSource: model.FileContentEvidenceCompiledChecksum,
		Algorithm:      beforeAlgo,
		BeforeDigest:   beforeValue,
		AfterDigest:    afterValue,
	}, true
}

// getChecksumAlgorithm returns params' `checksum` parameter value
// lower-cased, or defaultChecksumAlgorithm when the parameter is absent,
// per Puppet's documented default ("The default checksum type is
// sha256").
func getChecksumAlgorithm(params map[string]model.Value) string {
	if v, ok := getStringParam(params, checksumParameter); ok && v != "" {
		return lowerASCII(v)
	}
	return defaultChecksumAlgorithm
}

// isDirectoryOrRecursive reports whether params describes a File
// resource whose `source` (if any) names a directory tree rather than a
// single file: `ensure => directory`, or any `recurse` value other than
// an explicit false. Both spellings matter independently -- a recursive
// File may leave `ensure` unset, and PIACE sees each side separately, so
// one side alone is enough to make byte comparison inapplicable.
//
// `recurse` arrives from a catalog as a JSON boolean, so the bool case is
// the common one; the string and numeric cases cover Puppet's documented
// "remote"/"inf" spellings and the deprecated numeric recursion depth.
func isDirectoryOrRecursive(params map[string]model.Value) bool {
	if ensure, ok := getStringParam(params, ensureParameter); ok &&
		lowerASCII(ensure) == directoryEnsureValue {
		return true
	}
	if params == nil {
		return false
	}
	switch v := params[recurseParameter].(type) {
	case bool:
		return v
	case string:
		return recursiveRecurseValues[lowerASCII(v)]
	case model.Number:
		// A numeric recursion depth of 0 disables recursion, exactly as
		// `recurse => false` does; any other depth enables it.
		return string(v) != "0"
	default:
		return false
	}
}

// resolveNonByteComparable resolves a File resource whose content-bearing
// parameters changed but whose `source` names a directory tree, so no
// byte-level comparison is possible or meaningful. No retrieval is
// attempted; see doc.go's "Sources that are not byte-comparable" section
// for why this is reported as a reference change at warning severity
// rather than as a failed content verification.
func resolveNonByteComparable(certname string, identity model.ResourceIdentity, before, after map[string]model.Value) (model.FileContentEvidence, *model.Diagnostic) {
	beforeRef, beforeHas := getReferenceParam(before, sourceParameter)
	afterRef, afterHas := getReferenceParam(after, sourceParameter)

	if (beforeHas || afterHas) && (beforeRef != afterRef || beforeHas != afterHas) {
		diag := verifyContentDiagnostic(model.SeverityWarning, certname, identity,
			"content source reference changed on a directory or recursive File; byte-level content comparison does not apply to a directory source")
		return model.FileContentEvidence{State: model.FileContentReferenceChanged}, &diag
	}

	// The reference did not visibly change, so some other content-bearing
	// parameter did (a checksum or checksum_value the compiler inlined on
	// only one side, say). There is no reference-level fact to report and
	// no bytes to compare, which is exactly content_indeterminate: this
	// case keeps error severity so it cannot collapse into a clean run.
	diag := verifyContentDiagnostic(model.SeverityError, certname, identity,
		"content evidence changed on a directory or recursive File; byte-level content comparison does not apply to a directory source")
	return model.FileContentEvidence{State: model.FileContentIndeterminate}, &diag
}

// sideResolution is the outcome of resolving one side (before or after)
// of a File resource's content-bearing parameters toward a comparable
// digest, which is step 3.
type sideResolution struct {
	digest       DigestEvidence
	reference    string
	hasReference bool
	hasLiteral   bool
	err          error
}

// resolveSide resolves one side's digest per step 3: literal content is
// hashed locally; a `source` reference is retrieved through retriever
// when supplied. See doc.go for why a nil retriever and an attempted-
// but-failed retrieval are tracked as distinct outcomes.
func resolveSide(ctx context.Context, params map[string]model.Value, rc RetrievalContext, retriever ContentRetriever) sideResolution {
	if content, ok := getStringParam(params, contentParameter); ok {
		return sideResolution{
			digest:     DigestEvidence{Algorithm: contentDigestAlgorithm, Digest: hashLocalContent(content)},
			hasLiteral: true,
		}
	}
	ref, ok := getReferenceParam(params, sourceParameter)
	if !ok {
		return sideResolution{err: errNoContentOrSource}
	}
	if retriever == nil {
		return sideResolution{reference: ref, hasReference: true, err: errRetrieverUnavailable}
	}
	digest, err := retriever.Digest(ctx, ref, rc)
	if err != nil {
		return sideResolution{reference: ref, hasReference: true, err: err}
	}
	return sideResolution{digest: digest, reference: ref, hasReference: true}
}

// classifyUnresolvedState implements the reference_changed vs.
// content_indeterminate rule documented in doc.go: reference_changed
// applies only when retrieval was never attempted at all (retriever is
// nil) and both sides visibly carry a differing `source` reference;
// every other unresolved case is content_indeterminate.
func classifyUnresolvedState(retriever ContentRetriever, before, after sideResolution) (model.FileContentState, string) {
	if retriever == nil && before.hasReference && after.hasReference && before.reference != after.reference {
		return model.FileContentReferenceChanged,
			"content source reference changed but no content retriever is configured to compare bytes"
	}
	if retriever == nil && (before.hasReference || after.hasReference) {
		return model.FileContentIndeterminate,
			"content comparison requires retrieving referenced content but no content retriever is configured"
	}
	return model.FileContentIndeterminate,
		"content retrieval or comparison could not establish comparable bytes for this resource"
}

// verifyContentDiagnostic builds a model.OperationVerifyContent
// diagnostic identifying only the target, resource identity, and a safe
// reason string -- never a parameter value, source reference, or file
// content. severity is the caller's: a retrieval that was attempted and
// failed is an error, while a comparison this package declines to attempt
// because bytes are not the right evidence for the resource at all is a
// warning (see doc.go).
func verifyContentDiagnostic(severity model.DiagnosticSeverity, certname string, identity model.ResourceIdentity, reason string) model.Diagnostic {
	return model.Diagnostic{
		Severity:  severity,
		Operation: model.OperationVerifyContent,
		Certname:  certname,
		Message:   identity.String() + ": " + reason,
	}
}

// getStringParam returns params[key] only when it is present and holds
// exactly a string value within the model.Value domain (never a bool,
// Number, nil, array, or object).
func getStringParam(params map[string]model.Value, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	v, ok := params[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// getReferenceParam returns a single string reference for params[key],
// accepting either a bare string value or a non-empty []model.Value of
// strings (Puppet's File `source` attribute accepts an array of
// candidate sources; per Puppet's documented behavior "Puppet will use
// the first source that exists", this package takes the first element as
// the comparison reference, deferring to the same well-documented
// precedence rule rather than inventing its own).
func getReferenceParam(params map[string]model.Value, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	v, ok := params[key]
	if !ok {
		return "", false
	}
	switch val := v.(type) {
	case string:
		return val, true
	case []model.Value:
		if len(val) == 0 {
			return "", false
		}
		s, ok := val[0].(string)
		return s, ok
	default:
		return "", false
	}
}

// lowerASCII lower-cases s without importing strings solely for this one
// call site; checksum algorithm names are always plain ASCII identifiers.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
