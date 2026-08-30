package resolve

import "github.com/example42/piace/internal/config"

// Overrides carries the invocation-scoped configuration values a command
// supplies on its own command line in place of what the target file says.
// The zero value overrides nothing, so a command that offers no override
// passes one and every code path stays the same shape.
//
// An override is applied to the decoded target file *before* resolution,
// never to the resolved targets afterwards. That is what makes it
// indistinguishable from a file that had said so in the first place:
// every rule in design.md section 3.2 is validated against the effective
// value, and the redacted provenance a report carries records the
// effective value without inventing a second notion of where it came
// from. It also means a target file may legitimately omit a field the
// invocation supplies, which is the point: see CandidateEnvironment.
type Overrides struct {
	// CandidateEnvironment replaces `candidate.environment` for every
	// target, both the defaults block and every per-target override.
	//
	// The candidate environment is the one CI deployed for the change
	// under test, so it is a per-pipeline value in a file that is
	// otherwise reviewable policy. Without this, a pipeline has to
	// rewrite its own committed target file between checkout and run,
	// and what a reviewer approved is not quite what ran. A flag that
	// lost to a per-target `candidate:` block would reintroduce exactly
	// that surprise, so this one wins over both.
	//
	// Empty means "no override": the target file's own value is used,
	// and the usual "candidate.environment is required" rule applies.
	CandidateEnvironment string
}

// apply returns tf with ov's values substituted in. It copies whatever it
// modifies rather than writing through tf's slice and pointers, so the
// decoded document a caller still holds is not quietly rewritten.
func (ov Overrides) apply(tf config.TargetFile) config.TargetFile {
	if ov.CandidateEnvironment == "" {
		return tf
	}

	tf.Defaults.Candidate.Environment = ov.CandidateEnvironment

	// A per-target `candidate:` block replaces the defaults block
	// wholesale (see mergeScalars), so overriding the defaults alone
	// would leave every target that declares one on its file value —
	// or, worse, on no value at all.
	targets := make([]config.Target, len(tf.Targets))
	copy(targets, tf.Targets)
	for i := range targets {
		if targets[i].Candidate == nil {
			continue
		}
		candidate := *targets[i].Candidate
		candidate.Environment = ov.CandidateEnvironment
		targets[i].Candidate = &candidate
	}
	tf.Targets = targets

	return tf
}
