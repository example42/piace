package resolve

import (
	"github.com/example42/piace/internal/model"
)

// Provenance builds the redacted model.ConfigProvenance projection for
// one resolved target: "Resolved configuration provenance includes
// source choices, paths, API, policy values, and matching rules, but
// never endpoint credentials or private key paths."
//
// facts.file and baseline.file resolved paths are included: they are
// local snapshot paths, not service credentials, and what provenance
// excludes is endpoint credentials and private key paths, meaning
// config.ServiceEndpoint's CABundle, ClientCert and PrivateKey. This
// function never reads from Services or Endpoint at all, so a client
// certificate or private key path can never reach a TargetResult's
// provenance through here.
func Provenance(t Target) *model.ConfigProvenance {
	exclude := make([]model.ExclusionRuleRef, 0, len(t.Exclude))
	for _, r := range t.Exclude {
		exclude = append(exclude, model.ExclusionRuleRef{Type: r.Type, Title: r.Title})
	}

	redact := make([]model.RedactionSelectorRef, 0, len(t.Redact))
	for _, r := range t.Redact {
		redact = append(redact, model.RedactionSelectorRef{Type: r.Type, Parameter: r.Parameter})
	}

	return &model.ConfigProvenance{
		Candidate: map[string]any{
			"environment":                   t.Candidate.Environment,
			"catalog_api":                   string(t.Candidate.CatalogAPI),
			"allow_v3_fallback":             t.Candidate.AllowV3Fallback,
			"trusted_facts_compiler_lookup": t.Candidate.TrustedFactsCompilerLookup,
		},
		Facts: map[string]any{
			"source": string(t.Facts.Source),
			"file":   t.Facts.File,
		},
		Baseline: map[string]any{
			"source":      string(t.Baseline.Source),
			"environment": t.Baseline.Environment,
			"file":        t.Baseline.File,
		},
		Exclude: exclude,
		Redact:  redact,
		ImpactEstimate: map[string]any{
			"enabled":      t.ImpactEstimate.Enabled,
			"timeout":      t.ImpactEstimate.Timeout.String(),
			"result_limit": t.ImpactEstimate.ResultLimit,
		},
		FailOnDiff: t.FailOnDiff,
	}
}
