package model

// ImpactEstimateStatus is the outcome of one PQL impact query.
type ImpactEstimateStatus string

const (
	ImpactStatusCompleted ImpactEstimateStatus = "completed"
	ImpactStatusTimeout   ImpactEstimateStatus = "timeout"
	ImpactStatusFailed    ImpactEstimateStatus = "failed"
)

// ImpactEstimate is a single potential-impact-estimate result for one
// exact `Type[title]` resource identity. It is always labeled a
// **Potential impact estimate**: it identifies nodes whose latest stored
// catalog contains the changed resource, never proof that those nodes
// would change.
type ImpactEstimate struct {
	Identity ResourceIdentity `json:"identity"`
	// PQL is the exact generated query string used for this estimate.
	PQL string `json:"pql"`
	// Request records the non-PQL request options the query was sent with.
	// The exact generated PQL and its request options are preserved together
	// in the result.
	Request ImpactRequest `json:"request"`
	// ResultLimit is the configured limit; the adapter requests
	// ResultLimit+1 to detect truncation without an extra round trip.
	ResultLimit int `json:"result_limit"`
	// Timeout is the resolved per-query deadline, formatted as a Go
	// duration string (e.g. "10s").
	Timeout string               `json:"timeout"`
	Status  ImpactEstimateStatus `json:"status"`
	// Certnames is the deterministic, locally sorted certname sample.
	// When Truncated is true, it holds exactly ResultLimit certnames.
	Certnames []string `json:"certnames,omitempty"`
	// ResultCount is the number of distinct certnames the bounded query
	// actually returned, at most ResultLimit+1, since that is all the query
	// asked for. It is NOT a total: when Truncated is true, the number of
	// nodes whose latest stored catalog contains the resource is only known
	// to exceed ResultLimit. PIACE never asks PuppetDB for a true total,
	// because truncation detection is fixed at limit+1 and nothing needs a
	// count beyond it.
	ResultCount int  `json:"result_count"`
	Truncated   bool `json:"truncated"`
	// FailureReason is populated only when Status is timeout or failed. It
	// is a safe, non-raw-body diagnostic message.
	FailureReason string `json:"failure_reason,omitempty"`
}

// ImpactRequest records the query scope and bounded request options one
// impact estimate was issued with, so that query scope, result count,
// truncation, timeout and query failures are reported separately. It
// carries no host, credential, or TLS material: the service endpoint's
// authority is already recorded once in the run's configuration
// provenance.
type ImpactRequest struct {
	// Path is the PuppetDB query API path the PQL was sent to.
	Path string `json:"path"`
	// Limit is the value of the `limit` URL parameter actually sent, always
	// ResultLimit+1, so receiving more than ResultLimit rows detects
	// truncation without a second round trip.
	Limit int `json:"limit"`
	// OrderBy is the exact `order_by` URL parameter sent, or empty when
	// server-side ordering was not requested. See internal/impact's
	// doc.go: without it, a truncated sample is not reproducible, because
	// local sorting orders an arbitrary subset deterministically rather
	// than making the subset itself deterministic.
	OrderBy string `json:"order_by,omitempty"`
}
