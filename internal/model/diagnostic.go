package model

// DiagnosticSeverity distinguishes a reported failure from a warning
// that does not by itself change outcome or exit status, such as the v3
// trusted-fact compatibility warning.
type DiagnosticSeverity string

const (
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"
)

// DiagnosticOperation identifies which stage of the pipeline produced a
// Diagnostic: load_facts, load_baseline, request_candidate,
// verify_content, estimate_impact, normalize, configure.
type DiagnosticOperation string

const (
	OperationConfigure        DiagnosticOperation = "configure"
	OperationLoadFacts        DiagnosticOperation = "load_facts"
	OperationLoadBaseline     DiagnosticOperation = "load_baseline"
	OperationRequestCandidate DiagnosticOperation = "request_candidate"
	OperationNormalize        DiagnosticOperation = "normalize"
	OperationVerifyContent    DiagnosticOperation = "verify_content"
	OperationEstimateImpact   DiagnosticOperation = "estimate_impact"
	// OperationSnapshot identifies a local snapshot envelope write, load, or
	// validation failure. Snapshot validation is its own operational-error
	// sub-category, distinct from baseline and fact retrieval: load_facts
	// and load_baseline already cover a live PuppetDB or compiler retrieval
	// failure, but capture's local envelope I/O (temp file, rename and fsync
	// failures, overwrite refusal) and a file-backed source's envelope
	// shape, checksum and identity checks are neither a retrieval failure
	// nor a normalization failure. They need their own category so a
	// diagnostic's Operation field does not mischaracterize which stage
	// failed.
	OperationSnapshot DiagnosticOperation = "snapshot"
	// OperationRequestCandidateTransport identifies a transport-level
	// failure (TLS/connect/timeout/redirect/response-too-large; see
	// internal/transport's ErrorKind) while attempting to reach the
	// compiler for a candidate catalog request. It is deliberately
	// distinct from OperationRequestCandidate.
	//
	// This is internal/compiler's resolution of a classification gap. A
	// compilation failure is a compiler request that is rejected or fails, a
	// candidate identity or environment that does not match, or unmet v4
	// trusted-fact requirements: the compiler was reached and responded, or
	// a policy prerequisite like a trusted-fact source was unmet before even
	// asking, and the outcome is about that response or that policy. But
	// internal/transport's doc.go decision 4 is equally explicit that every
	// *transport.Error (TLS handshake failure, DNS or connect failure,
	// timeout, oversized response, rejected redirect) is an operational
	// error, and that the compiler adapter must not reclassify any Error
	// from that package as a compilation failure. A shared Operation value
	// for both natures would force the outcome reducer to choose one
	// classification for every diagnostic tagged OperationRequestCandidate,
	// misclassifying whichever nature it did not choose.
	//
	// This mirrors OperationSnapshot's own precedent immediately above:
	// same call site, two distinct failure natures, resolved by giving the
	// operational-error nature its own Operation constant rather than
	// overloading one value or adding a new field to Diagnostic. The
	// intended reducer mapping (internal/diff/11) is therefore:
	//
	// 	OperationRequestCandidateTransport -> operational error
	// 	OperationRequestCandidate          -> compilation failure
	OperationRequestCandidateTransport DiagnosticOperation = "request_candidate_transport"
)

// Diagnostic is one target-local or global problem/notice recorded with
// a safe reason and source context. It never carries raw response
// bodies, credentials, private key material, or unredacted sensitive
// values;
type Diagnostic struct {
	Severity  DiagnosticSeverity  `json:"severity"`
	Operation DiagnosticOperation `json:"operation"`
	// Certname is empty for a global (pre-service-call) diagnostic such as
	// invalid target-file configuration.
	Certname string `json:"certname,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}
