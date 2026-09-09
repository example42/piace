// Package limits collects the input and output budgets PIACE enforces,
// so the whole picture can be read in one place rather than reconstructed
// from constants scattered across the packages that apply them.
//
// # Why they exist
//
// Every one of these bounds an amount of work an input can ask PIACE to
// do. A response-byte limit alone does not: eight bytes of JSON
// (`1e-10000`) expand into ten thousand bytes of canonical output, a
// catalog with a thousand differing resources produces an inference
// request nobody budgeted for, and a `git log` in a repository with a
// hundred thousand commits fills memory before anything checks. A limit
// here is not a guess at what a real deployment contains; it is the
// point past which PIACE stops and says so, instead of continuing until
// something else stops it.
//
// # What they are not
//
// They are not a security boundary against a hostile compiler. A service
// PIACE is configured to trust with catalog compilation can do worse
// than send a large number. They exist so that a run against a
// misbehaving service, a mistyped configuration, or a repository nobody
// expected fails with a diagnostic naming the limit, at a moment the
// operator can act on.
//
// Each constant names the package that enforces it. A package keeps its
// own exported name where one is already part of its contract, defined
// in terms of the value here.
package limits

// Bytes read from a local file or stream, before anything parses them.
const (
	// ResultDocument bounds a stored result document read by `explain`,
	// from a file or from stdin (cmd/piace, internal/report).
	ResultDocument = 64 * 1024 * 1024
	// Snapshot bounds a fact or catalog snapshot envelope
	// (internal/snapshot).
	Snapshot = 64 * 1024 * 1024
	// Config bounds a target or services file (internal/config/resolve).
	// Neither lists more than nodes and endpoints.
	Config = 4 * 1024 * 1024
	// PolicyNotes bounds the policy-notes file an inference configuration
	// may name (internal/config/resolve). What is actually sent is
	// smaller still and separately bounded by assess.MaxPolicyNotesBytes;
	// this stops a file of any size being read in order to take the first
	// few thousand bytes of it.
	PolicyNotes = 1 * 1024 * 1024
	// ChangeContext bounds a change-context file read by `explain`
	// (cmd/piace), and the output collected from one git invocation by
	// `change-context`.
	ChangeContext = 1 * 1024 * 1024
)

// Numeric shape, applied to a JSON numeric token before it is parsed
// into an arbitrary-precision value (internal/snapshot).
//
// PIACE compares configuration values. A port, a timeout, a version, a
// byte count: none of them needs a thousand significant digits or an
// exponent past a few hundred, and a token that does is either a
// mistake or an attempt to make canonicalization expensive. The bound is
// on the token, checked before parsing, because the expansion happens
// during parsing: `1e-10000` costs nothing to read and produces a
// ten-thousand-digit exact decimal.
const (
	// NumberDigits bounds the significant digits of a numeric token.
	NumberDigits = 1024
	// NumberExponent bounds the magnitude of a numeric token's decimal
	// exponent, and with it the length of the exact decimal expansion.
	//
	// It cannot go much below 324: that is the decimal exponent of the
	// smallest denormal float64 (5e-324), so a fact that legitimately
	// arrived as a float and was formatted back out would be refused by a
	// tighter bound. 1024 leaves room above that while keeping the worst
	// allowed token under a millisecond to canonicalize.
	NumberExponent = 1024
	// JSONNestingDepth bounds how deeply a canonically encoded value may
	// nest. Recursion over an attacker-shaped document is otherwise
	// bounded only by the stack.
	JSONNestingDepth = 200
)

// InferenceRequest bounds the total encoded size of one inference
// request body (internal/assess), across values, targets, groups, impact
// entries, change context and policy notes together. Group and node
// counts are budgeted separately and first; this is the backstop for the
// case those counts do not predict, a handful of groups carrying very
// large values.
//
// It applies to the retry request too: a retry is a second disclosure of
// the same size, not a free one.
const InferenceRequest = 1 * 1024 * 1024
