package compiler

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
	"github.com/example42/piace/internal/transport"
)

// Adapter is the v3/v4 compiler-backed implementation of
// capture.CompilerCatalogRequester (see internal/capture/compiler.go for
// the interface contract this type satisfies). It wraps a
// *transport.Client already built (by internal/transport) from the
// resolved compiler resolve.Endpoint, and issues only the documented
// catalog-compilation POST requests described in doc.go — never a
// PuppetDB request of any kind (see doc.go's "Scope" section).
type Adapter struct {
	client  *transport.Client
	baseURL *url.URL
}

// NewAdapter builds an Adapter that issues requests against endpoint
// using client, mirroring internal/puppetdb.NewAdapter's construction
// pattern: endpoint is threaded through separately because
// *transport.Client does not retain the full endpoint URL, only its
// authority.
func NewAdapter(client *transport.Client, endpoint *url.URL) *Adapter {
	return &Adapter{client: client, baseURL: endpoint}
}

// RequestCandidate implements capture.CompilerCatalogRequester. It
// requests target's candidate catalog for its configured candidate
// environment using facts as the target's input factset, applying
// design.md section 5's exact v4 trusted-fact policy, v4-to-v3 fallback
// conditions, and v3 warning rule. It is reused identically by `capture
// catalog` (already wired against this interface by task 5) and by the
// future `compare` command, per design.md's "Capture catalog uses the
// exact same adapter and policy as comparison."
func (a *Adapter) RequestCandidate(ctx context.Context, target resolve.Target, facts puppetdb.Factset) (puppetdb.Catalog, model.CandidateProvenance, []string, *model.Diagnostic) {
	host := a.client.Host()

	flatFacts, err := flattenFacts(facts.Facts)
	if err != nil {
		diag := operationalLocalDiagnostic(target.Certname, host,
			"malformed or unparseable input factset: cannot build a candidate catalog request")
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, &diag
	}

	factSourceKind := model.SourceKindPuppetDB
	if target.Facts.Source == config.FactSourceFile {
		factSourceKind = model.SourceKindFile
	}
	factsetIdentity := facts.Hash

	baseProvenance := model.CandidateProvenance{
		RequestedAPI:    target.Candidate.CatalogAPI,
		Environment:     target.Candidate.Environment,
		FactSource:      factSourceKind,
		FactsetIdentity: factsetIdentity,
	}

	switch target.Candidate.CatalogAPI {
	case config.CatalogAPIv3:
		cat, prov, diag := a.requestV3(ctx, target, flatFacts, baseProvenance)
		return cat, prov, nil, diag
	case config.CatalogAPIv4:
		return a.requestV4WithFallback(ctx, target, flatFacts, baseProvenance)
	default:
		diag := operationalLocalDiagnostic(target.Certname, host,
			"target has an unsupported candidate.catalog_api value; this should have been rejected during configuration resolution")
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, &diag
	}
}

// requestV3 issues one v3 candidate catalog request and applies the
// non-suppressible v3 warning unconditionally, per requirements.md
// 2.5-2.6 and design.md section 5 ("For API v3... PIACE attaches a
// prominent, non-suppressible warning").
func (a *Adapter) requestV3(ctx context.Context, target resolve.Target, flatFacts map[string]json.RawMessage, base model.CandidateProvenance) (puppetdb.Catalog, model.CandidateProvenance, *model.Diagnostic) {
	req, err := buildV3Request(ctx, a.client, a.baseURL, target.Certname, target.Candidate.Environment, flatFacts)
	if err != nil {
		diag := operationalLocalDiagnostic(target.Certname, a.client.Host(), "building v3 candidate catalog request")
		return puppetdb.Catalog{}, model.CandidateProvenance{}, &diag
	}

	resp, err := a.client.Do(req, 0)
	if err != nil {
		diag := diagnosticFromTransportError(target.Certname, err)
		return puppetdb.Catalog{}, model.CandidateProvenance{}, &diag
	}

	cat, diag := processResponse(resp, a.client.Host(), target.Certname, target.Candidate.Environment, config.CatalogAPIv3)
	if diag != nil {
		return puppetdb.Catalog{}, model.CandidateProvenance{}, diag
	}

	prov := base
	prov.EffectiveAPI = config.CatalogAPIv3
	prov.V3Warning = model.V3TrustedFactWarning
	return cat, prov, nil
}

// requestV4WithFallback issues one v4 candidate catalog request,
// enforcing design.md section 5's trusted-fact policy first, then applies
// the v4-to-v3 fallback decision on the response per design.md section
// 3.1: fallback happens only when target.Candidate.AllowV3Fallback is
// true AND the v4 response is a verified-unsupported response
// (isVerifiedUnsupportedV4); it never happens for authentication,
// authorization, timeout, malformed response, or candidate identity/
// environment mismatch.
func (a *Adapter) requestV4WithFallback(ctx context.Context, target resolve.Target, flatFacts map[string]json.RawMessage, base model.CandidateProvenance) (puppetdb.Catalog, model.CandidateProvenance, []string, *model.Diagnostic) {
	host := a.client.Host()

	decision := decideTrustedFacts(flatFacts, target.Candidate.TrustedFactsCompilerLookup)
	if !decision.available {
		diag := compilationFailureDiagnosticNoResponse(target.Certname, host,
			"v4 candidate request requires target trusted facts, but the input factset has no valid "+
				"trusted-fact structure and the compiler is not configured for a PuppetDB trusted-fact lookup "+
				"(trusted_facts_compiler_lookup); PIACE fails compilation rather than inventing trusted facts")
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, &diag
	}

	req, err := buildV4Request(ctx, a.client, a.baseURL, target.Certname, target.Candidate.Environment, flatFacts, decision)
	if err != nil {
		diag := operationalLocalDiagnostic(target.Certname, host, "building v4 candidate catalog request")
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, &diag
	}

	resp, err := a.client.Do(req, 0)
	if err != nil {
		// A transport-level failure (TLS/connect/timeout/etc.) never
		// triggers fallback: design.md section 3.1 explicitly excludes
		// timeout, and there is no HTTP response at all here to classify
		// as "verified unsupported" in the first place.
		diag := diagnosticFromTransportError(target.Certname, err)
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, &diag
	}

	if target.Candidate.AllowV3Fallback && isVerifiedUnsupportedV4(resp.StatusCode) {
		cat, prov, diag := a.requestV3(ctx, target, flatFacts, base)
		if diag != nil {
			return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, diag
		}
		prov.RequestedAPI = config.CatalogAPIv4
		prov.FellBackFromV4 = true
		return cat, prov, nil, nil
	}

	cat, diag := processResponse(resp, host, target.Certname, target.Candidate.Environment, config.CatalogAPIv4)
	if diag != nil {
		return puppetdb.Catalog{}, model.CandidateProvenance{}, nil, diag
	}

	prov := base
	prov.EffectiveAPI = config.CatalogAPIv4
	switch decision.source {
	case trustedFactsSourceProvided:
		prov.TrustedFactsSource = model.TrustedFactsProvided
	case trustedFactsSourceCompilerLookup:
		prov.TrustedFactsSource = model.TrustedFactsCompilerLookup
	}
	return cat, prov, nil, nil
}

var _ interface {
	RequestCandidate(ctx context.Context, target resolve.Target, facts puppetdb.Factset) (puppetdb.Catalog, model.CandidateProvenance, []string, *model.Diagnostic)
} = (*Adapter)(nil)
