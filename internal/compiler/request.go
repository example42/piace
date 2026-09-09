package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/example42/piace/internal/transport"
)

// trustedFactsDecision is the outcome of the v4 trusted-fact policy for
// one request: either a validated trusted-fact value to send explicitly,
// or an instruction to omit the field and rely on the compiler's own
// PuppetDB lookup, or neither. Neither is a compilation failure the
// caller must report without ever issuing an HTTP request: if no source
// is available, PIACE fails compilation rather than inventing trusted
// facts.
type trustedFactsDecision struct {
	// available is false only when neither source applies; callers must
	// check this before issuing a v4 request.
	available bool
	// source is model.TrustedFactsProvided or
	// model.TrustedFactsCompilerLookup when available is true.
	source trustedFactsSource
	// value is the raw "trusted" fact JSON object to send as
	// trusted_facts.values; nil when source is compiler_lookup (the field
	// is omitted from the request entirely in that case).
	value json.RawMessage
}

// trustedFactsSource mirrors model.TrustedFactsSource without importing
// internal/model into this decision helper's signature, keeping the
// decision self-contained and testable independent of the model package's
// JSON tags. adapter.go converts it to model.TrustedFactsSource when
// building CandidateProvenance.
type trustedFactsSource int

const (
	trustedFactsSourceNone trustedFactsSource = iota
	trustedFactsSourceProvided
	trustedFactsSourceCompilerLookup
)

// decideTrustedFacts implements the exact v4 trusted-fact policy: prefer
// a validated trusted-fact structure already present in the factset;
// otherwise fall back to the compiler's PuppetDB lookup only when the
// target is explicitly configured for it; otherwise report
// unavailability so the caller fails compilation.
func decideTrustedFacts(flatFacts map[string]json.RawMessage, compilerLookupConfigured bool) trustedFactsDecision {
	if raw, ok := flatFacts["trusted"]; ok {
		return trustedFactsDecision{available: true, source: trustedFactsSourceProvided, value: raw}
	}
	if compilerLookupConfigured {
		return trustedFactsDecision{available: true, source: trustedFactsSourceCompilerLookup}
	}
	return trustedFactsDecision{available: false}
}

// v3CatalogAcceptHeader is the Accept header sent with every v3 catalog
// request. `application/json` is one of the catalog indirection's
// supported formats and is the only response encoding this package's
// response decoding handles; text/pson is deliberately not offered. The
// agent's richer `application/vnd.puppet.rich+json` list is deliberately
// not requested either: measured against a deployed compiler, it selects
// only the response Content-Type, not the catalog's rich-data encoding.
// See doc.go's v3 rich-data bullet.
const v3CatalogAcceptHeader = "application/json"

// buildV4Request constructs the POST /puppet/v4/catalog request. See
// doc.go for the documented v4 request-shape assumption.
func buildV4Request(ctx context.Context, client *transport.Client, baseURL *url.URL, certname, environment string, flatFacts map[string]json.RawMessage, decision trustedFactsDecision) (*http.Request, error) {
	body := v4Request{
		Certname:    certname,
		Persistence: v4Persistence{Facts: false, Catalog: false},
		Environment: environment,
		Facts:       v4FactsField{Values: flatFacts},
	}
	if decision.source == trustedFactsSourceProvided {
		body.TrustedFacts = &v4TrustedFactsField{Values: decision.value}
	}
	uuid, err := newTransactionUUID()
	if err != nil {
		return nil, err
	}
	body.TransactionUUID = uuid

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding v4 catalog request: %w", err)
	}

	u := *baseURL
	u.Path = "/puppet/v4/catalog"
	u.RawQuery = ""
	u.Fragment = ""

	req, err := client.NewRequest(ctx, http.MethodPost, u.String(), strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// buildV3Request constructs the POST /puppet/v3/catalog/:certname
// request. See doc.go for the documented v3 request-shape assumption:
// form-encoded body with environment, facts_format, facts (a JSON string
// of {"name", "values"}), and transaction_uuid, plus an explicit Accept
// header, which the v3 endpoint requires (see doc.go's v3 request
// bullet: the request is rejected outright without one).
func buildV3Request(ctx context.Context, client *transport.Client, baseURL *url.URL, certname, environment string, flatFacts map[string]json.RawMessage) (*http.Request, error) {
	factsJSON, err := json.Marshal(v3Facts{Name: certname, Values: flatFacts})
	if err != nil {
		return nil, fmt.Errorf("encoding v3 facts parameter: %w", err)
	}
	uuid, err := newTransactionUUID()
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("environment", environment)
	form.Set("facts_format", "application/json")
	form.Set("facts", string(factsJSON))
	form.Set("transaction_uuid", uuid)

	u := *baseURL
	u.Path = "/puppet/v3/catalog/" + url.PathEscape(certname)
	u.RawQuery = ""
	u.Fragment = ""

	req, err := client.NewRequest(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Non-optional for v3: the endpoint is served by the compiler's
	// embedded Ruby Puppet request handler, which rejects a catalog
	// request carrying no Accept header before it ever compiles
	// ("Missing required Accept header"). The v4 endpoint has no such
	// requirement, which is why only this builder sets it.
	req.Header.Set("Accept", v3CatalogAcceptHeader)
	return req, nil
}
