// Package safemeta projects untrusted HTTP metadata for diagnostics.
package safemeta

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const MaxTextBytes = 256

// Text bounds output bytes and escapes controls, including Unicode controls.
func Text(s string) string {
	var b strings.Builder
	for _, r := range s {
		piece := string(r)
		if !strconv.IsPrint(r) {
			quoted := strconv.QuoteRuneToASCII(r)
			piece = quoted[1 : len(quoted)-1]
		}
		if b.Len()+len(piece) > MaxTextBytes-3 {
			b.WriteString("...")
			break
		}
		b.WriteString(piece)
	}
	return b.String()
}

var apiVersion = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}(-preview)?$`)

// URL omits userinfo, fragments and all query values except a dated API version.
func URL(u *url.URL) string {
	if u == nil {
		return "<invalid URL>"
	}
	safe := *u
	safe.User, safe.Fragment, safe.RawFragment = nil, "", ""
	safe.RawQuery = ""
	safe.ForceQuery = false
	if strings.HasPrefix(safe.Path, "/puppet/v3/file_content/") {
		safe.Path, safe.RawPath = "/puppet/v3/file_content/<redacted>", ""
	}
	if values := u.Query()["api-version"]; len(values) == 1 && apiVersion.MatchString(values[0]) {
		safe.RawQuery = url.Values{"api-version": values}.Encode()
	}
	return Text(safe.String())
}

// RequestError keeps URL-bearing and provider-generated error text private.
func RequestError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &requestError{operation: operation, cause: err}
}

type requestError struct {
	operation string
	cause     error
}

func (e *requestError) Error() string { return e.operation + " failed" }
func (e *requestError) Unwrap() error { return e.cause }
