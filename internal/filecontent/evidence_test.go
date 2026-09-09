package filecontent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

// sampleContentBytes is the sentinel content string every test in this
// file uses when it needs a File resource's literal content: no test
// output (a returned model.FileContentEvidence, a *model.Diagnostic
// message, or any error string this package produces) may ever contain
// this exact string. assertNoRawContentLeak checks that directly: no
// test may ever find raw content bytes in a returned
// FileContentEvidence, a diagnostic message, or any string this package
// produces.
const sampleContentBytes = "TOP-SECRET-MANAGED-FILE-BYTES-4f8e2a"

func fileParams(overrides map[string]model.Value) map[string]model.Value {
	out := make(map[string]model.Value, len(overrides))
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// assertNoRawContentLeak fails the test if raw appears anywhere in
// evidence's string fields or diag's message.
func assertNoRawContentLeak(t *testing.T, raw string, evidence model.FileContentEvidence, diag *model.Diagnostic) {
	t.Helper()
	fields := []string{
		string(evidence.State),
		string(evidence.EvidenceSource),
		evidence.Algorithm,
		evidence.BeforeDigest,
		evidence.AfterDigest,
	}
	for _, f := range fields {
		if strings.Contains(f, raw) {
			t.Errorf("FileContentEvidence field contains raw content bytes: %q", f)
		}
	}
	if diag != nil && strings.Contains(diag.Message, raw) {
		t.Errorf("diagnostic message contains raw content bytes: %q", diag.Message)
	}
}

// --- Step 1: inline content ---

func TestResolveFileContentEvidence_Step1_InlineContentMatch(t *testing.T) {
	before := fileParams(map[string]model.Value{"content": sampleContentBytes})
	after := fileParams(map[string]model.Value{"content": sampleContentBytes})

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/motd"}, before, after, nil)

	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if evidence.State != model.FileContentUnchanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentUnchanged)
	}
	if evidence.EvidenceSource != model.FileContentEvidenceInline {
		t.Errorf("EvidenceSource = %q, want %q", evidence.EvidenceSource, model.FileContentEvidenceInline)
	}
	if evidence.Algorithm != "sha256" {
		t.Errorf("Algorithm = %q, want sha256", evidence.Algorithm)
	}
	if evidence.BeforeDigest == "" || evidence.BeforeDigest != evidence.AfterDigest {
		t.Errorf("digests = %q/%q, want equal non-empty digests", evidence.BeforeDigest, evidence.AfterDigest)
	}
	assertNoRawContentLeak(t, sampleContentBytes, evidence, diag)
}

func TestResolveFileContentEvidence_Step1_InlineContentMismatch(t *testing.T) {
	before := fileParams(map[string]model.Value{"content": sampleContentBytes})
	after := fileParams(map[string]model.Value{"content": sampleContentBytes + "-modified"})

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/motd"}, before, after, nil)

	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if evidence.State != model.FileContentChanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentChanged)
	}
	if evidence.EvidenceSource != model.FileContentEvidenceInline {
		t.Errorf("EvidenceSource = %q, want %q", evidence.EvidenceSource, model.FileContentEvidenceInline)
	}
	if evidence.BeforeDigest == evidence.AfterDigest {
		t.Errorf("digests are equal, want different: %q", evidence.BeforeDigest)
	}
	assertNoRawContentLeak(t, sampleContentBytes, evidence, diag)
}

// --- Step 2: compiled checksum ---

func TestResolveFileContentEvidence_Step2_ChecksumMatch(t *testing.T) {
	before := fileParams(map[string]model.Value{
		"source":         "puppet:///modules/example/data.txt",
		"checksum":       "sha256",
		"checksum_value": strings.Repeat("a", 64),
	})
	after := fileParams(map[string]model.Value{
		"source":         "puppet:///modules/example/data.txt",
		"checksum":       "sha256",
		"checksum_value": strings.Repeat("a", 64),
	})

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, nil)

	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if evidence.State != model.FileContentUnchanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentUnchanged)
	}
	if evidence.EvidenceSource != model.FileContentEvidenceCompiledChecksum {
		t.Errorf("EvidenceSource = %q, want %q", evidence.EvidenceSource, model.FileContentEvidenceCompiledChecksum)
	}
	if evidence.Algorithm != "sha256" {
		t.Errorf("Algorithm = %q, want sha256", evidence.Algorithm)
	}
}

