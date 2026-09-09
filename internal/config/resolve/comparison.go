package resolve

import "github.com/example42/piace/internal/config"

// ValidateComparisonTargets applies only to compare. Catalog capture can
// intentionally use v3, and reports the persistence effects of that request.
func ValidateComparisonTargets(targets []Target) error {
	var c errorCollector
	for _, target := range targets {
		if target.Baseline.Source == config.BaselineSourcePuppetDB &&
			(target.Candidate.CatalogAPI == config.CatalogAPIv3 || target.Candidate.AllowV3Fallback) {
			c.addf("target %q: baseline.source must be file when catalog_api is v3 or allow_v3_fallback is enabled; v3 can persist candidate facts and catalogs", target.Certname)
		}
	}
	if c.hasErrors() {
		return c.result()
	}
	return nil
}
