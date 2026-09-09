package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/safemeta"
)

// DefaultTimeout is the per-request deadline applied when neither the
// caller nor the service's configuration names one. See doc.go decision
// 1 for the full precedence: a caller's explicit per-request deadline
// wins, then the service's configured `timeout`, then this value.
const DefaultTimeout = 30 * time.Second

// DefaultMaxResponseBodyBytes bounds a single response body. See doc.go
// decision 2: nothing else names a limit; 64 MiB comfortably covers
// large Puppet catalog JSON documents while still bounding memory use
// against a misbehaving or compromised endpoint.
const DefaultMaxResponseBodyBytes int64 = 64 * 1024 * 1024

// Client is one hardened, independent mTLS HTTP client for a single
// resolve.Endpoint, compiler or PuppetDB. Two Clients built from two
// NewClient calls never share a *tls.Config or *http.Transport, even
// when their resolve.Endpoint values name identical certificate files:
// each call constructs its own tls.Config and http.Transport from
// scratch.
type Client struct {
	httpClient   *http.Client
	host         string // authority (host:port) this client is dedicated to
	timeout      time.Duration
	maxBodyBytes int64
	// observer, when non-nil, receives one Event per executed request.
	// See debug.go for the seam and its redaction boundary.
	observer Observer
	// captureBodies makes each emitted Event carry raw request/response
	// bodies. Off unless a caller opted in with WithBodyCapture.
	captureBodies bool
}

// Option customizes a Client at construction time. Callers building the
// compiler/PuppetDB clients from resolved configuration normally need
// none of these; they exist so a target's smaller effective deadline or
// a non-default body limit can be applied without a parallel
// construction path.
type Option func(*Client)

// WithTimeout overrides this client's default per-request deadline. It
// does not set *http.Client.Timeout: that field is a single deadline for
// every request the client will ever make, so setting it would cap a
// caller that legitimately asks Do for a longer one.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithMaxResponseBodyBytes overrides DefaultMaxResponseBodyBytes.
func WithMaxResponseBodyBytes(n int64) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxBodyBytes = n
		}
	}
}

// NewClient builds one hardened mTLS *Client for ep. This is the first
// point actual file I/O happens against ep's CA bundle, client
// certificate, and private-key paths (see doc.go); any read, parse, or
// PEM-decode failure here is an *Error with Kind KindConfig, an
// operational error.
//
// Enforced per client:
//
//   - ep.URL.Scheme must be "https" (defense in depth: resolve already
//     validates this before NewClient is ever reached);
//   - ep.URL's host must be non-empty and becomes tls.Config.ServerName
//     (defense in depth: resolve already validates this too);
//   - the specific client certificate/key pair from ep.ClientCert/
//     ep.PrivateKey via tls.Certificates;
//   - the specific CA pool from ep.CABundle via tls.Config.RootCAs;
//   - tls.Config.MinVersion = tls.VersionTLS12;
//   - a CheckRedirect that rejects any redirect to a different
//     scheme/host/port than the request's original authority (see
//     checkRedirect).
func NewClient(ep resolve.Endpoint, opts ...Option) (*Client, error) {
	if ep.URL == nil {
		return nil, newError(KindConfig, "", "endpoint URL is not set", nil)
	}
	if ep.URL.User != nil {
		return nil, newError(KindConfig, "", "endpoint userinfo is forbidden", nil)
	}
	host := ep.URL.Host
	if ep.URL.Scheme != "https" {
		return nil, newError(KindConfig, host, fmt.Sprintf("endpoint scheme must be https, got %q", ep.URL.Scheme), nil)
	}
	serverName := ep.URL.Hostname()
	if serverName == "" {
		return nil, newError(KindConfig, host, "endpoint host is empty", nil)
	}

	cert, err := tls.LoadX509KeyPair(ep.ClientCert, ep.PrivateKey)
	if err != nil {
		return nil, newError(KindConfig, host, "loading client certificate/private key", err)
	}

	caPEM, err := os.ReadFile(ep.CABundle)
	if err != nil {
		return nil, newError(KindConfig, host, "reading CA bundle", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, newError(KindConfig, host, "CA bundle contains no usable PEM certificates", nil)
	}

	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		ServerName:   serverName,
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
	}

	httpClient := &http.Client{
		// A dedicated *http.Transport per Client: two Clients never share a
		// connection pool or session cache, even if their tlsConfig values
		// were built from identical certificate files.
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		// Timeout is deliberately left zero. It is one deadline for every
		// request a client ever makes, so a value here silently shortens
		// any longer per-request deadline Do is asked for: a target
		// configured with a 90-second impact-estimate timeout used to get
		// 30 seconds and a timeout diagnostic naming a deadline it never
		// had. Do applies a context deadline to every request instead,
		// which bounds the whole exchange (dial, TLS handshake, redirects,
		// and the body read Do performs before returning) and is
		// per-request rather than per-client.
		CheckRedirect: checkRedirect,
	}

	timeout := DefaultTimeout
	if ep.Timeout > 0 {
		timeout = ep.Timeout
	}
	c := &Client{
		httpClient:   httpClient,
		host:         host,
		timeout:      timeout,
		maxBodyBytes: DefaultMaxResponseBodyBytes,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// checkRedirect implements the rule that no redirect may reach another
// authority: it allows a redirect only when the new request's scheme is
// https and its host (net/http's url.URL.Host, which already includes an
// explicit port) exactly matches the original request's scheme and host.
// Any other redirect is rejected, which stops *http.Client from
// following it and surfaces a *Error with KindRedirectRejected instead.
//
// Authorization-header note: PIACE authenticates exclusively via mTLS
// and this package never sets an Authorization header on any request it
// builds. As defense in depth against a future caller adding one, this
// function strips any Authorization header from the redirected request
// before net/http would send it, even for an allowed same-authority
// redirect.
func checkRedirect(req *http.Request, via []*http.Request) error {
	req.Header.Del("Authorization")
	if req.URL.User != nil {
		return newError(KindRedirectRejected, "", "redirect userinfo is forbidden", nil)
	}

	if len(via) == 0 {
		return nil
	}
	orig := via[0].URL
	if req.URL.Scheme != "https" {
		return newError(KindRedirectRejected, req.URL.Host,
			"redirect to non-https scheme rejected", nil)
	}
	if req.URL.Host != orig.Host || req.URL.Scheme != orig.Scheme {
		return newError(KindRedirectRejected, req.URL.Host,
			"redirect to a different authority rejected", nil)
	}
	return nil
}

// Response is a fully-read, size-bounded HTTP response. Body is already
// materialized (never larger than the client's configured maximum), so
// protocol adapters do not need to manage streaming or
// remember to close a body.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// NewRequest wraps http.NewRequestWithContext. It exists so every
// request this package's callers build goes through one helper, per
// doc.go's framing of the deadline as a helper rather than only a global
// Client.Timeout. It does not itself apply the deadline; Do does, per
// request.
func (c *Client) NewRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, newError(KindConfig, c.host, "invalid request URL", err)
	}
	return req, nil
}