func TestResolveFileContentEvidence_Step2_ChecksumMismatch(t *testing.T) {
	before := fileParams(map[string]model.Value{
		"source":         "puppet:///modules/example/data.txt",
		"checksum":       "md5",
		"checksum_value": strings.Repeat("a", 32),
	})
	after := fileParams(map[string]model.Value{
		"source":         "puppet:///modules/example/data.txt",
		"checksum":       "md5",
		"checksum_value": strings.Repeat("b", 32),
	})

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, nil)

	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if evidence.State != model.FileContentChanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentChanged)
	}
	if evidence.EvidenceSource != model.FileContentEvidenceCompiledChecksum {
		t.Errorf("EvidenceSource = %q, want %q", evidence.EvidenceSource, model.FileContentEvidenceCompiledChecksum)
	}
}

// TestResolveFileContentEvidence_Step2_UnrecognizedAlgorithmFallsThrough
// verifies mtime/ctime/none and a before/after algorithm mismatch are
// never treated as step 2 evidence (per doc.go), falling through to
// step 3/4 instead. With no retriever and differing source references,
// this lands in step 4a (reference_changed).
func TestResolveFileContentEvidence_Step2_UnrecognizedAlgorithmFallsThrough(t *testing.T) {
	before := fileParams(map[string]model.Value{
		"source":         "puppet:///modules/example/a.txt",
		"checksum":       "mtime",
		"checksum_value": "sametimestamp",
	})
	after := fileParams(map[string]model.Value{
		"source":         "puppet:///modules/example/b.txt",
		"checksum":       "mtime",
		"checksum_value": "sametimestamp",
	})

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, nil)

	if diag == nil {
		t.Fatal("expected a diagnostic (evidence must fall through to step 4), got nil")
	}
	if evidence.State != model.FileContentReferenceChanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentReferenceChanged)
	}
	if evidence.EvidenceSource == model.FileContentEvidenceCompiledChecksum {
		t.Error("EvidenceSource = compiled_checksum, want evidence to have fallen through past step 2")
	}
}

// --- Step 3: successful retrieval-based digest comparison ---

// fakeRetriever is a minimal ContentRetriever test double: reference ->
// (digest, error). It never receives or returns raw content bytes.
type fakeRetriever struct {
	digests map[string]DigestEvidence
	errs    map[string]error
}

func (f *fakeRetriever) Digest(_ context.Context, reference string, _ RetrievalContext) (DigestEvidence, error) {
	if err, ok := f.errs[reference]; ok {
		return DigestEvidence{}, err
	}
	if d, ok := f.digests[reference]; ok {
		return d, nil
	}
	return DigestEvidence{}, errors.New("fakeRetriever: no fixture for reference")
}

func TestResolveFileContentEvidence_Step3_RetrievalSuccess_Unchanged(t *testing.T) {
	before := fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"})
	after := fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"})

	retriever := &fakeRetriever{digests: map[string]DigestEvidence{
		"puppet:///modules/example/data.txt": {Algorithm: "sha256", Digest: hashLocalContent("same-digest-value")},
	}}

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, retriever)

	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if evidence.State != model.FileContentUnchanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentUnchanged)
	}
	if evidence.EvidenceSource != model.FileContentEvidenceCompilerRetrieval {
		t.Errorf("EvidenceSource = %q, want %q", evidence.EvidenceSource, model.FileContentEvidenceCompilerRetrieval)
	}
	if evidence.BeforeDigest != hashLocalContent("same-digest-value") || evidence.AfterDigest != hashLocalContent("same-digest-value") {
		t.Errorf("digests = %q/%q, want same-digest-value/same-digest-value", evidence.BeforeDigest, evidence.AfterDigest)
	}
}

