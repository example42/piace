package filecontent

import (
	"context"
	"github.com/example42/piace/internal/model"
)

// DigestEvidence contains a full content digest, never the retrieved bytes.
// The evidence resolver validates it before it enters the published model.
type DigestEvidence struct {
	Algorithm string
	Digest    string
}

// RetrievalContext belongs to the side being resolved. Historical evidence
// cannot reach this interface through ResolveSide.
type RetrievalContext struct {
	Certname    string
	Identity    model.ResourceIdentity
	Environment string
}

// ContentRetriever resolves one source. Only ErrSourceNotFound permits source
// fallback; all other failures stop selection. Implementations hash locally.
type ContentRetriever interface {
	Digest(ctx context.Context, reference string, rc RetrievalContext) (DigestEvidence, error)
}
