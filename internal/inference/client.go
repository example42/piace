package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/example42/piace/internal/safemeta"
)

// maxResponseBodyBytes bounds one response. A change assessment is a few
// kilobytes of JSON; anything approaching this is a misconfigured
// endpoint, not an answer.
const maxResponseBodyBytes int64 = 8 << 20

// Client is a hardened client for one OpenAI-compatible inference
// service.
//
// It is the only place in PIACE that sets an Authorization header.
// internal/transport deletes that header from every request it makes,
// deliberately, because the compiler and PuppetDB authenticate by mTLS
// and a stolen services file must yield nothing usable. This client is
// the scoped exception to that rule, and it is a separate package so the
// exception is visible in the import graph rather than buried in a
// conditional. See CONTEXT.md.
type Client struct {
	// httpClient is the transport New built, or the one SetHTTPClient
	// substituted. It is unexported so the redirect policy below cannot be
	// dropped by assignment: see SetHTTPClient.
	httpClient *http.Client

	url     *url.URL
	token   string
	timeout time.Duration

	// observer and captureBodies back the --debug seam. Both are off by
	// default; see debug.go. observer is invoked synchronously from
	// Complete and must not change what Complete returns.
	observer      Observer
	captureBodies bool
}

// New builds a client for u. Only https is accepted, and a token is
// required: PIACE never mints or discovers a credential on its own, so a
// missing one is a configuration error rather than an anonymous request.
//
// Options are applied after the validated fields; see WithObserver and
// WithBodyCapture in debug.go.
func New(u *url.URL, token string, timeout time.Duration, opts ...Option) (*Client, error) {
	if u == nil {
		return nil, fmt.Errorf("inference: no endpoint configured")
	}
	if u.User != nil {
		return nil, fmt.Errorf("inference: endpoint userinfo is forbidden")
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("inference: endpoint scheme must be https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("inference: endpoint has no host")
	}
	if token == "" {
		return nil, fmt.Errorf("inference: no bearer token configured")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("inference: timeout must be positive")
	}
	endpoint := *u
	c := &Client{
		httpClient: &http.Client{Timeout: timeout, CheckRedirect: checkRedirect},
		url:        &endpoint,
		token:      token,
		timeout:    timeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// checkRedirect is this package's counterpart to internal/transport's
// rule that no redirect may reach another authority, and it exists for a
// sharper reason than symmetry: this is the one client in PIACE that
// carries a bearer token, and net/http's own redirect policy is not
// enough to keep it.
//
// net/http drops Authorization only when the redirect target is neither
// the original host nor a subdomain of it, and it judges that on the
// host alone. A 302 from https://<endpoint> to http://<same host> is
// therefore followed with the token attached, in cleartext; so is one to
// a sibling subdomain of the provider's domain. Neither is a redirect a
// chat-completions endpoint has any reason to issue, so both are refused
// here rather than left to a rule written for browsers.
//
// The header is deleted as well as the redirect refused. Returning an
// error already stops the request, but the deletion means a future
// caller who relaxes this policy does not silently reintroduce the leak.
func checkRedirect(req *http.Request, via []*http.Request) error {
	req.Header.Del("Authorization")
	if req.URL.User != nil {
		return fmt.Errorf("inference: redirect userinfo is forbidden")
	}

	if len(via) == 0 {
		return nil
	}
	orig := via[0].URL
	if req.URL.Scheme != "https" || req.URL.Host != orig.Host {
		return fmt.Errorf("inference: refused a redirect from %s://%s to %s://%s: an inference endpoint may not redirect to another authority or off https",
			orig.Scheme, orig.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// SetHTTPClient substitutes the HTTP client requests are issued through
// and reapplies the redirect policy to it, so a substituted client can
// never be one that carries the bearer token off https or to another
// authority.
//
// It exists so a test can reach a stub server over TLS with a generated
// certificate. It is a method rather than an exported field because the
// field was the footgun: assigning a plain *http.Client dropped
// checkRedirect silently, and the one client in PIACE that holds a
// credential is the worst place for a policy that can be lost by
// assignment. A nil h is ignored, so a caller can pass one through
// unconditionally.
func (c *Client) SetHTTPClient(h *http.Client) {
	if h == nil {
		return
	}
	client := *h
	client.CheckRedirect = checkRedirect
	c.httpClient = &client
}

// Authority is the endpoint's host, safe to record in an artifact so a
// reader can audit where an assessment came from.
func (c *Client) Authority() string { return c.url.Host }

// chatResponse is the part of a chat-completions envelope PIACE reads.
// Everything else, usage, fingerprints and tool calls among it, is the
// service's business.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete sends one request and returns the assistant message's content
// verbatim, for internal/assess to validate.
//
// A non-2xx status is reported by status only. The response body of a
// failed inference request routinely carries account, project, and quota
// details belonging to whoever configured the service, and PIACE's
// diagnostics reach CI logs and reports.
func (c *Client) Complete(ctx context.Context, req Request) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("inference: encoding request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url.String(), bytes.NewReader(body))
	if err != nil {
		return nil, safemeta.RequestError("building inference request", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		err = safemeta.RequestError("inference request", err)
		c.emit(Event{
			Method: http.MethodPost, URL: safemeta.URL(c.url), Host: c.url.Host,
			Duration: time.Since(start), RequestBodyBytes: len(body),
			Err: err, RequestBody: body,
		})
		return nil, fmt.Errorf("inference: requesting %s: %w", c.url.Host, err)
	}
	defer resp.Body.Close()

	// Read one byte past the limit, as internal/transport does, so a body
	// that exactly reaches it succeeds and one that exceeds it is
	// detected rather than silently truncated. Without the extra byte an
	// oversized response comes back as a JSON fragment and is reported as
	// "not a chat completion", which sends an operator looking at the
	// wrong thing.
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	tooLarge := int64(len(raw)) > maxResponseBodyBytes
	if tooLarge {
		raw = raw[:maxResponseBodyBytes]
	}

	shape, keys, keysTruncated := describeBody(raw)
	c.emit(Event{
		Method: http.MethodPost, URL: safemeta.URL(c.url), Host: c.url.Host,
		StatusCode: resp.StatusCode, Duration: time.Since(start),
		RequestBodyBytes: len(body), ResponseBodyBytes: len(raw),
		ContentType:   safemeta.Text(resp.Header.Get("Content-Type")),
		Shape:         shape,
		TopLevelKeys:  keys,
		KeysTruncated: keysTruncated,
		Err:           safemeta.RequestError("reading inference response", readErr),
		RequestBody:   body,
		ResponseBody:  raw,
	})

	if readErr != nil {
		return nil, safemeta.RequestError("reading inference response", readErr)
	}
	if tooLarge {
		return nil, fmt.Errorf("inference: %s returned a response body exceeding the %d byte limit", c.url.Host, maxResponseBodyBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Status only: a failed inference response body routinely carries
		// account, project, and quota details belonging to whoever
		// configured the service, and this error becomes a diagnostic that
		// reaches reports and CI logs. Operators who need the body ask for
		// it explicitly with `piace explain --debug-dump-dir`.
		return nil, fmt.Errorf("inference: %s returned status %d", c.url.Host, resp.StatusCode)
	}

	var envelope chatResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("inference: %s returned a response that is not a chat completion", c.url.Host)
	}
	if len(envelope.Choices) == 0 || envelope.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("inference: %s returned no message content", c.url.Host)
	}
	return []byte(envelope.Choices[0].Message.Content), nil
}