func TestResolveFileContentEvidence_Step3_RetrievalSuccess_Changed(t *testing.T) {
	before := fileParams(map[string]model.Value{"source": "puppet:///modules/example/a.txt"})
	after := fileParams(map[string]model.Value{"source": "puppet:///modules/example/b.txt"})

	retriever := &fakeRetriever{digests: map[string]DigestEvidence{
		"puppet:///modules/example/a.txt": {Algorithm: "sha256", Digest: hashLocalContent("digest-a")},
		"puppet:///modules/example/b.txt": {Algorithm: "sha256", Digest: hashLocalContent("digest-b")},
	}}

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, retriever)

	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if evidence.State != model.FileContentChanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentChanged)
	}
	if evidence.EvidenceSource != model.FileContentEvidenceCompilerRetrieval {
		t.Errorf("EvidenceSource = %q, want %q", evidence.EvidenceSource, model.FileContentEvidenceCompilerRetrieval)
	}
}

// TestResolveFileContentEvidence_Step3_OneSideLiteralOneSideRetrieved
// verifies a side with literal `content` is hashed locally even when the
// other side only has a `source` reference requiring retrieval.
func TestResolveFileContentEvidence_Step3_OneSideLiteralOneSideRetrieved(t *testing.T) {
	before := fileParams(map[string]model.Value{"content": sampleContentBytes})
	after := fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"})

	retriever := &fakeRetriever{digests: map[string]DigestEvidence{
		"puppet:///modules/example/data.txt": {Algorithm: "sha256", Digest: hashLocalContent(sampleContentBytes)},
	}}

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, retriever)

	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if evidence.State != model.FileContentUnchanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentUnchanged)
	}
	assertNoRawContentLeak(t, sampleContentBytes, evidence, diag)
}

// --- Step 4a: reference changed, retrieval unavailable ---

func TestResolveFileContentEvidence_Step4a_ReferenceChangedNoRetriever(t *testing.T) {
	before := fileParams(map[string]model.Value{"source": "puppet:///modules/example/a.txt"})
	after := fileParams(map[string]model.Value{"source": "puppet:///modules/example/b.txt"})

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, nil)

	if diag == nil {
		t.Fatal("expected a diagnostic, got nil")
	}
	if diag.Operation != model.OperationVerifyContent {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationVerifyContent)
	}
	if evidence.State != model.FileContentReferenceChanged {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentReferenceChanged)
	}
	if evidence.Algorithm != "" || evidence.BeforeDigest != "" || evidence.AfterDigest != "" {
		t.Errorf("evidence carries digest fields for an unresolved comparison: %+v", evidence)
	}
}

// --- Step 4b: retrieval failure -> content_indeterminate ---

func TestResolveFileContentEvidence_Step4b_RetrievalFailure(t *testing.T) {
	before := fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"})
	after := fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"})

	retriever := &fakeRetriever{errs: map[string]error{
		"puppet:///modules/example/data.txt": errors.New("simulated network failure"),
	}}

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, retriever)

	if diag == nil {
		t.Fatal("expected a diagnostic, got nil")
	}
	if diag.Operation != model.OperationVerifyContent {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationVerifyContent)
	}
	if evidence.State != model.FileContentIndeterminate {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentIndeterminate)
	}
	if evidence.Algorithm != "" || evidence.BeforeDigest != "" || evidence.AfterDigest != "" {
		t.Errorf("evidence carries digest fields for a failed retrieval: %+v", evidence)
	}
}

// TestResolveFileContentEvidence_Step4b_SameReferenceNoRetriever verifies
// that when references are identical (no visible change) and no
// retriever is configured, the state is content_indeterminate rather
// than reference_changed -- there is no "the reference changed" fact to
// report in that case.
func TestResolveFileContentEvidence_Step4b_SameReferenceNoRetriever(t *testing.T) {
	before := fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"})
	after := fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"})

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
		model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, before, after, nil)

	if diag == nil {
		t.Fatal("expected a diagnostic, got nil")
	}
	if evidence.State != model.FileContentIndeterminate {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentIndeterminate)
	}
}

