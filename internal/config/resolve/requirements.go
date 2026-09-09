package resolve

import "github.com/example42/piace/internal/config"

// Command identifies the PIACE command whose configuration is being
// resolved. One target file and one services file serve every command,
// but the commands do not read the same fields, and requiring a field a
// command never reads turns configuration into ceremony: `capture facts`
// retrieves a factset and writes it to a file, and used to refuse to run
// until the operator had also supplied a candidate environment, a
// catalog API version, and a baseline the command never looks at.
//
// `explain` is absent from this list on purpose. It reads only the
// services file's inference section, through LoadInferenceFile, and
// never a target file at all.
type Command string

const (
	CommandCompare        Command = "compare"
	CommandCaptureFacts   Command = "capture facts"
	CommandCaptureCatalog Command = "capture catalog"
)

// ServiceSet names the service endpoints a command needs. A service not
// in the set is neither required nor validated, and the corresponding
// Services field stays zero: a services file that names no `puppetdb:`
// section at all is valid for a run that never queries PuppetDB.
type ServiceSet struct {
	Compiler bool
	PuppetDB bool
}

// targetNeeds says which per-target values must be *present* for a
// command. It never governs whether a present value is *valid*: an
// invalid environment name, catalog API, source kind, glob, path, or
// duration is rejected for every command, because a target file that
// contains nonsense is wrong whoever reads it. Only presence is
// command-scoped, which is what keeps resolve.Target's contract
// statable: everything present has been validated, and a field a command
// does not need may be empty.
type targetNeeds struct {
	candidateEnvironment bool
	candidateAPI         bool
	baselineSource       bool
	baselineEnvironment  bool
}

// needs returns the presence requirements of one command, derived from
// what its workflow actually reads:
//
//   - compare compiles a candidate for a named environment against a
//     baseline from a named environment, so it needs all four;
//   - capture facts retrieves a factset from PuppetDB and writes it to
//     the target's facts.file, and reads nothing else;
//   - capture catalog compiles for the environment named by
//     --environment (which replaces the target's candidate environment,
//     see capture.candidateEnvironmentView) using the target's fact
//     source, and writes to baseline.file. It needs the catalog API and
//     the baseline destination, but neither environment field.
func (c Command) needs() targetNeeds {
	switch c {
	case CommandCaptureFacts:
		return targetNeeds{}
	case CommandCaptureCatalog:
		return targetNeeds{candidateAPI: true, baselineSource: true}
	default:
		return targetNeeds{candidateEnvironment: true, candidateAPI: true, baselineSource: true, baselineEnvironment: true}
	}
}

// services reports which endpoints the command needs given tf, the
// decoded target file.
//
// The decision is made on the decoded document rather than on resolved
// targets so that an invalid target file still produces one accumulated
// diagnostic covering both files, the way it always has: facts.source,
// baseline.source and impact_estimate.enabled are readable whether or
// not the rest of the target validates. A target whose source values are
// invalid counts as needing PuppetDB, which costs nothing: that target
// file fails validation regardless, and the alternative would be to hide
// a real services problem behind a target problem.
func (c Command) services(tf config.TargetFile) ServiceSet {
	switch c {
	case CommandCaptureFacts:
		// Fact capture retrieves from PuppetDB whatever a target's own
		// comparison-time fact source is (see capture.puppetDBView), and
		// compiles nothing.
		return ServiceSet{PuppetDB: true}
	case CommandCaptureCatalog:
		return ServiceSet{Compiler: true, PuppetDB: anyTarget(tf, func(s resolvedScalars) bool {
			// Only targets with a file-backed baseline are captured; the
			// rest are skipped without any retrieval at all.
			return s.baseline.Source == config.BaselineSourceFile && s.facts.Source != config.FactSourceFile
		})}
	default:
		return ServiceSet{Compiler: true, PuppetDB: anyTarget(tf, func(s resolvedScalars) bool {
			return s.facts.Source != config.FactSourceFile ||
				s.baseline.Source != config.BaselineSourceFile ||
				(s.impactEstimate.Enabled != nil && *s.impactEstimate.Enabled)
		})}
	}
}

// anyTarget reports whether pred holds for any target in tf, evaluated
// against that target's fully merged defaults-plus-override view.
func anyTarget(tf config.TargetFile, pred func(resolvedScalars) bool) bool {
	for _, t := range tf.Targets {
		if pred(mergeScalars(tf.Defaults, t)) {
			return true
		}
	}
	return false
}