// Do executes req with a bounded deadline and a bounded response body
// size, and returns a structured, classified *Error (never a bare stdlib
// error) on any failure.
//
// Deadline: Do wraps req's context in context.WithTimeout using timeout,
// or the Client's configured default when timeout <= 0. That context is
// the only deadline (see NewClient for why *http.Client.Timeout stays
// zero), and it bounds the whole exchange: dial, TLS handshake, allowed
// redirects, and the response-body read Do performs before returning.
// The precedence is caller, then the service's configured `timeout`,
// then DefaultTimeout; see doc.go decision 1.
//
// Authorization: any Authorization header on req is deleted before the
// request is sent. See the note in checkRedirect.
//
// Body size: the response body is read through an io.LimitedReader capped
// at the Client's configured maximum plus one byte, so a body that exactly
// reaches the limit succeeds and a body that exceeds it is detected and
// reported as *Error{Kind: KindResponseTooLarge} rather than silently
// truncated.
func (c *Client) Do(req *http.Request, timeout time.Duration) (*Response, error) {
	if req.URL == nil || req.URL.User != nil || req.URL.Scheme != "https" || req.URL.Host != c.host || (req.Host != "" && req.Host != c.host) {
		return nil, newError(KindConfig, c.host, "request must use the configured HTTPS authority without userinfo", nil)
	}
	if timeout <= 0 {
		timeout = c.timeout
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	req = req.WithContext(ctx)

	// PIACE authenticates to the compiler and PuppetDB exclusively via mTLS,
	// so no request this package sends carries a bearer token, whatever a
	// caller set. checkRedirect strips it again on an allowed same-authority
	// redirect.
	//
	// internal/inference is the one scoped exception, and it is a
	// separate client precisely so this line can stay unconditional. See
	// CONTEXT.md for the scope of that exception.
	req.Header.Del("Authorization")

	// The request body is snapshotted before the request is sent, while
	// req.GetBody still can replay it; net/http consumes the original
	// reader. Only done when a caller opted into body capture.
	var requestBody []byte
	if c.observer != nil && c.captureBodies {
		requestBody = snapshotRequestBody(req)
	}
	started := time.Now()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		classified := classifyDoErr(req.URL.Host, err)
		c.observe(req, requestBody, nil, 0, "", time.Since(started), classified)
		return nil, classified
	}
	defer resp.Body.Close()

	limited := &io.LimitedReader{R: resp.Body, N: c.maxBodyBytes + 1}
	body, readErr := io.ReadAll(limited)
	if readErr != nil {
		wrapped := newError(KindUnknown, req.URL.Host, "reading response body", readErr)
		c.observe(req, requestBody, nil, resp.StatusCode, resp.Header.Get("Content-Type"), time.Since(started), wrapped)
		return nil, wrapped
	}
	if int64(len(body)) > c.maxBodyBytes {
		tooLarge := newError(KindResponseTooLarge, req.URL.Host,
			fmt.Sprintf("response body exceeded the %d byte limit", c.maxBodyBytes), nil)
		c.observe(req, requestBody, nil, resp.StatusCode, resp.Header.Get("Content-Type"), time.Since(started), tooLarge)
		return nil, tooLarge
	}

	c.observe(req, requestBody, body, resp.StatusCode, resp.Header.Get("Content-Type"), time.Since(started), nil)
	return &Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

// snapshotRequestBody replays req's body via GetBody, which
// http.NewRequestWithContext populates for the in-memory readers this
// package's callers use. It returns nil for a request with no body, or
// one whose body cannot be replayed: debug observation must never change
// what is sent or fail a request.
func snapshotRequestBody(req *http.Request) []byte {
	if req.GetBody == nil {
		return nil
	}
	rc, err := req.GetBody()
	if err != nil {
		return nil
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return data
}

// observe emits one Event to the configured Observer, if any. It is the
// only place this package derives anything from a response body, and it
// derives only the body's outermost shape and top-level member names
// (see debug.go). Raw bodies are attached solely when the Client was
// built WithBodyCapture.
func (c *Client) observe(req *http.Request, requestBody, responseBody []byte, statusCode int, contentType string, elapsed time.Duration, err error) {
	if c.observer == nil {
		return
	}
	shape, keys, truncated := describeBody(responseBody)
	ev := Event{
		Method:            req.Method,
		URL:               safemeta.URL(req.URL),
		Host:              req.URL.Host,
		StatusCode:        statusCode,
		Duration:          elapsed,
		RequestBodyBytes:  req.ContentLength,
		ResponseBodyBytes: len(responseBody),
		ContentType:       safemeta.Text(contentType),
		Shape:             shape,
		TopLevelKeys:      keys,
		KeysTruncated:     truncated,
		Err:               err,
	}
	if c.captureBodies {
		ev.RequestBody = requestBody
		ev.ResponseBody = responseBody
	}
	c.observer(ev)
}

// Host returns the authority (host:port) this Client is dedicated to, safe
// to include in logs/diagnostics.
func (c *Client) Host() string { return c.host }

// classifyDoErr maps an error returned by (*http.Client).Do into this
// package's *Error taxonomy. Every branch here still yields an
// operational error (see doc.go decision 4); Kind only distinguishes the
// operational sub-category for a caller that wants to present a more
// specific message.
func classifyDoErr(host string, err error) error {
	var alreadyClassified *Error
	if errors.As(err, &alreadyClassified) {
		// e.g. our own checkRedirect rejection, unwrapped from *url.Error.
		return alreadyClassified
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return newError(KindTimeout, host, "request deadline exceeded", err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newError(KindTimeout, host, "network operation timed out", err)
	}

	var certVerifyErr *tls.CertificateVerificationError
	if errors.As(err, &certVerifyErr) {
		return newError(KindTLS, host, "TLS certificate verification failed", err)
	}
	var unknownAuthErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthErr) {
		return newError(KindTLS, host, "TLS certificate verification failed: unknown authority", err)
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return newError(KindTLS, host, "TLS certificate verification failed: server name mismatch", err)
	}
	var recordHeaderErr tls.RecordHeaderError
	if errors.As(err, &recordHeaderErr) {
		return newError(KindTLS, host, "TLS handshake failed", err)
	}
	// crypto/tls reports several handshake failures (including protocol
	// version mismatches) as a plain *errors.errorString/opaque error whose
	// text is prefixed "tls:"; there is no exported typed error for every
	// case, so this documented string check is the remaining fallback.
	if strings.Contains(err.Error(), "tls:") {
		return newError(KindTLS, host, "TLS handshake failed", err)
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return newError(KindConnect, host, "DNS resolution failed", err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return newError(KindConnect, host, "network connection failed", err)
	}

	return newError(KindUnknown, host, "request failed", err)
}

// IsOperational reports whether err is (or wraps) an *Error produced by
// this package. Every *Error is an operational error (see doc.go
// decision 4); this lets a caller branch on that without a direct type
// assertion.
func IsOperational(err error) bool {
	var te *Error
	return errors.As(err, &te)
}