// TestResolveFileContentEvidence_NeverLeaksContentAcrossAllStates runs a
// broad sweep across every step/state this function can produce and
// asserts sampleContentBytes never appears anywhere in the output.
func TestResolveFileContentEvidence_NeverLeaksContentAcrossAllStates(t *testing.T) {
	cases := []struct {
		name      string
		before    map[string]model.Value
		after     map[string]model.Value
		retriever ContentRetriever
	}{
		{
			name:   "inline_unchanged",
			before: fileParams(map[string]model.Value{"content": sampleContentBytes}),
			after:  fileParams(map[string]model.Value{"content": sampleContentBytes}),
		},
		{
			name:   "inline_changed",
			before: fileParams(map[string]model.Value{"content": sampleContentBytes}),
			after:  fileParams(map[string]model.Value{"content": sampleContentBytes + "-x"}),
		},
		{
			name:   "reference_changed_no_retriever",
			before: fileParams(map[string]model.Value{"source": "puppet:///modules/example/" + sampleContentBytes}),
			after:  fileParams(map[string]model.Value{"source": "puppet:///modules/example/other"}),
		},
		{
			name: "directory_reference_changed",
			before: fileParams(map[string]model.Value{
				"ensure": "directory", "recurse": true,
				"source": "puppet:///modules/example/" + sampleContentBytes + "/",
			}),
			after: fileParams(map[string]model.Value{
				"ensure": "directory", "recurse": true,
				"source": "puppet:///modules/example/other/",
			}),
		},
		{
			name:   "retrieval_failure",
			before: fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"}),
			after:  fileParams(map[string]model.Value{"source": "puppet:///modules/example/data.txt"}),
			retriever: &fakeRetriever{errs: map[string]error{
				"puppet:///modules/example/data.txt": errors.New(sampleContentBytes),
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "production",
				model.ResourceIdentity{Type: "File", Title: "/etc/example.txt"}, tc.before, tc.after, tc.retriever)
			assertNoRawContentLeak(t, sampleContentBytes, evidence, diag)
		})
	}
}

// countingRetriever fails the test if it is ever asked for a digest. A
// directory or recursive File's source names a directory tree the
// compiler's file_content endpoint cannot serve, so resolution must stop
// before any retrieval is attempted.
type countingRetriever struct {
	t     *testing.T
	calls int
}

func (c *countingRetriever) Digest(_ context.Context, reference string, _ RetrievalContext) (DigestEvidence, error) {
	c.t.Helper()
	c.calls++
	c.t.Fatalf("retriever called for a directory/recursive File source %q", reference)
	return DigestEvidence{}, nil
}

func TestResolveFileContentEvidence_DirectoryRecursiveSource_ReferenceChangedWithoutRetrieval(t *testing.T) {
	cases := []struct {
		name           string
		beforeOverride map[string]model.Value
		afterOverride  map[string]model.Value
	}{
		{
			// The exact shape observed in a live comparison: a tp module
			// directory copy whose source lost its trailing slash between
			// the baseline and candidate environments.
			name: "ensure_directory_bool_recurse",
			beforeOverride: map[string]model.Value{
				"ensure": "directory", "recurse": true,
				"source": "puppet:///modules/tp/run_info/",
			},
			afterOverride: map[string]model.Value{
				"ensure": "directory", "recurse": true,
				"source": "puppet:///modules/tp/run_info",
			},
		},
		{
			name: "recurse_remote_without_ensure",
			beforeOverride: map[string]model.Value{
				"recurse": "remote", "source": "puppet:///modules/example/tree/",
			},
			afterOverride: map[string]model.Value{
				"recurse": "remote", "source": "puppet:///modules/example/other/",
			},
		},
		{
			// Only one side is a directory: byte comparison still does not
			// apply, because the two sides are not comparable as files.
			name: "one_side_directory",
			beforeOverride: map[string]model.Value{
				"ensure": "directory", "recurse": true,
				"source": "puppet:///modules/example/tree/",
			},
			afterOverride: map[string]model.Value{
				"source": "puppet:///modules/example/tree/file.txt",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			retriever := &countingRetriever{t: t}
			evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "upstream",
				model.ResourceIdentity{Type: "File", Title: "info scripts"},
				fileParams(tc.beforeOverride), fileParams(tc.afterOverride), retriever)

			if retriever.calls != 0 {
				t.Fatalf("retriever calls = %d, want 0", retriever.calls)
			}
			if evidence.State != model.FileContentReferenceChanged {
				t.Errorf("State = %q, want %q", evidence.State, model.FileContentReferenceChanged)
			}
			if evidence.Algorithm != "" || evidence.BeforeDigest != "" || evidence.AfterDigest != "" {
				t.Errorf("digest fields must stay empty, got %+v", evidence)
			}
			if diag == nil {
				t.Fatal("diagnostic = nil, want a verify_content diagnostic")
			}
			if diag.Severity != model.SeverityWarning {
				t.Errorf("Severity = %q, want %q", diag.Severity, model.SeverityWarning)
			}
			if diag.Operation != model.OperationVerifyContent {
				t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationVerifyContent)
			}
			// A warning must not turn the target into an operational error.
			if _, contributes := model.OutcomeForDiagnostic(*diag); contributes {
				t.Error("a not-applicable content comparison must not contribute to the outcome")
			}
		})
	}
}

