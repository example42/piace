package capture

import (
	"context"
	"errors"

	"github.com/example42/piace/internal/filecontent"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/normalize"
	"github.com/example42/piace/internal/puppetdb"
)

// captureContent retains digests observed while the environment is live.
// Failed observations prevent publication; unsupported trees carry warnings.
func (w *Workflow) captureContent(ctx context.Context, cat *puppetdb.Catalog) []model.Diagnostic {
	normalized, diag := normalize.Catalog(*cat)
	if diag != nil {
		return []model.Diagnostic{*diag}
	}
	cat.CapturedContent = make(map[string]model.ContentDigest)
	var diagnostics []model.Diagnostic
	for _, resource := range normalized.Resources {
		if !filecontent.NeedsEvidence(resource) {
			continue
		}
		side := filecontent.Side{Resource: resource, Context: model.ContentContext{Source: "compiler", Environment: cat.Environment, CatalogIdentity: cat.CodeID}}
		digest, source, err := filecontent.ResolveSide(ctx, cat.Certname, side, w.ContentRetriever)
		if err != nil {
			// ResolveFileContentEvidence owns safe diagnostic text and the
			// unsupported directory policy; it never renders resolver errors.
			_, diag := filecontent.ResolveFileContentEvidence(ctx, cat.Certname, resource.Identity, side, side, nil)
			if diag != nil {
				if errors.Is(err, filecontent.ErrNonByteComparable) {
					diag.Severity = model.SeverityWarning
				}
				diagnostics = append(diagnostics, *diag)
			}
			continue
		}
		if source == model.FileContentEvidenceCompilerRetrieval {
			cat.CapturedContent[resource.Identity.Title] = model.ContentDigest{Algorithm: digest.Algorithm, Digest: digest.Digest}
		}
	}
	return diagnostics
}
