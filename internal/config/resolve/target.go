package resolve

import (
	"time"

	"github.com/example42/piace/internal/config"
)

// ResolveTargets resolves and validates every target in tf against its
// global defaults, for the command that is going to read them.
// targetFileDir is the directory containing the target file, used to
// resolve relative facts.file/baseline.file values.
//
// cmd governs which fields must be *present* (see Command.needs); every
// value that is present is validated the same way whatever the command.
//
// It validates config.TargetFileVersion (version: 1) and accumulates
// every problem found across every target into a single error.
// ("Invalid configuration is one operational diagnostic"). When err is
// non-nil, targets is nil: a caller must not act on partial results from
// an invalid target file.
func ResolveTargets(tf config.TargetFile, targetFileDir string, cmd Command) (targets []Target, err error) {
	var c errorCollector

	if tf.Version != config.TargetFileVersion {
		c.addf("target file: unsupported version %d, expected %d", tf.Version, config.TargetFileVersion)
	}

	if len(tf.Targets) == 0 {
		c.addf("target file: no targets configured")
	}

	seenCertnames := make(map[string]bool, len(tf.Targets))
	resolved := make([]Target, 0, len(tf.Targets))

	for i, t := range tf.Targets {
		if err := validateCertname(t.Certname); err != nil {
			c.addf("targets[%d]: %s", i, err)
			continue
		}
		if seenCertnames[t.Certname] {
			c.addf("targets[%d]: duplicate certname %q", i, t.Certname)
			continue
		}
		seenCertnames[t.Certname] = true

		rt, ok := resolveOneTarget(tf.Defaults, t, targetFileDir, cmd.needs(), &c)
		if ok {
			resolved = append(resolved, rt)
		}
	}

	if c.hasErrors() {
		return nil, c.result()
	}
	return resolved, nil
}

