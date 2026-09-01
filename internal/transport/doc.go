// Package transport builds and operates PIACE's hardened, independent
// mTLS HTTP clients for the compiler and PuppetDB services.
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
// adapter to interpret. It never inspects HTTP response bodies
// or status codes for meaning.
//
// # Independence
//
// The compiler and PuppetDB clients are built from two entirely
// independent *tls.Config and *http.Transport values, even when an
// installation deliberately points both services at the same certificate
// files. Nothing here shares a connection pool, session cache, or
// *tls.Config pointer between the two services.
//
// # Design decisions made where nothing else settles the question
//
//  1. Default per-request timeout: nothing names a default request
//     deadline. What is defined is only how a target's impact-estimate
//     timeout composes with an unspecified "service deadline", and
//     resolve.go's package comment documents that gap explicitly. This
//     package defines
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
//     url.URL.Host already includes an explicit port), which is what "no
//     redirects to another authority" means here.
//  4. Error classification: every error this package itself produces
//     (TLS/CA/cert load failure, non-https construction, TLS handshake
//     failure, dial/DNS failure, context deadline exceeded, redirect
//     rejection, response-too-large) is in the "operational error"
//     class; see errors.go's Error and ErrorKind. This
//     package never produces a compilation-failure classification: a
//     non-2xx HTTP response is not an error at this layer at all (Do
//     returns it as a plain status code with nil error), and interpreting
//     it as a compilation failure requires v3/v4 response-shape knowledge
//     that belongs to internal/compiler's compiler adapter, not this package.
package transport
