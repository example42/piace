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
// conditional. See
// docs/adr/0003-authenticate-the-inference-service-with-a-bearer-token.md.
type Client struct {
	// HTTPClient is exported so a test can substitute a stub server's
	// client. Production callers use the one New builds.
	HTTPClient *http.Client

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
	c := &Client{
		HTTPClient: &http.Client{Timeout: timeout},
		url:        u,
		token:      token,
		timeout:    timeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Authority is the endpoint's host, safe to record in an artifact so a
// reader can audit where an assessment came from.
func (c *Client) Authority() string { return c.url.Host }

// chatResponse is the part of a chat-completions envelope PIACE reads.
// Everything else — usage, fingerprints, tool calls — is the service's
// business.
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
		return nil, fmt.Errorf("inference: building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	start := time.Now()
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		// url.Error stringifies to include the request URL but never a
		// header, so the token cannot appear here.
		c.emit(Event{
			Method: http.MethodPost, URL: c.url.String(), Host: c.url.Host,
			Duration: time.Since(start), RequestBodyBytes: len(body),
			Err: err, RequestBody: body,
		})
		return nil, fmt.Errorf("inference: requesting %s: %w", c.url.Host, err)
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))

	shape, keys, keysTruncated := describeBody(raw)
	c.emit(Event{
		Method: http.MethodPost, URL: c.url.String(), Host: c.url.Host,
		StatusCode: resp.StatusCode, Duration: time.Since(start),
		RequestBodyBytes: len(body), ResponseBodyBytes: len(raw),
		ContentType:   resp.Header.Get("Content-Type"),
		Shape:         shape,
		TopLevelKeys:  keys,
		KeysTruncated: keysTruncated,
		Err:           readErr,
		RequestBody:   body,
		ResponseBody:  raw,
	})

	if readErr != nil {
		return nil, fmt.Errorf("inference: reading response from %s: %w", c.url.Host, readErr)
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
