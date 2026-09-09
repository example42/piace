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
//  1. Request deadline precedence. Three sources can name a deadline,
//     and they compose in this order, most specific first:
//
//     a. the deadline a caller passes to Do for one request, which is
//     how a target's impact-estimate timeout reaches the transport;
//     b. the service's configured `timeout` (services.<section>.timeout,
//     resolved into resolve.Endpoint.Timeout);
//     c. DefaultTimeout = 30 * time.Second, this package's documented
//     default for compiler/PuppetDB requests, since nothing upstream
//     names one.
//
//     The chosen deadline is applied as a context.WithTimeout around the
//     request, and that context is the only deadline: *http.Client.Timeout
//     stays zero. It reads as defense in depth but is not, because it is
//     one value for every request a client makes: with it set to 30
//     seconds, a target asking for a 90-second impact-estimate deadline
//     received 30 and a timeout diagnostic quoting a deadline that was
//     never in force. The context deadline covers the same ground per
//     request (dial, handshake, redirects, and Do's own body read), so
//     nothing is lost by removing the cap.
//
//     A caller may therefore both shorten and lengthen the configured
//     default, which is the point: it knows what it is asking for. What
//     it cannot do is remove the deadline, since a non-positive value
//     means "use the client's default" rather than "wait forever".
//
//  2. Default maximum response body size: neither document names a body
//     size limit. This package defines DefaultMaxResponseBodyBytes = 64
//     MiB. Puppet catalog JSON documents are typically well under this for
//     even large infrastructures, while 64 MiB is small enough to bound
//     memory use against a misbehaving or compromised endpoint.
//
//  3. Redirect authority equality is judged on scheme+host (net/http's
//     url.URL.Host already includes an explicit port), which is what "no
//     redirects to another authority" means here.
//
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
//
// # Observable metadata and authority
//
// Configuration and construction reject URL userinfo. Every initial request
// must use the client's configured HTTPS authority, including its Host header;
// redirects remain on that authority and cannot introduce userinfo. Authorization
// is stripped on both paths. Errors retain causes for classification but their
// display text never includes URL-bearing lower-level errors.
//
// Debug URLs omit userinfo, fragments and arbitrary query values. The sole
// query allowlist entry is api-version in YYYY-MM-DD or YYYY-MM-DD-preview form.
// File-content request paths omit the source suffix, which can be sensitive.
// Response member names and content types are untrusted: safemeta.Text escapes
// controls and bounds each to 256 output bytes; at most 64 names are retained.
// Explicit body capture remains a separate raw-body opt-in.
package transport