func TestResolveFileContentEvidence_DirectoryUnchangedSource_Indeterminate(t *testing.T) {
	retriever := &countingRetriever{t: t}
	before := fileParams(map[string]model.Value{
		"ensure": "directory", "recurse": true,
		"source": "puppet:///modules/tp/run_info/", "checksum_value": "abc",
	})
	after := fileParams(map[string]model.Value{
		"ensure": "directory", "recurse": true,
		"source": "puppet:///modules/tp/run_info/",
	})

	evidence, diag := resolveEvidenceTest(context.Background(), "web-01", "upstream",
		model.ResourceIdentity{Type: "File", Title: "info scripts"}, before, after, retriever)

	if retriever.calls != 0 {
		t.Fatalf("retriever calls = %d, want 0", retriever.calls)
	}
	if evidence.State != model.FileContentIndeterminate {
		t.Errorf("State = %q, want %q", evidence.State, model.FileContentIndeterminate)
	}
	if diag == nil || diag.Severity != model.SeverityError {
		t.Fatalf("want an error-severity diagnostic, got %+v", diag)
	}
}

func TestIsDirectoryOrRecursive(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]model.Value
		want   bool
	}{
		{"plain file", map[string]model.Value{"source": "puppet:///modules/example/a.txt"}, false},
		{"ensure file", map[string]model.Value{"ensure": "file"}, false},
		{"ensure Directory mixed case", map[string]model.Value{"ensure": "Directory"}, true},
		{"recurse bool true", map[string]model.Value{"recurse": true}, true},
		{"recurse bool false", map[string]model.Value{"recurse": false}, false},
		{"recurse string true", map[string]model.Value{"recurse": "true"}, true},
		{"recurse string false", map[string]model.Value{"recurse": "false"}, false},
		{"recurse remote", map[string]model.Value{"recurse": "remote"}, true},
		{"recurse inf", map[string]model.Value{"recurse": "inf"}, true},
		{"recurse depth 0", map[string]model.Value{"recurse": model.Number("0")}, false},
		{"recurse depth 2", map[string]model.Value{"recurse": model.Number("2")}, true},
		{"nil params", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDirectoryOrRecursive(tc.params); got != tc.want {
				t.Errorf("isDirectoryOrRecursive = %v, want %v", got, tc.want)
			}
		})
	}
}

func resolveEvidenceTest(ctx context.Context, certname, environment string, identity model.ResourceIdentity, before, after map[string]model.Value, retriever ContentRetriever) (model.FileContentEvidence, *model.Diagnostic) {
	c := model.ContentContext{Source: "compiler", Environment: environment}
	return ResolveFileContentEvidence(ctx, certname, identity, Side{Resource: model.Resource{Identity: identity, Parameters: before}, Context: c}, Side{Resource: model.Resource{Identity: identity, Parameters: after}, Context: c}, retriever)
}
