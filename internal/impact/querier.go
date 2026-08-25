package impact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/transport"
)

// queryPath is the PuppetDB root query endpoint path. See doc.go for why
// design.md section 8's PQL text goes here rather than to
// /pdb/query/v4/resources.
const queryPath = "/pdb/query/v4"

// certnameOrderBy is the exact `order_by` URL parameter value sent with
// every estimate query. See doc.go: without server-side ordering a
// truncated sample is not reproducible.
const certnameOrderBy = `[{"field":"certname","order":"asc"}]`

// Limits is the bounded request policy for one estimate, per
// requirements.md 9.5. It deliberately omits resolve.ImpactEstimate's
// Enabled flag: whether to estimate at all is the caller's gate (see
// EstimateAll), not something a querier should be able to ignore.
type Limits struct {
	Timeout     time.Duration
	ResultLimit int
}

// LimitsFrom projects a resolved per-target impact policy into Limits.
func LimitsFrom(cfg resolve.ImpactEstimate) Limits {
	return Limits{Timeout: cfg.Timeout, ResultLimit: cfg.ResultLimit}
}

// ImpactQuerier is design.md's named
// `ImpactQuerier.Estimate(resourceIdentity, limits) -> ImpactEstimate`
// interface. An implementation returns a fully populated
// model.ImpactEstimate for every call — including on failure, where
// Status carries timeout or failed and the accompanying diagnostic
// carries the safe reason — so a caller never has to synthesize a
// placeholder estimate of its own.
type ImpactQuerier interface {
	Estimate(ctx context.Context, identity model.ResourceIdentity, limits Limits) (model.ImpactEstimate, *model.Diagnostic)
}

// Querier is the PuppetDB-backed ImpactQuerier. It wraps a
// *transport.Client already built (by task 3) from the resolved PuppetDB
// resolve.Endpoint, mirroring internal/puppetdb.NewAdapter and
// internal/filecontent.NewCompilerContentResolver.
type Querier struct {
	client  *transport.Client
	baseURL *url.URL
}

// NewQuerier builds a Querier issuing requests against endpoint using
// client.
func NewQuerier(client *transport.Client, endpoint *url.URL) *Querier {
	return &Querier{client: client, baseURL: endpoint}
}

// Estimate implements ImpactQuerier for one exact `Type[title]`.
func (q *Querier) Estimate(ctx context.Context, identity model.ResourceIdentity, limits Limits) (model.ImpactEstimate, *model.Diagnostic) {
	estimate := model.ImpactEstimate{
		Identity:    identity,
		ResultLimit: limits.ResultLimit,
		Timeout:     limits.Timeout.String(),
		Request: model.ImpactRequest{
			Path:    queryPath,
			Limit:   limits.ResultLimit + 1,
			OrderBy: certnameOrderBy,
		},
	}

	pql, err := BuildPQL(identity)
	if err != nil {
		return failed(estimate, identity, err.Error())
	}
	estimate.PQL = pql

	u := *q.baseURL
	u.Path = queryPath
	params := url.Values{}
	params.Set("query", pql)
	params.Set("limit", fmt.Sprintf("%d", estimate.Request.Limit))
	params.Set("order_by", certnameOrderBy)
	u.RawQuery = params.Encode()
	u.Fragment = ""

	req, err := q.client.NewRequest(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return failed(estimate, identity, "building impact estimate request: "+transport.SafeMessage(err))
	}

	resp, err := q.client.Do(req, limits.Timeout)
	if err != nil {
		var te *transport.Error
		if errors.As(err, &te) {
			if te.Kind == transport.KindTimeout {
				estimate.Status = model.ImpactStatusTimeout
				estimate.FailureReason = "impact estimate query exceeded its " + estimate.Timeout + " deadline"
				return estimate, estimateDiagnostic(identity, estimate.FailureReason)
			}
			return failed(estimate, identity, "impact estimate query failed: "+te.Message)
		}
		return failed(estimate, identity, "impact estimate query failed: "+transport.SafeMessage(err))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The response body is deliberately never echoed: design.md's
		// Error Handling section ("They do not preserve raw body text by
		// default, because service errors can echo values") applies here
		// exactly as it does to every other adapter, and a rejected PQL
		// query's error text can quote the query — which embeds the
		// resource title.
		return failed(estimate, identity,
			fmt.Sprintf("PuppetDB returned a non-2xx status (%d) for the impact estimate query", resp.StatusCode))
	}

	certnames, err := parseCertnames(resp.Body)
	if err != nil {
		return failed(estimate, identity, err.Error())
	}

	sort.Strings(certnames)
	estimate.ResultCount = len(certnames)
	if len(certnames) > limits.ResultLimit {
		estimate.Truncated = true
		certnames = certnames[:limits.ResultLimit]
	}
	estimate.Certnames = certnames
	estimate.Status = model.ImpactStatusCompleted
	return estimate, nil
}

// certnameRow is the single projected column design.md section 8's PQL
// selects. Any other field PuppetDB may include is ignored rather than
// rejected: the projection fixes what PIACE relies on, and tolerating
// extra fields keeps a future PuppetDB addition from failing an estimate.
type certnameRow struct {
	// Certname is a pointer so an absent `certname` key and an
	// explicitly empty one stay distinguishable: encoding/json decodes a
	// missing key and `""` to the same empty string, and those are
	// different wire-level failures that deserve different diagnostics.
	Certname *string `json:"certname"`
}

// parseCertnames decodes the query response into a deduplicated certname
// slice. A body that is not a JSON array of objects, or a row with no
// certname, is a malformed response rather than an empty result — per
// design.md's Components and Interfaces section, "unknown or malformed
// ... data is an operational normalization failure, never an empty
// catalog or factset". The raw body never reaches the returned error.
func parseCertnames(body []byte) ([]string, error) {
	var rows []certnameRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, errors.New("impact estimate response was not a JSON array of certname rows")
	}
	seen := make(map[string]bool, len(rows))
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Certname == nil {
			return nil, errors.New("impact estimate response contained a row without a certname field")
		}
		if *row.Certname == "" {
			return nil, errors.New("impact estimate response contained a row with an empty certname")
		}
		if seen[*row.Certname] {
			continue
		}
		seen[*row.Certname] = true
		out = append(out, *row.Certname)
	}
	return out, nil
}

// failed stamps a failed status and reason onto estimate and pairs it
// with the matching diagnostic.
func failed(estimate model.ImpactEstimate, identity model.ResourceIdentity, reason string) (model.ImpactEstimate, *model.Diagnostic) {
	estimate.Status = model.ImpactStatusFailed
	estimate.FailureReason = reason
	return estimate, estimateDiagnostic(identity, reason)
}

// estimateDiagnostic builds the error-severity diagnostic that makes an
// enabled-but-failed estimate contribute an operational outcome, per
// design.md section 8. It is not certname-scoped: an estimate is a
// run-level query about one resource identity, not about one target (see
// EstimateAll), so Certname is left empty and the identity travels in
// Source.
func estimateDiagnostic(identity model.ResourceIdentity, reason string) *model.Diagnostic {
	return &model.Diagnostic{
		Severity:  model.SeverityError,
		Operation: model.OperationEstimateImpact,
		Source:    identity.String(),
		Message:   reason,
	}
}

var _ ImpactQuerier = (*Querier)(nil)
