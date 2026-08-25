package puppetdb

import (
	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
)

// SelectFactSource returns fileSource when target.Facts.Source resolves
// to config.FactSourceFile, and puppetDB otherwise. This is the small
// dispatch design.md's Architecture diagram implies with its single
// "fact-source adapter (PuppetDB or envelope)" box: task 4 and task 5
// each implement one backend behind the shared FactSource interface, and
// a caller that must honor "the target's configured fact source" (e.g.
// requirements.md 2.1, 11.3) picks between them with this function rather
// than duplicating the switch at each call site.
func SelectFactSource(target resolve.Target, puppetDB, fileSource FactSource) FactSource {
	if target.Facts.Source == config.FactSourceFile {
		return fileSource
	}
	return puppetDB
}

// SelectCatalogSource is SelectFactSource's counterpart for a target's
// baseline catalog source.
func SelectCatalogSource(target resolve.Target, puppetDB, fileSource CatalogSource) CatalogSource {
	if target.Baseline.Source == config.BaselineSourceFile {
		return fileSource
	}
	return puppetDB
}
