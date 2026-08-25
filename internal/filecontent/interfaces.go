package filecontent

import (
	"context"

	"github.com/example42/piace/internal/model"
)

// DigestEvidence is the redaction-safe result of resolving one side's
// referenced content into a cryptographic digest: an algorithm name and
// a hex-encoded digest string, never the underlying bytes. It is the
// return shape design.md's Components and Interfaces section names for
// ContentResolver.Digest.
type DigestEvidence struct {
	Algorithm string
	Digest    string
}

// RetrievalContext carries the non-secret parameters a ContentRetriever
// needs to resolve one reference, beyond the reference string itself:
// the candidate environment the reference must be resolved within (the
// compiler's file-serving API is environment-scoped; see doc.go's
// documented endpoint-shape assumption), and, for diagnostics only, the
// target certname and resource identity. It never carries credentials or
// content.
//
// design.md's Components and Interfaces section names this interface's
// method as "ContentResolver.Digest(reference, context) -> DigestEvidence".
// This package's ContentRetriever.Digest takes both a context.Context
// (for cancellation/deadline propagation, matching every other adapter
// in this codebase — see internal/compiler and internal/puppetdb) and a
// RetrievalContext (the "context" design.md's shorthand refers to:
// environment/certname/identity metadata a retrieval implementation
// needs but a bare reference string does not carry).
type RetrievalContext struct {
	Certname    string
	Identity    model.ResourceIdentity
	Environment string
}

// ContentRetriever resolves one side of a File resource's `source`
// reference into a DigestEvidence without ever exposing the retrieved
// bytes to its caller: an implementation must hash the retrieved content
// locally and return only the resulting digest. ResolveFileContentEvidence
// never inspects or logs anything a ContentRetriever returns beyond
// Algorithm/Digest, and never passes retrieved bytes anywhere else.
//
// A non-nil error return means retrieval or comparison could not
// establish comparable bytes for this reference (network/timeout/
// not-found/unsupported source scheme); ResolveFileContentEvidence maps
// that into design.md section 7.2 step 4's content_indeterminate state
// plus a model.OperationVerifyContent diagnostic. The error's Error()
// text must itself be safe to place in a diagnostic message (no
// authorization headers, no PEM/key material, no file content) — see
// resolver.go's CompilerContentResolver, which builds every error
// through transport.SafeMessage/transport.Diagnostic exactly as tasks
// 4's PuppetDB adapter and task 6's compiler adapter do.
type ContentRetriever interface {
	Digest(ctx context.Context, reference string, rc RetrievalContext) (DigestEvidence, error)
}
