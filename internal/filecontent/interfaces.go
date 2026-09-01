package filecontent

import (
	"context"

	"github.com/example42/piace/internal/model"
)

// DigestEvidence is the redaction-safe result of resolving one side's
// referenced content into a cryptographic digest: an algorithm name and
// a hex-encoded digest string, never the underlying bytes. It is what
// ContentResolver.Digest returns.
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
// The interface is named as "ContentResolver.Digest(reference, context)
// -> DigestEvidence". This package's ContentRetriever.Digest takes both
// a context.Context, for cancellation and deadline propagation, matching
// every other adapter in this codebase (see internal/compiler and
// internal/puppetdb), and a RetrievalContext, which is the environment,
// certname and identity metadata a retrieval implementation needs and a
// bare reference string does not carry.
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
// establish comparable bytes for this reference: network, timeout,
// not-found, or an unsupported source scheme. ResolveFileContentEvidence
// maps that into the content_indeterminate state plus a
// model.OperationVerifyContent diagnostic. The error's Error() text must
// itself be safe to place in a diagnostic message, carrying no
// authorization headers, no PEM or key material, and no file content.
// See resolver.go's CompilerContentResolver, which builds every error
// through transport.SafeMessage and transport.Diagnostic exactly as the
// PuppetDB and compiler adapters do.
type ContentRetriever interface {
	Digest(ctx context.Context, reference string, rc RetrievalContext) (DigestEvidence, error)
}