// resolveOneTarget resolves and fully validates a single target. It
// reports every problem it finds via c and returns ok=false if the target
// could not be fully resolved (callers must not use a partially resolved
// Target). t.Certname has already been validated and checked for
// uniqueness by the caller.
func resolveOneTarget(defaults config.Defaults, t config.Target, targetFileDir string, needs targetNeeds, c *errorCollector) (Target, bool) {
	scalars := mergeScalars(defaults, t)
	ok := true

	// --- candidate ---
	if needs.candidateEnvironment && scalars.candidate.Environment == "" {
		c.addf("target %q: candidate.environment is required", t.Certname)
		ok = false
	}
	switch {
	case scalars.candidate.CatalogAPI == config.CatalogAPIv3 || scalars.candidate.CatalogAPI == config.CatalogAPIv4:
	case scalars.candidate.CatalogAPI == "" && !needs.candidateAPI:
	default:
		c.addf("target %q: candidate.catalog_api must be %q or %q, got %q",
			t.Certname, config.CatalogAPIv3, config.CatalogAPIv4, scalars.candidate.CatalogAPI)
		ok = false
	}
	allowV3Fallback := false
	if scalars.candidate.AllowV3Fallback != nil {
		allowV3Fallback = *scalars.candidate.AllowV3Fallback
	}
	if allowV3Fallback && scalars.candidate.CatalogAPI == config.CatalogAPIv3 {
		c.addf("target %q: allow_v3_fallback is valid only with catalog_api: v4, got catalog_api: v3",
			t.Certname)
		ok = false
	}
	trustedFactsCompilerLookup := false
	if scalars.candidate.TrustedFactsCompilerLookup != nil {
		trustedFactsCompilerLookup = *scalars.candidate.TrustedFactsCompilerLookup
	}
	if trustedFactsCompilerLookup && scalars.candidate.CatalogAPI == config.CatalogAPIv3 {
		c.addf("target %q: trusted_facts_compiler_lookup is valid only with catalog_api: v4, got catalog_api: v3",
			t.Certname)
		ok = false
	}

	// --- facts ---
	switch scalars.facts.Source {
	case config.FactSourcePuppetDB:
		if scalars.facts.File != "" {
			c.addf("target %q: facts.file must not be set when facts.source is %q",
				t.Certname, config.FactSourcePuppetDB)
			ok = false
		}
	case config.FactSourceFile:
		if scalars.facts.File == "" {
			c.addf("target %q: facts.file is required when facts.source is %q",
				t.Certname, config.FactSourceFile)
			ok = false
		}
	default:
		c.addf("target %q: facts.source must be %q or %q, got %q",
			t.Certname, config.FactSourcePuppetDB, config.FactSourceFile, scalars.facts.Source)
		ok = false
	}

	// --- baseline ---
	if needs.baselineEnvironment && scalars.baseline.Environment == "" {
		c.addf("target %q: baseline.environment is required", t.Certname)
		ok = false
	}
	switch scalars.baseline.Source {
	case "":
		if needs.baselineSource {
			c.addf("target %q: baseline.source must be %q or %q, got %q",
				t.Certname, config.BaselineSourcePuppetDB, config.BaselineSourceFile, scalars.baseline.Source)
			ok = false
		}
	case config.BaselineSourcePuppetDB:
		if scalars.baseline.File != "" {
			c.addf("target %q: baseline.file must not be set when baseline.source is %q",
				t.Certname, config.BaselineSourcePuppetDB)
			ok = false
		}
	case config.BaselineSourceFile:
		if scalars.baseline.File == "" {
			c.addf("target %q: baseline.file is required when baseline.source is %q",
				t.Certname, config.BaselineSourceFile)
			ok = false
		}
	default:
		c.addf("target %q: baseline.source must be %q or %q, got %q",
			t.Certname, config.BaselineSourcePuppetDB, config.BaselineSourceFile, scalars.baseline.Source)
		ok = false
	}

	// Resolve template file paths only once source/file presence above is
	// known to be consistent; a missing-but-required file was already
	// reported above and there is nothing to resolve.
	var factsFile, baselineFile string
	if scalars.facts.Source == config.FactSourceFile && scalars.facts.File != "" {
		f, err := resolveFilePath(scalars.facts.File, targetFileDir, t.Certname)
		if err != nil {
			c.addf("target %q: facts.file: %s", t.Certname, err)
			ok = false
		}
		factsFile = f
	}
	if scalars.baseline.Source == config.BaselineSourceFile && scalars.baseline.File != "" {
		f, err := resolveFilePath(scalars.baseline.File, targetFileDir, t.Certname)
		if err != nil {
			c.addf("target %q: baseline.file: %s", t.Certname, err)
			ok = false
		}
		baselineFile = f
	}

	// --- exclude / redact (append-only merge, then per-rule validation) ---
	exclude := mergeExclusions(defaults.Exclude, t.Exclude)
	for _, rule := range exclude {
		if rule.Type == "" {
			c.addf("target %q: exclude rule has an empty type", t.Certname)
			ok = false
		}
		if err := validateGlobSyntax(rule.Title); err != nil {
			c.addf("target %q: exclude rule %+v: %s", t.Certname, rule, err)
			ok = false
		}
	}

	redact := mergeRedactions(defaults.Redact, t.Redact)
	for _, sel := range redact {
		if sel.Type == "" || sel.Parameter == "" {
			c.addf("target %q: redact selector requires non-empty type and parameter, got %+v",
				t.Certname, sel)
			ok = false
		}
	}

	// --- impact estimate ---
	impactEnabled := false
	if scalars.impactEstimate.Enabled != nil {
		impactEnabled = *scalars.impactEstimate.Enabled
	}
	var impactTimeout time.Duration
	var impactLimit int
	if impactEnabled {
		if scalars.impactEstimate.Timeout == "" {
			c.addf("target %q: impact_estimate.timeout is required when impact_estimate.enabled is true",
				t.Certname)
			ok = false
		} else {
			d, err := time.ParseDuration(scalars.impactEstimate.Timeout)
			if err != nil {
				c.addf("target %q: impact_estimate.timeout %q is not a valid duration: %s",
					t.Certname, scalars.impactEstimate.Timeout, err)
				ok = false
			} else if d <= 0 {
				c.addf("target %q: impact_estimate.timeout must be positive, got %q",
					t.Certname, scalars.impactEstimate.Timeout)
				ok = false
			} else {
				impactTimeout = d
			}
		}
		if scalars.impactEstimate.ResultLimit == nil {
			c.addf("target %q: impact_estimate.result_limit is required when impact_estimate.enabled is true",
				t.Certname)
			ok = false
		} else if *scalars.impactEstimate.ResultLimit <= 0 {
			c.addf("target %q: impact_estimate.result_limit must be a positive integer, got %d",
				t.Certname, *scalars.impactEstimate.ResultLimit)
			ok = false
		} else {
			impactLimit = *scalars.impactEstimate.ResultLimit
		}
	}

	if !ok {
		return Target{}, false
	}

	return Target{
		Certname: t.Certname,
		Candidate: Candidate{
			Environment:                scalars.candidate.Environment,
			CatalogAPI:                 scalars.candidate.CatalogAPI,
			AllowV3Fallback:            allowV3Fallback,
			TrustedFactsCompilerLookup: trustedFactsCompilerLookup,
		},
		Facts: Facts{
			Source: scalars.facts.Source,
			File:   factsFile,
		},
		Baseline: Baseline{
			Source:      scalars.baseline.Source,
			Environment: scalars.baseline.Environment,
			File:        baselineFile,
		},
		Exclude: exclude,
		Redact:  redact,
		ImpactEstimate: ImpactEstimate{
			Enabled:     impactEnabled,
			Timeout:     impactTimeout,
			ResultLimit: impactLimit,
		},
		FailOnDiff: scalars.failOnDiff,
	}, true
}
