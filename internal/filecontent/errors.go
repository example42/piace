package filecontent

import "errors"

// errNoContentOrSource is returned internally by resolveSide when a File
// resource's parameter map exposes neither a literal `content` string
// nor a `source` reference this package recognizes; it is never itself
// placed in a diagnostic message (classifyUnresolvedState builds its own
// safe reason text).
var errNoContentOrSource = errors.New("filecontent: no comparable content or source parameter")

// errRetrieverUnavailable is returned internally by resolveSide when a
// side has a `source` reference but no ContentRetriever was supplied to
// resolve it; it is never itself placed in a diagnostic message.
var errRetrieverUnavailable = errors.New("filecontent: no content retriever configured")

// errUnsupportedSourceScheme and errUnsafeSourcePath are the two ways a
// File `source` value fails parsePuppetSourceURI. Unlike the two above
// these do reach a diagnostic, so neither text quotes the reference: a
// `source` is catalog data, it reaches CI logs and reports through the
// diagnostic, and echoing it back would undo the rule every adapter in
// this codebase follows about raw values in error text.
var (
	errUnsupportedSourceScheme = errors.New("filecontent: source reference does not use a supported puppet:// scheme for compiler-mediated retrieval")
	errUnsafeSourcePath        = errors.New("filecontent: source reference path contains an empty, \".\" or \"..\" segment, or a NUL byte, and is refused rather than resolved against the compiler")
)
