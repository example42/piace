package model

// DiagnosticSeverity distinguishes a reported failure from a warning that
// does not by itself change outcome/exit status (e.g. the v3 trusted-fact
// compatibility warning; see design.md section 10).
type DiagnosticSeverity string

const (
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"
)

// DiagnosticOperation identifies which stage of the pipeline produced a
// Diagnostic, per design.md's Error Handling section:
// load_facts, load_baseline, request_candidate, verify_content,
// estimate_impact, normalize, configure.
type DiagnosticOperation string

const (
	OperationConfigure        DiagnosticOperation = "configure"
	OperationLoadFacts        DiagnosticOperation = "load_facts"
	OperationLoadBaseline     DiagnosticOperation = "load_baseline"
	OperationRequestCandidate DiagnosticOperation = "request_candidate"
	OperationNormalize        DiagnosticOperation = "normalize"
	OperationVerifyContent    DiagnosticOperation = "verify_content"
	OperationEstimateImpact   DiagnosticOperation = "estimate_impact"
	// OperationSnapshot identifies a local snapshot envelope write, load,
	// or validation failure (task 5). design.md section 10's error
	// taxonomy lists "snapshot validation" as its own operational-error
	// sub-category distinct from "baseline/fact retrieval": load_facts and
	// load_baseline already cover a live PuppetDB/compiler retrieval
	// failure, but capture's local envelope I/O (temp-file/rename/fsync
	// failures, overwrite refusal) and a file-backed source's envelope
	// shape/checksum/identity checks are neither a retrieval failure nor a
	// normalization failure — they need their own category so a
	// diagnostic's Operation field does not mischaracterize which stage
	// failed.
	OperationSnapshot DiagnosticOperation = "snapshot"
	// OperationRequestCandidateTransport identifies a transport-level
	// failure (TLS/connect/timeout/redirect/response-too-large; see
	// internal/transport's ErrorKind) while attempting to reach the
	// compiler for a candidate catalog request. It is deliberately
	// distinct from OperationRequestCandidate.
	//
	// This is task 6's (internal/compiler) resolution of a classification
	// gap design.md leaves implicit: design.md section 10 defines
	// "compilation failure" as "a compiler request is rejected/fails,
	// candidate identity or environment does not match, or v4 trusted-fact
	// requirements are unmet" — i.e. the compiler was reached and
	// responded (or a policy prerequisite like a trusted-fact source was
	// unmet before even asking), and the outcome is about that response
	// or policy. But internal/transport's doc.go decision 4 is equally
	// explicit that every *transport.Error (TLS handshake failure, DNS/
	// connect failure, timeout, oversized response, rejected redirect) is
	// design.md section 10's "operational error" class, and that "task 6's
	// compiler adapter... must not reclassify any Error from this package
	// as a compilation failure." A shared Operation value for both natures
	// would force task 9/11's outcome reducer to choose only one
	// classification for every diagnostic tagged OperationRequestCandidate,
	// misclassifying whichever nature it did not choose.
	//
	// This mirrors OperationSnapshot's own precedent immediately above:
	// same call site, two distinct failure natures, resolved by giving the
	// operational-error nature its own Operation constant rather than
	// overloading one value or adding a new field to Diagnostic. The
	// intended reducer mapping (task 9/11) is therefore:
	//
	//	OperationRequestCandidateTransport -> operational error
	//	OperationRequestCandidate          -> compilation failure
	OperationRequestCandidateTransport DiagnosticOperation = "request_candidate_transport"
)

// Diagnostic is one target-local or global problem/notice recorded with a
// safe reason and source context. It never carries raw response bodies,
// credentials, private key material, or unredacted sensitive values; see
// design.md's Error Handling section and requirements.md 3.5.
type Diagnostic struct {
	Severity  DiagnosticSeverity  `json:"severity"`
	Operation DiagnosticOperation `json:"operation"`
	// Certname is empty for a global (pre-service-call) diagnostic such as
	// invalid target-file configuration.
	Certname string `json:"certname,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}
