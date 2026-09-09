package capture

import (
	"context"

	"github.com/example42/piace/internal/filecontent"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/normalize"
	"github.com/example42/piace/internal/puppetdb"
)

// captureContent retains digests observed while the environment is live.
//
// A failed observation prevents publication; evidence that was never
// obtainable does not. The two are separated by
// filecontent.ResolveFileContentEvidence, which owns both the safe
// diagnostic text and the severity: a directory, a recursive source, a
// bare local path the compiler's file server does not serve, and a
// resource naming neither content nor source are warnings and travel
// into the snapshot's warning list, while an invalid checksum or a
// retrieval that failed is an error and stops the capture.
//
// Capturing a real catalog depends on that distinction. The first live
// capture attempted against a deployed OpenVox 8.15.2 installation, on
// 2026-09-09, wrote no snapshot and exited 30, because one File in the
// catalog had `source => /etc/puppetlabs/puppet/ssl/certs/ca.pem`. A
// bare local path is read from the agent's own filesystem; it is not a
// retrieval this tool can make, let alone one that failed.
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
			// filecontent owns the safe diagnostic text and the
			// severity that goes with each resolution error; it never
			// renders resolver errors itself.
			diagnostics = append(diagnostics, filecontent.EvidenceDiagnostic(cat.Certname, resource.Identity, err))
			continue
		}
		if source == model.FileContentEvidenceCompilerRetrieval {
			cat.CapturedContent[resource.Identity.Title] = model.ContentDigest{Algorithm: digest.Algorithm, Digest: digest.Digest}
		}
	}
	return diagnostics
}
