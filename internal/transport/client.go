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
)

// DefaultTimeout is the per-request deadline applied when a caller does not
// supply a smaller one. See doc.go decision 1: neither requirements.md nor
// design.md names a default, so this value is a documented assumption.
const DefaultTimeout = 30 * time.Second

// DefaultMaxResponseBodyBytes bounds a single response body. See doc.go
// decision 2: neither requirements.md nor design.md names a limit; 64 MiB
// comfortably covers large Puppet catalog JSON documents while still
// bounding memory use against a misbehaving or compromised endpoint.
const DefaultMaxResponseBodyBytes int64 = 64 * 1024 * 1024

// Client is one hardened, independent mTLS HTTP client for a single
// resolve.Endpoint (compiler or PuppetDB). Two Clients built from two
// NewClient calls never share a *tls.Config or *http.Transport, even when
// their resolve.Endpoint values name identical certificate files
// (requirements.md 3.3) — each call constructs its own tls.Config and
// http.Transport from scratch.
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
// compiler/PuppetDB clients from resolved configuration normally need none
// of these; they exist so a target's smaller effective deadline
// (design.md section 3.2 rule 7) or a non-default body limit can be
// applied without a parallel construction path.
type Option func(*Client)

// WithTimeout overrides DefaultTimeout for both the per-request context
// deadline and the underlying *http.Client.Timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
			c.httpClient.Timeout = d
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
// point actual file I/O happens against ep's CA bundle, client certificate,
// and private-key paths (see doc.go); any read, parse, or PEM-decode
// failure here is an *Error with Kind KindConfig, an operational error per
// design.md section 10.
//
// Enforced per client, per design.md section 2.2 and requirements.md 3.1-
// 3.5:
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
		Transport:     &http.Transport{TLSClientConfig: tlsConfig},
		Timeout:       DefaultTimeout,
		CheckRedirect: checkRedirect,
	}

	c := &Client{
		httpClient:   httpClient,
		host:         host,
		timeout:      DefaultTimeout,
		maxBodyBytes: DefaultMaxResponseBodyBytes,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// checkRedirect implements design.md section 2.2's "no redirects to
// another authority": it allows a redirect only when the new request's
// scheme is https and its host (net/http's url.URL.Host, which already
// includes an explicit port) exactly matches the original request's
// scheme and host. Any other redirect is rejected, which stops
// *http.Client from following it and surfaces a *Error with
// KindRedirectRejected instead.
//
// Authorization-header note (requirements.md 3.5): PIACE authenticates
// exclusively via mTLS and this package never sets an Authorization
// header on any request it builds. As defense in depth against a future
// caller adding one, this function strips any Authorization header from
// the redirected request before net/http would send it, even for an
// allowed same-authority redirect.
func checkRedirect(req *http.Request, via []*http.Request) error {
	req.Header.Del("Authorization")

	if len(via) == 0 {
		return nil
	}
	orig := via[0].URL
	if req.URL.Scheme != "https" {
		return newError(KindRedirectRejected, req.URL.Host,
			fmt.Sprintf("redirect to non-https scheme %q rejected", req.URL.Scheme), nil)
	}
	if req.URL.Host != orig.Host || req.URL.Scheme != orig.Scheme {
		return newError(KindRedirectRejected, req.URL.Host,
			fmt.Sprintf("redirect to a different authority rejected: %s://%s -> %s://%s",
				orig.Scheme, orig.Host, req.URL.Scheme, req.URL.Host), nil)
	}
	return nil
}

// Response is a fully-read, size-bounded HTTP response. Body is already
// materialized (never larger than the client's configured maximum), so
// protocol adapters (tasks 4-6) do not need to manage streaming or
// remember to close a body.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// NewRequest wraps http.NewRequestWithContext. It exists so every request
// this package's callers build goes through one helper (per doc.go's
// framing of the deadline as a helper, not only a global Client.Timeout);
// it does not itself apply the deadline — Do does, per request.
func (c *Client) NewRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, url, body)
}

// Do executes req with a bounded deadline and a bounded response body
// size, and returns a structured, classified *Error (never a bare stdlib
// error) on any failure.
//
// Deadline: Do wraps req's context in context.WithTimeout using timeout,
// or the Client's configured default when timeout <= 0. This is applied in
// addition to the *http.Client.Timeout set at construction (defense in
// depth: Client.Timeout also bounds the full redirect-following/response-
// read sequence as a single deadline, not only connection setup; see
// doc.go decision 1).
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
	if timeout <= 0 {
		timeout = c.timeout
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	req = req.WithContext(ctx)

	// requirements.md 3.5: PIACE authenticates to the compiler and
	// PuppetDB exclusively via mTLS, so no request this package sends
	// carries a bearer token — whatever a caller set. checkRedirect
	// strips it again on an allowed same-authority redirect.
	//
	// internal/inference is the one scoped exception, and it is a
	// separate client precisely so this line can stay unconditional. See
	// docs/adr/0003-authenticate-the-inference-service-with-a-bearer-token.md.
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
// one whose body cannot be replayed — debug observation must never
// change what is sent or fail a request.
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
		URL:               req.URL.String(),
		Host:              req.URL.Host,
		StatusCode:        statusCode,
		Duration:          elapsed,
		RequestBodyBytes:  req.ContentLength,
		ResponseBodyBytes: len(responseBody),
		ContentType:       contentType,
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
// package's *Error taxonomy. Every branch here still yields an operational
// error per design.md section 10 (see doc.go decision 4); Kind only
// distinguishes the operational sub-category for a caller that wants to
// present a more specific message.
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
// this package. Every *Error is design.md section 10's "operational
// error" class (see doc.go decision 4); this lets a caller branch on that
// without a direct type assertion.
func IsOperational(err error) bool {
	var te *Error
	return errors.As(err, &te)
}
