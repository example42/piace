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
