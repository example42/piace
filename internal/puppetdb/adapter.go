package puppetdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/transport"
)

// Adapter is the PuppetDB-backed implementation of FactSource and
// CatalogSource. It wraps a *transport.Client already built (by task 3's
// package) from the resolved PuppetDB resolve.Endpoint, and issues only
// GET requests against PuppetDB's v4 query API — see doc.go's "Scope"
// section for why no write/command-endpoint call is possible from this
// package.
type Adapter struct {
	client  *transport.Client
	baseURL *url.URL
}

// NewAdapter builds an Adapter that issues requests against endpoint using
// client. endpoint is normally the same resolve.Endpoint.URL used to
// construct client via transport.NewClient; it is threaded through
// separately here because *transport.Client does not retain the full
// endpoint URL, only its authority (see transport.Client.Host).
func NewAdapter(client *transport.Client, endpoint *url.URL) *Adapter {
	return &Adapter{client: client, baseURL: endpoint}
}

// queryURL builds the PuppetDB v4 query URL for the given
// factsets/catalogs segment and certname, per the documented paths
// /pdb/query/v4/factsets/<NODE> and /pdb/query/v4/catalogs/<NODE> (see
// doc.go). certname is path-escaped defensively even though
// internal/config/resolve already rejects certnames containing `/`, `\`,
// NUL, or `..` before this package is ever reached.
func (a *Adapter) queryURL(segment, certname string) string {
	u := *a.baseURL
	u.Path = "/pdb/query/v4/" + segment + "/" + url.PathEscape(certname)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// get issues one GET request for segment/certname using the wrapped
// transport.Client, which applies its configured deadline, response-size
// bound, and redaction-safe error classification. A zero timeout requests
// the client's own configured default (see transport.Client.Do).
func (a *Adapter) get(ctx context.Context, segment, certname string) (*transport.Response, error) {
	req, err := a.client.NewRequest(ctx, http.MethodGet, a.queryURL(segment, certname), nil)
	if err != nil {
		return nil, err
	}
	return a.client.Do(req, 0)
}

// diagnosticFromTransportError builds a model.Diagnostic from a transport-
// layer error (connection/TLS/timeout/redirect/size failures — see
// transport.Error), preferring the already-safe Kind/Host/Message fields
// of a classified *transport.Error and falling back to
// transport.SafeMessage for any other error reaching this package.
func diagnosticFromTransportError(op model.DiagnosticOperation, certname string, err error) model.Diagnostic {
	var te *transport.Error
	if errors.As(err, &te) {
		return transport.Diagnostic(op, certname, transport.Summary{Host: te.Host}, te.Message)
	}
	return transport.Diagnostic(op, certname, transport.Summary{}, transport.SafeMessage(err))
}

// notFoundOrMalformedDiagnostic builds a diagnostic for a non-2xx response,
// a not-found PuppetDB error body, or malformed/unparseable JSON. It never
// includes raw response body text (design.md's Error Handling section:
// "They do not preserve raw body text by default, because service errors
// can echo values") — only a fixed, safe classification string plus safe
// transport.Summary metadata.
func notFoundOrMalformedDiagnostic(op model.DiagnosticOperation, certname, host string, statusCode int, reason string) model.Diagnostic {
	return transport.Diagnostic(op, certname, transport.Summary{Host: host, StatusCode: statusCode}, reason)
}

// factsetProbe/catalogProbe are decoded first to detect PuppetDB's
// documented not-found/error body shape ({"error": "<message>"}) and the
// defensive empty-certname case, without ever routing the raw "error"
// message text into a Diagnostic (see doc.go).
type probeBody struct {
	Error    string `json:"error"`
	Certname string `json:"certname"`
}

// parseFactset decodes body into a Factset. notFound is true when body
// decodes as valid JSON but matches PuppetDB's documented not-found/error
// shape or lacks a certname; decodeErr is non-nil only for JSON that fails
// to parse at all (malformed/unparseable response).
func parseFactset(body []byte) (fs Factset, notFound bool, decodeErr error) {
	var probe probeBody
	if err := json.Unmarshal(body, &probe); err != nil {
		return Factset{}, false, err
	}
	if probe.Error != "" || probe.Certname == "" {
		return Factset{}, true, nil
	}
	if err := json.Unmarshal(body, &fs); err != nil {
		return Factset{}, false, err
	}
	return fs, false, nil
}

// parseCatalog decodes body into a Catalog. See parseFactset for the
// notFound/decodeErr contract.
func parseCatalog(body []byte) (cat Catalog, notFound bool, decodeErr error) {
	var probe probeBody
	if err := json.Unmarshal(body, &probe); err != nil {
		return Catalog{}, false, err
	}
	if probe.Error != "" || probe.Certname == "" {
		return Catalog{}, true, nil
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		return Catalog{}, false, err
	}
	return cat, false, nil
}

// Load implements FactSource. It retrieves target's latest factset from
// PuppetDB's /pdb/query/v4/factsets/<NODE> endpoint (GET only; see doc.go).
func (a *Adapter) Load(ctx context.Context, target resolve.Target) (Factset, model.SourceProvenance, *model.Diagnostic) {
	if target.Facts.Source != config.FactSourcePuppetDB {
		diag := transport.Diagnostic(model.OperationLoadFacts, target.Certname, transport.Summary{},
			fmt.Sprintf("puppetdb fact-source adapter invoked for a target whose facts.source is %q, not %q",
				target.Facts.Source, config.FactSourcePuppetDB))
		return Factset{}, model.SourceProvenance{}, &diag
	}

	resp, err := a.get(ctx, "factsets", target.Certname)
	if err != nil {
		diag := diagnosticFromTransportError(model.OperationLoadFacts, target.Certname, err)
		return Factset{}, model.SourceProvenance{}, &diag
	}

	host := a.client.Host()
	if resp.StatusCode != http.StatusOK {
		diag := notFoundOrMalformedDiagnostic(model.OperationLoadFacts, target.Certname, host, resp.StatusCode,
			"puppetdb returned a non-200 status retrieving the factset")
		return Factset{}, model.SourceProvenance{}, &diag
	}

	fs, notFound, decodeErr := parseFactset(resp.Body)
	if decodeErr != nil {
		diag := notFoundOrMalformedDiagnostic(model.OperationLoadFacts, target.Certname, host, resp.StatusCode,
			"malformed or unparseable puppetdb factset response")
		return Factset{}, model.SourceProvenance{}, &diag
	}
	if notFound {
		diag := notFoundOrMalformedDiagnostic(model.OperationLoadFacts, target.Certname, host, resp.StatusCode,
			"no factset found in puppetdb for certname")
		return Factset{}, model.SourceProvenance{}, &diag
	}

	prov := model.SourceProvenance{
		Kind:              model.SourceKindPuppetDB,
		Certname:          fs.Certname,
		Environment:       fs.Environment,
		ProducerTimestamp: fs.ProducerTimestamp,
		CatalogIdentity:   fs.Hash,
		Producer:          fs.Producer,
	}
	return fs, prov, nil
}

// LoadBaseline implements CatalogSource. It retrieves target's latest
// catalog from PuppetDB's /pdb/query/v4/catalogs/<NODE> endpoint (GET
// only; see doc.go), then enforces requirements.md 1.3's baseline-
// environment rejection rule before the catalog is returned as usable
// (see doc.go's "Baseline-environment-mismatch resolution" section for why
// this check applies unconditionally to baseline.source == puppetdb).
func (a *Adapter) LoadBaseline(ctx context.Context, target resolve.Target) (Catalog, model.SourceProvenance, *model.Diagnostic) {
	if target.Baseline.Source != config.BaselineSourcePuppetDB {
		diag := transport.Diagnostic(model.OperationLoadBaseline, target.Certname, transport.Summary{},
			fmt.Sprintf("puppetdb baseline-source adapter invoked for a target whose baseline.source is %q, not %q",
				target.Baseline.Source, config.BaselineSourcePuppetDB))
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	resp, err := a.get(ctx, "catalogs", target.Certname)
	if err != nil {
		diag := diagnosticFromTransportError(model.OperationLoadBaseline, target.Certname, err)
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	host := a.client.Host()
	if resp.StatusCode != http.StatusOK {
		diag := notFoundOrMalformedDiagnostic(model.OperationLoadBaseline, target.Certname, host, resp.StatusCode,
			"puppetdb returned a non-200 status retrieving the baseline catalog")
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	cat, notFound, decodeErr := parseCatalog(resp.Body)
	if decodeErr != nil {
		diag := notFoundOrMalformedDiagnostic(model.OperationLoadBaseline, target.Certname, host, resp.StatusCode,
			"malformed or unparseable puppetdb catalog response")
		return Catalog{}, model.SourceProvenance{}, &diag
	}
	if notFound {
		diag := notFoundOrMalformedDiagnostic(model.OperationLoadBaseline, target.Certname, host, resp.StatusCode,
			"no catalog found in puppetdb for certname")
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	if cat.Environment != target.Baseline.Environment {
		diag := notFoundOrMalformedDiagnostic(model.OperationLoadBaseline, target.Certname, host, resp.StatusCode,
			fmt.Sprintf("puppetdb baseline catalog environment %q does not match the configured baseline environment %q",
				cat.Environment, target.Baseline.Environment))
		return Catalog{}, model.SourceProvenance{}, &diag
	}

	prov := model.SourceProvenance{
		Kind:              model.SourceKindPuppetDB,
		Certname:          cat.Certname,
		Environment:       cat.Environment,
		ProducerTimestamp: cat.ProducerTimestamp,
		CatalogIdentity:   cat.Hash,
		Producer:          cat.Producer,
	}
	return cat, prov, nil
}

// Ensure Adapter satisfies both adapter interfaces at compile time.
var (
	_ FactSource    = (*Adapter)(nil)
	_ CatalogSource = (*Adapter)(nil)
)
