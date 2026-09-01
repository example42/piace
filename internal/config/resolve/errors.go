package resolve

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidationError accumulates every configuration problem found while
// decoding, resolving, and validating a target or services file. Invalid
// configuration is one operational diagnostic and prevents every service
// call, so Load and ResolveTargets never fail fast on the first problem:
// they accumulate everything they can find into a single
// ValidationError, and a user sees every misconfiguration from one run
// rather than fixing them one at a time.
//
// ValidationError implements error; callers that need the underlying list
// (e.g. to build a single model.Diagnostic message, or to count problems in
// a test) can use Problems().
type ValidationError struct {
	problems []string
}

// Error joins every accumulated problem, one per line, prefixed for
// readability. It never includes secret material: problems are built from
// field names, certnames, and structural descriptions only, never file
// contents or credential values.
func (e *ValidationError) Error() string {
	if e == nil || len(e.problems) == 0 {
		return "invalid configuration"
	}
	if len(e.problems) == 1 {
		return "invalid configuration: " + e.problems[0]
	}
	var b strings.Builder
	b.WriteString("invalid configuration (")
	b.WriteString(strconv.Itoa(len(e.problems)))
	b.WriteString(" problems):")
	for _, p := range e.problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

// Problems returns every accumulated problem message in the order they
// were found.
func (e *ValidationError) Problems() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.problems...)
}

// add records one problem. Callers use errorCollector rather than a bare
// *ValidationError so there is no nil-receiver pitfall.
func (e *ValidationError) add(problem string) {
	e.problems = append(e.problems, problem)
}

// errorCollector accumulates ValidationError problems across an entire
// decode+resolve+validate pass and yields either nil (no problems) or a
// populated *ValidationError.
type errorCollector struct {
	err ValidationError
}

func (c *errorCollector) addf(format string, args ...any) {
	c.err.add(fmt.Sprintf(format, args...))
}

// result returns nil if no problems were collected, so callers can return
// `err` directly as an `error` without an explicit len check leaking a
// non-nil-typed-nil-interface bug.
func (c *errorCollector) result() error {
	if len(c.err.problems) == 0 {
		return nil
	}
	out := c.err
	return &out
}

func (c *errorCollector) hasErrors() bool {
	return len(c.err.problems) > 0
}
