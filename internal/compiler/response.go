package compiler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
	"github.com/example42/piace/internal/transport"
)

// A generic 404 is an opt-in fallback signal, not proof of API support.
// Bodies describing errors, malformed JSON and HTML are refused. There is no
// verified Puppet Server contract for 501. A proxy can still mimic a generic
// 404, so callers always report this ambiguity and the v3 persistence effects.
func isFallbackEligibleV4(resp *transport.Response) bool {
	return resp.StatusCode == http.StatusNotFound &&
		(len(bytes.TrimSpace(resp.Body)) == 0 || string(bytes.TrimSpace(resp.Body)) == "Not Found")
}

// processResponse implements the response validation contract for a
// received, non-fallback-triggering HTTP response: the returned catalog
// must identify the requested certname and candidate environment
// exactly, and a non-2xx compiler response, semantic request rejection,
// identity mismatch, or environment mismatch is a compilation failure.
//
// A malformed or unparseable response body is deliberately NOT in that
// compilation-failure list, which names exactly four conditions and does
// not include it, and malformed response is a condition distinct from a
// verified compiler rejection: fallback must not happen for it, the same
// way it must not happen for a transport timeout. This package therefore
// classifies a malformed or unparseable response body as an operational
// error (model.OperationRequestCandidateTransport), since response
// decoding and normalization sit under the operational-error class. That
// matches how a malformed PuppetDB response is already classified by
// internal/puppetdb/adapter.go (model.OperationLoadFacts and
// model.OperationLoadBaseline, both operational), rather than forcing
// every response-shape problem into the same compilation-failure bucket
// as a verified rejection.
func processResponse(resp *transport.Response, host, certname, environment string, effectiveAPI config.CatalogAPI) (puppetdb.Catalog, *model.Diagnostic) {
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
		// The raw probe.Error text is never placed into the diagnostic message:
		// it is compiler-supplied text that can echo request content, the same
		// principle internal/puppetdb/adapter.go's notFoundOrMalformedDiagnostic
		// already applies.
		diag := compilationFailureDiagnostic(certname, host, resp.StatusCode,
			"compiler rejected the candidate catalog request (semantic request rejection)")
		return puppetdb.Catalog{}, &diag
	}

	document, diag := catalogDocument(resp, host, certname, effectiveAPI)
	if diag != nil {
		return puppetdb.Catalog{}, diag
	}

	var wc wireCatalog
	if err := json.Unmarshal(document, &wc); err != nil || wc.Name == "" {
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
		Certname:          wc.Name,
		Version:           string(wc.Version),
		Environment:       wc.Environment,
		TransactionUUID:   derefOrEmpty(wc.TransactionUUID),
		CatalogUUID:       derefOrEmpty(wc.CatalogUUID),
		CodeID:            derefOrEmpty(wc.CodeID),
		Resources:         wc.Resources,
		Edges:             wc.Edges,
		Metadata:          wc.Metadata,
		RecursiveMetadata: wc.RecursiveMetadata,
	}, nil
}

// catalogDocument extracts the catalog document from a 2xx compiler
// response according to the API version that actually served it.
//
// The two endpoints do not share a response envelope, and the difference
// is silent rather than loud: a v4 body decodes cleanly into wireCatalog
// with every field absent, which is why an unhandled v4 envelope
// surfaces as "malformed or unparseable response" against a compiler
// whose own log records a successful compilation. See doc.go's wire-shape
// section for the primary sources.
//
//   - v3 returns the catalog document as the whole response body.
//   - v4 returns `{"catalog": <document>}`.
//
// The version is taken from the caller rather than sniffed from the
// body. A "top-level `name`, else look under `catalog`" heuristic would
// accept either shape from either endpoint, which is exactly the
// speculative probing this package rules out, and it would also mask a
// compiler that started returning the wrong envelope.
//
// effectiveAPI is the API of the request that produced this very
// response, never target.Candidate.CatalogAPI: on the permitted v4-to-v3
// fallback path the target is configured for v4 while the response in
// hand came from v3.
func catalogDocument(resp *transport.Response, host, certname string, effectiveAPI config.CatalogAPI) (json.RawMessage, *model.Diagnostic) {
	if effectiveAPI != config.CatalogAPIv4 {
		return resp.Body, nil
	}
	// A present-but-null "catalog" member is treated the same as a
	// missing one: json.RawMessage("null") is four non-empty bytes, so
	// without this it would slip through to wireCatalog decoding and
	// report the generic malformed-response message instead of the
	// specific one naming the envelope.
	var envelope v4CatalogEnvelope
	if err := json.Unmarshal(resp.Body, &envelope); err != nil ||
		len(envelope.Catalog) == 0 || bytes.Equal(envelope.Catalog, []byte("null")) {
		diag := operationalResponseDiagnostic(certname, host, resp.StatusCode,
			`malformed or unparseable compiler catalog response: v4 response has no "catalog" member`)
		return nil, &diag
	}
	return envelope.Catalog, nil
}

// compilationFailureDiagnostic builds a model.Diagnostic classified as a
// compilation failure (model.OperationRequestCandidate).
func compilationFailureDiagnostic(certname, host string, statusCode int, message string) model.Diagnostic {
	return transport.Diagnostic(model.OperationRequestCandidate, certname, transport.Summary{Host: host, StatusCode: statusCode}, message)
}

// compilationFailureDiagnosticNoResponse builds a compilation-failure
// diagnostic for a policy prerequisite that failed before any HTTP
// request was attempted, the v4 trusted-fact-source-unavailable case:
// unmet v4 trusted-fact requirements are a compilation failure.
func compilationFailureDiagnosticNoResponse(certname, host, message string) model.Diagnostic {
	return transport.Diagnostic(model.OperationRequestCandidate, certname, transport.Summary{Host: host}, message)
}

// operationalResponseDiagnostic builds a model.Diagnostic classified as
// an operational error (model.OperationRequestCandidateTransport) for a
// response that was received but could not be decoded or normalized.
func operationalResponseDiagnostic(certname, host string, statusCode int, message string) model.Diagnostic {
	return transport.Diagnostic(model.OperationRequestCandidateTransport, certname, transport.Summary{Host: host, StatusCode: statusCode}, message)
}

// operationalLocalDiagnostic builds a model.Diagnostic classified as an
// operational error for a local failure that occurred before any HTTP
// request was sent, such as a malformed input factset shape encountered
// while building the request body.
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
