package compiler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
	"github.com/example42/piace/internal/transport"
)

// isVerifiedUnsupportedV4 implements this package's "verified unsupported
// v4 response" detection rule, the judgment call design.md section 3.1
// leaves to the adapter: "A v4 request may fall back only for a
// documented unsupported-endpoint or unsupported-version response."
//
// Rule: exactly HTTP 404 (Not Found) or 501 (Not Implemented) on the v4
// request, decided from the status code alone, before any response body
// is inspected.
//
//   - 404 is the literal, unambiguous signal that `POST
//     /puppet/v4/catalog` is not a registered route at all — the case a
//     Puppet Server build older than 6.3.0 (which introduced the v4
//     endpoint; see doc.go) or an OpenVox compiler (which documents only
//     v3; see doc.go) would produce, since neither server has any route
//     bound to that path. This is exactly "unsupported-endpoint."
//   - 501 is the standard HTTP status a server uses to say "the server
//     does not support the functionality required to fulfill the
//     request" — the natural status for a server that recognizes the
//     path/method but has deliberately not implemented it (e.g. a
//     feature-flagged or version-gated v4 handler that responds rather
//     than 404ing). This is "unsupported-version" in the absence of any
//     publicly documented, fixture-verified alternative status/body
//     convention for that exact case.
//
// This rule is deliberately status-code-only and independent of response
// body content: design.md section 3.1 requires that fallback "must not
// fall back after authentication, authorization, timeout, malformed
// response, or candidate identity/environment mismatch" — none of those
// conditions can ever produce a 404 or 501 by definition (401/403 for
// auth/authz, a transport.Error with no HTTP status at all for
// timeout/malformed-response-below-the-HTTP-layer, and a 2xx response
// body for identity/environment mismatch), so a status-code-only rule
// cannot accidentally satisfy this prohibition list. A body-shape-based
// rule was deliberately rejected: there is no publicly documented,
// fixture-verified "unsupported" error body shape to check, and requiring
// one would make this rule silently inert against a real server that
// signals "unsupported" via status code alone (the common case for an
// unregistered route).
func isVerifiedUnsupportedV4(statusCode int) bool {
	return statusCode == http.StatusNotFound || statusCode == http.StatusNotImplemented
}

// processResponse implements design.md section 5's response validation
// contract for a received (non-fallback-triggering) HTTP response: "Its
// contract requires that the returned catalog identify the requested
// certname and candidate environment exactly. A non-2xx compiler
// response, semantic request rejection, identity mismatch, or environment
// mismatch is a compilation failure."
//
// A malformed/unparseable response body is deliberately NOT included in
// that compilation-failure list (design.md section 5 names exactly four
// conditions; malformed response is absent), and design.md section 3.1
// treats "malformed response" as a condition distinct from a verified
// compiler rejection (fallback must not happen for it, the same way it
// must not happen for a transport timeout). This package therefore
// classifies a malformed/unparseable response body as an operational
// error (model.OperationRequestCandidateTransport) via design.md section
// 10's general taxonomy, which explicitly lists "response decoding/
// normalization" under "operational error" — matching how a malformed
// PuppetDB response is already classified by internal/puppetdb/adapter.go
// (model.OperationLoadFacts/OperationLoadBaseline, both operational),
// rather than forcing every response-shape problem into the same
// compilation-failure bucket as a verified rejection.
func processResponse(resp *transport.Response, host, certname, environment string) (puppetdb.Catalog, *model.Diagnostic) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		diag := compilationFailureDiagnostic(certname, host, resp.StatusCode,
			"compiler returned a non-2xx status for the candidate catalog request")
		return puppetdb.Catalog{}, &diag
	}

	var probe wireErrorProbe
	if err := json.Unmarshal(resp.Body, &probe); err != nil {
		diag := operationalResponseDiagnostic(certname, host, resp.StatusCode,
			"malformed or unparseable compiler catalog response")
		return puppetdb.Catalog{}, &diag
	}
	if probe.Error != "" {
		// The raw probe.Error text is never placed into the diagnostic
		// message: it is compiler-supplied text that can echo request
		// content, matching design.md's Error Handling principle already
		// applied by internal/puppetdb/adapter.go's notFoundOrMalformedDiagnostic.
		diag := compilationFailureDiagnostic(certname, host, resp.StatusCode,
			"compiler rejected the candidate catalog request (semantic request rejection)")
		return puppetdb.Catalog{}, &diag
	}

	var wc wireCatalog
	if err := json.Unmarshal(resp.Body, &wc); err != nil || wc.Name == "" {
		diag := operationalResponseDiagnostic(certname, host, resp.StatusCode,
			"malformed or unparseable compiler catalog response")
		return puppetdb.Catalog{}, &diag
	}

	if wc.Name != certname {
		diag := compilationFailureDiagnostic(certname, host, resp.StatusCode,
			"compiler catalog response certname does not match the requested target (identity mismatch)")
		return puppetdb.Catalog{}, &diag
	}
	if wc.Environment != environment {
		diag := compilationFailureDiagnostic(certname, host, resp.StatusCode,
			"compiler catalog response environment does not match the requested candidate environment")
		return puppetdb.Catalog{}, &diag
	}

	return puppetdb.Catalog{
		Certname:        wc.Name,
		Version:         string(wc.Version),
		Environment:     wc.Environment,
		TransactionUUID: derefOrEmpty(wc.TransactionUUID),
		CatalogUUID:     derefOrEmpty(wc.CatalogUUID),
		CodeID:          derefOrEmpty(wc.CodeID),
		Resources:       wc.Resources,
		Edges:           wc.Edges,
	}, nil
}

