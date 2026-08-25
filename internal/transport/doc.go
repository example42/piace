// Package transport builds and operates PIACE's hardened, independent mTLS
// HTTP clients for the compiler and PuppetDB services, per design.md
// sections 2.2, 10, and 11 and requirements.md 3.1-3.5, 10.1/10.5, 12.4.
//
// # Scope
//
// This package is the first place actual file I/O happens against a
// resolve.Endpoint's CA bundle, client certificate, and private-key paths
// (internal/config/resolve validates those paths only for syntax, per its
// own doc.go). It is also the last place a raw network error is observed
// before it must be classified and reported without secret disclosure.
//
// It does not know about compiler or PuppetDB wire formats. It builds
// *http.Client values, executes requests with a bounded deadline and
// response size, and hands back a status code and body for a protocol
// adapter (tasks 4-6) to interpret. It never inspects HTTP response bodies
// or status codes for meaning.
//
// # Independence
//
// The compiler and PuppetDB clients are built from two entirely independent
// *tls.Config and *http.Transport values, even when an installation
// deliberately points both services at the same certificate files
// (requirements.md 3.3). Nothing here shares a connection pool, session
// cache, or *tls.Config pointer between the two services.
//
// # Design decisions made where requirements.md/design.md are silent
//
//  1. Default per-request timeout: neither document names a default
//     request deadline (design.md section 3.2 rule 7 only defines how a
//     target's impact-estimate timeout composes with an unspecified
//     "service deadline", and resolve.go's package comment documents that
//     gap explicitly as left for this task). This package defines
//     DefaultTimeout = 30 * time.Second as a documented, reasonable
//     default for compiler/PuppetDB requests and applies it both as a
//     context.WithTimeout per request and as the *http.Client.Timeout
//     (defense in depth: Client.Timeout also bounds the full
//     redirect-following/response-read sequence as one deadline, not just
//     connection setup). A caller with a smaller effective deadline (e.g.
//     resolve.Target.ImpactEstimate.Timeout) passes it explicitly to
//     NewRequest/Do instead of this default.
//  2. Default maximum response body size: neither document names a body
//     size limit. This package defines DefaultMaxResponseBodyBytes = 64
//     MiB. Puppet catalog JSON documents are typically well under this for
//     even large infrastructures, while 64 MiB is small enough to bound
//     memory use against a misbehaving or compromised endpoint.
//  3. Redirect authority equality is judged on scheme+host (net/http's
//     url.URL.Host already includes an explicit port), matching design.md
//     section 2.2's "no redirects to another authority."
//  4. Error classification: every error this package itself produces
//     (TLS/CA/cert load failure, non-https construction, TLS handshake
//     failure, dial/DNS failure, context deadline exceeded, redirect
//     rejection, response-too-large) is design.md section 10's
//     "operational error" class — see errors.go's Error/ErrorKind. This
//     package never produces a compilation-failure classification: a
//     non-2xx HTTP response is not an error at this layer at all (Do
//     returns it as a plain status code with nil error), and interpreting
//     it as a compilation failure requires v3/v4 response-shape knowledge
//     that belongs to task 6's compiler adapter, not this package.
package transport
