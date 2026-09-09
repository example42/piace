package resolve

import (
	"github.com/example42/piace/internal/config"
	"testing"
)

func TestComparisonV3PolicyIsSeparateFromCaptureResolution(t *testing.T) {
	for _, api := range []config.CatalogAPI{config.CatalogAPIv3, config.CatalogAPIv4} {
		for _, source := range []config.BaselineSourceKind{config.BaselineSourceFile, config.BaselineSourcePuppetDB} {
			d := validDefaults()
			d.Candidate.CatalogAPI = api
			d.Candidate.AllowV3Fallback = boolPtr(api == config.CatalogAPIv4)
			d.Baseline.Source = source
			if source == config.BaselineSourcePuppetDB {
				d.Baseline.File = ""
			}
			targets, err := ResolveTargets(config.TargetFile{Version: 1, Defaults: d, Targets: []config.Target{{Certname: "node"}}}, t.TempDir(), CommandCompare)
			if err != nil {
				t.Fatalf("capture config refused: %v", err)
			}
			err = ValidateComparisonTargets(targets)
			if (err != nil) != (source == config.BaselineSourcePuppetDB) {
				t.Fatalf("api=%s source=%s: %v", api, source, err)
			}
		}
	}
}