// compilationFailureDiagnostic builds a model.Diagnostic classified as
// design.md section 10's "compilation failure" (model.OperationRequestCandidate).
func compilationFailureDiagnostic(certname, host string, statusCode int, message string) model.Diagnostic {
	return transport.Diagnostic(model.OperationRequestCandidate, certname, transport.Summary{Host: host, StatusCode: statusCode}, message)
}

// compilationFailureDiagnosticNoResponse builds a compilation-failure
// diagnostic for a policy prerequisite that failed before any HTTP
// request was attempted (the v4 trusted-fact-source-unavailable case),
// per design.md section 10's explicit "v4 trusted-fact requirements are
// unmet" compilation-failure condition.
func compilationFailureDiagnosticNoResponse(certname, host, message string) model.Diagnostic {
	return transport.Diagnostic(model.OperationRequestCandidate, certname, transport.Summary{Host: host}, message)
}

// operationalResponseDiagnostic builds a model.Diagnostic classified as
// design.md section 10's "operational error"
// (model.OperationRequestCandidateTransport) for a response that was
// received but could not be decoded/normalized.
func operationalResponseDiagnostic(certname, host string, statusCode int, message string) model.Diagnostic {
	return transport.Diagnostic(model.OperationRequestCandidateTransport, certname, transport.Summary{Host: host, StatusCode: statusCode}, message)
}

// operationalLocalDiagnostic builds a model.Diagnostic classified as
// design.md section 10's "operational error" for a local failure that
// occurred before any HTTP request was sent (e.g. malformed input
// factset shape encountered while building the request body).
func operationalLocalDiagnostic(certname, host, message string) model.Diagnostic {
	return transport.Diagnostic(model.OperationRequestCandidateTransport, certname, transport.Summary{Host: host}, message)
}

// diagnosticFromTransportError builds a model.Diagnostic from a
// transport-layer error, mirroring internal/puppetdb/adapter.go's helper
// of the same purpose: it prefers a classified *transport.Error's own
// safe Kind/Host/Message fields and falls back to transport.SafeMessage
// for any other error reaching this package. Always classified as
// model.OperationRequestCandidateTransport (operational error): every
// error transport.Client.Do/NewRequest can return is, per
// internal/transport/doc.go decision 4, an operational error this
// package must not reclassify as a compilation failure.
func diagnosticFromTransportError(certname string, err error) model.Diagnostic {
	var te *transport.Error
	if errors.As(err, &te) {
		return transport.Diagnostic(model.OperationRequestCandidateTransport, certname, transport.Summary{Host: te.Host}, te.Message)
	}
	return transport.Diagnostic(model.OperationRequestCandidateTransport, certname, transport.Summary{}, transport.SafeMessage(err))
}
