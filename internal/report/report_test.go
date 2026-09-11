package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/model"
)

func TestSampleResult_ReducesAsExpected(t *testing.T) {
	if got := sampleResult().Outcome; got != wantOutcome {
		t.Fatalf("Outcome = %q, want %q", got, wantOutcome)
	}
}

// TestJSON_IsValidAndCarriesTheSchemaVersion checks the JSON artifact
// parses and keeps its versioned envelope.
func TestJSON_IsValidAndCarriesTheSchemaVersion(t *testing.T) {
	data, err := JSON(sampleResult())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded model.Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.SchemaVersion != model.ResultSchemaVersion {
		t.Errorf("SchemaVersion = %d", decoded.SchemaVersion)
	}
	if decoded.ExitCode != 30 || decoded.Outcome != wantOutcome {
		t.Errorf("Outcome/ExitCode = %q/%d", decoded.Outcome, decoded.ExitCode)
	}
	if len(decoded.Targets) != 2 || len(decoded.Aggregate.Groups) != 2 || len(decoded.ImpactEstimates) != 2 {
		t.Errorf("document is missing sections: %d targets, %d groups, %d estimates",
			len(decoded.Targets), len(decoded.Aggregate.Groups), len(decoded.ImpactEstimates))
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("JSON artifact does not end with a single trailing newline")
	}
}

// TestJSON_IsByteIdenticalForIdenticalInput applies the determinism
// property to the JSON artifact. Rendering the same document twice must
// produce the same bytes despite the map fields in ConfigProvenance,
// whose Go iteration order is randomized.
func TestJSON_IsByteIdenticalForIdenticalInput(t *testing.T) {
	first, err := JSON(sampleResult())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := JSON(sampleResult())
		if err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if string(next) != string(first) {
			t.Fatalf("render %d differs from the first render", i)
		}
	}
}

// TestJSON_CanonicalizesNumberSpelling verifies the canonical encoder is
// actually in the path: two spellings of the same number must produce
// identical bytes.
func TestJSON_CanonicalizesNumberSpelling(t *testing.T) {
	render := func(number model.Number) string {
		r := model.NewResult("test", "2026-08-25T12:00:00Z")
		r.Targets = []model.TargetResult{{
			Certname: "a",
			Config:   &model.ConfigProvenance{},
			NodeDiff: &model.NodeDiff{
				HasDifference: true,
				ResourceChanges: []model.ResourceChange{{
					Kind:      model.ChangeParameterChanged,
					Identity:  model.ResourceIdentity{Type: "Notify", Title: "n"},
					Parameter: "port", Before: model.Number("1"), After: number,
				}},
			},
		}}
		r.Reduce()
		data, err := JSON(r)
		if err != nil {
			t.Fatalf("JSON: %v", err)
		}
		return string(data)
	}
	if render(model.Number("1.50")) != render(model.Number("1.5")) {
		t.Error(`"1.50" and "1.5" rendered differently`)
	}
}

// TestText_SectionOrder locks the fixed text order: final outcome first,
// then per-target status, node changes, warnings and errors, aggregate
// summary, impact summary.
func TestText_SectionOrder(t *testing.T) {
	data, err := Text(sampleResult(), nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	previous := -1
	for _, marker := range []string{"outcome:", "targets (", "aggregate diff (", ImpactEstimateLabel} {
		index := strings.Index(out, marker)
		if index < 0 {
			t.Fatalf("text report is missing %q", marker)
		}
		if index <= previous {
			t.Errorf("%q appears out of order", marker)
		}
		previous = index
	}
	if !strings.HasPrefix(out, "PIACE test (2026-08-25T12:00:00Z)\noutcome: operational_error (exit 30)\n") {
		t.Errorf("text report does not open with the outcome")
	}
}

// TestText_RequiredContent checks the elements that have to be visible
// in the CI log. The PQL is deliberately not among them: it is
// discharged by the JSON report, see TestJSON_KeepsWhatTextAndHTMLOmit.
func TestText_RequiredContent(t *testing.T) {
	data, err := Text(sampleResult(), nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	for _, want := range []string{
		"reason:",                   // what drove the outcome
		model.V3TrustedFactWarning,  // the non-suppressible v3 warning
		"Package[*]: 2 resource(s)", // an exclusion and what it suppressed
		ImpactEstimateLabel,         // the estimate's fixed label
		ImpactEstimateNote,          // and the note that qualifies it
		"truncated",                 // an over-limit estimate says so
		"result_limit 2",            // and names the bound, not just the state
		"ERROR [load_baseline]",     // a retrieval failure, with its operation
		`~ Service[nginx] ensure: "stopped" -> "running"`,
		"~ File[/etc/motd] content: changed (via inline_content) sha256 aaaa -> bbbb",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text report is missing %q\n---\n%s", want, out)
		}
	}
}

// TestText_OmitsEdgesAndQueryMechanics locks the display policy the two
// human-facing formats apply (see doc.go). These strings are not merely
// absent by accident: each one was previously rendered, and each is the
// bulk that buried the resource changes a reviewer reads a CI log for.
func TestText_OmitsEdgesAndQueryMechanics(t *testing.T) {
	data, err := Text(sampleResult(), nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	for _, forbidden := range []string{
		"+edge Class[a] -> Class[b]", // per-target edge change
		"edge_added Class[a]",        // aggregate edge group
		"edge(s) suppressed",         // exclusion edge count
		"resources[certname]",        // an estimate's PQL
		"/pdb/query/v4",              // an estimate's request path
		"order_by",                   // an estimate's request options
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("text report still shows %q\n---\n%s", forbidden, out)
		}
	}
}

// TestText_CountsOnlyWhatItPrints guards the header/body agreement that
// keeps a section from promising lines it does not show. sampleResult has
// four resource changes and one edge change on its first target, and two
// aggregate groups of which one is an edge group.
func TestText_CountsOnlyWhatItPrints(t *testing.T) {
	data, err := Text(sampleResult(), nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	if !strings.Contains(out, "changes (4):") {
		t.Errorf("per-target change count does not match the displayed lines\n---\n%s", out)
	}
	if !strings.Contains(out, "aggregate diff (1):") {
		t.Errorf("aggregate count still includes the filtered edge group\n---\n%s", out)
	}
}

// TestText_AggregateLineIsUnambiguous locks the shape of an aggregate
// line. The count is not bracketed, because a bracketed number directly
// after a bracketed resource title reads as a second resource reference,
// and there is exactly one structural colon separating the change from
// its targets, so the parameter name is not followed by one.
func TestText_AggregateLineIsUnambiguous(t *testing.T) {
	data, err := Text(sampleResult(), nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	const want = `  parameter_changed Service[nginx] ensure "stopped" -> "running": 1 target: web-01.example.test` + "\n"
	if !strings.Contains(out, want) {
		t.Errorf("aggregate line shape changed:\nwant %q\n---\n%s", want, out)
	}
}

// TestText_EdgeOnlyTargetIsNotReportedAsUnchanged is the
// report/exit-code contract. A target whose only differences are edges
// still has HasDifference true and still drives the run's outcome, so
// hiding its edge list must not turn its section into a blank that reads
// as "nothing changed".
func TestText_EdgeOnlyTargetIsNotReportedAsUnchanged(t *testing.T) {
	r := model.NewResult("test", "2026-08-25T12:00:00Z")
	r.Targets = []model.TargetResult{{
		Certname: "web-01.example.test",
		Config:   &model.ConfigProvenance{},
		NodeDiff: &model.NodeDiff{
			Certname:      "web-01.example.test",
			HasDifference: true,
			EdgeChanges: []model.EdgeChange{
				{Kind: model.ChangeEdgeAdded, Edge: model.Edge{Source: "Class[a]", Target: "Class[b]"}},
			},
		},
	}}
	r.Reduce()

	data, err := Text(r, nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	if strings.Contains(out, "changes: none") {
		t.Errorf("an edge-only difference was reported as no changes\n---\n%s", out)
	}
	if !strings.Contains(out, "1 dependency-edge difference(s) only") {
		t.Errorf("the edge-only note is missing\n---\n%s", out)
	}
	if strings.Contains(out, "Class[a]") {
		t.Errorf("the note leaked the edge it stands in for\n---\n%s", out)
	}
}

// TestText_IndeterminateContentIsNotListedAsAChange covers older stored
// documents that still carry a content_indeterminate ResourceChange.
// Live compares no longer emit those rows; when one is present, text must
// not present it as a verified delta. The verify_content notice names the
// resource instead.
func TestText_IndeterminateContentIsNotListedAsAChange(t *testing.T) {
	r := indeterminateOnlyResult()
	data, err := Text(r, nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := string(data)

	if strings.Contains(out, "changes: none") {
		t.Errorf("indeterminate-only difference was reported as no changes\n---\n%s", out)
	}
	if strings.Contains(out, "~ File[/etc/motd] content: content_indeterminate") {
		t.Errorf("content_indeterminate appears under the change list\n---\n%s", out)
	}
	if !strings.Contains(out, "unverifiable File content comparison(s); see verify_content notices") {
		t.Errorf("the indeterminate-only note is missing\n---\n%s", out)
	}
	if !strings.Contains(out, "WARNING [verify_content]") || !strings.Contains(out, "File[/etc/motd]") {
		t.Errorf("the verify_content notice is missing\n---\n%s", out)
	}
}

// TestHTML_IndeterminateContentIsNotListedAsAChange mirrors the text
// policy: Notices carry the resource; Resource changes do not.
func TestHTML_IndeterminateContentIsNotListedAsAChange(t *testing.T) {
	r := indeterminateOnlyResult()
	data, err := HTML(r, nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	visible := string(data)[:strings.Index(string(data), "<h2>Result document</h2>")]

	if strings.Contains(visible, "No non-excluded differences") {
		t.Errorf("indeterminate-only difference was reported as no changes\n---\n%s", visible)
	}
	if strings.Contains(visible, "Resource changes") {
		t.Errorf("content_indeterminate appears under Resource changes\n---\n%s", visible)
	}
	if !strings.Contains(visible, `Notices <span class="count">1</span>`) {
		t.Errorf("the Notices chip is missing\n---\n%s", visible)
	}
	if !strings.Contains(visible, "File[/etc/motd]") || !strings.Contains(visible, "verify_content") {
		t.Errorf("the verify_content notice is missing\n---\n%s", visible)
	}
	// JSON embedded at the bottom still holds the row (html-escaped).
	if !strings.Contains(string(data), "content_indeterminate") {
		t.Error("the JSON document dropped content_indeterminate")
	}
}

func indeterminateOnlyResult() model.Result {
	r := model.NewResult("test", "2026-08-25T12:00:00Z")
	r.Targets = []model.TargetResult{{
		Certname: "web-01.example.test",
		Config:   &model.ConfigProvenance{},
		Diagnostics: []model.Diagnostic{{
			Severity: model.SeverityWarning, Operation: model.OperationVerifyContent,
			Certname: "web-01.example.test",
			Message:  "File[/etc/motd]: historical catalog has no retained content digest; current environment bytes cannot verify historical content",
		}},
		NodeDiff: &model.NodeDiff{
			Certname:      "web-01.example.test",
			HasDifference: true,
			ResourceChanges: []model.ResourceChange{{
				Kind:      model.ChangeParameterChanged,
				Identity:  model.ResourceIdentity{Type: "File", Title: "/etc/motd"},
				Parameter: "content",
				FileContent: &model.FileContentEvidence{
					State: model.FileContentIndeterminate, Redacted: true,
				},
			}},
		},
	}}
	r.Reduce()
	return r
}

// TestJSON_KeepsWhatTextAndHTMLOmit is where edges, suppression counts,
// complete node diffs and the exact generated PQL are actually
// discharged. Trimming the two human-facing formats is only defensible
// while the machine-readable record stays complete, so this test fails
// the moment display policy leaks into JSON.
func TestJSON_KeepsWhatTextAndHTMLOmit(t *testing.T) {
	data, err := JSON(sampleResult())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded model.Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.Targets[0].NodeDiff.EdgeChanges) != 1 {
		t.Errorf("edge changes did not survive into the JSON report")
	}
	if decoded.Targets[0].NodeDiff.Exclusions[0].SuppressedEdges != 1 {
		t.Errorf("the suppressed-edge count did not survive into the JSON report")
	}
	var edgeGroups int
	for _, g := range decoded.Aggregate.Groups {
		if g.Key.Kind == model.ChangeEdgeAdded || g.Key.Kind == model.ChangeEdgeRemoved {
			edgeGroups++
		}
	}
	if edgeGroups != 1 {
		t.Errorf("aggregate edge groups = %d, want 1", edgeGroups)
	}
	if decoded.ImpactEstimates[0].PQL == "" {
		t.Errorf("the generated PQL did not survive into the JSON report")
	}
	if decoded.ImpactEstimates[0].Request.Path == "" || decoded.ImpactEstimates[0].Request.OrderBy == "" {
		t.Errorf("the request options did not survive into the JSON report")
	}
}

// TestText_ImpactNodesControlsTheCertnameSample covers the --impact-nodes
// option: without it a long sample is capped and the remainder counted,
// with it every certname is named. The count itself is never elided
// either way, since that is what the estimate actually measured.
func TestText_ImpactNodesControlsTheCertnameSample(t *testing.T) {
	names := make([]string, 0, inlineCertnameCap+3)
	for i := 0; i < inlineCertnameCap+3; i++ {
		names = append(names, string(rune('a'+i))+".example.test")
	}

	r := model.NewResult("test", "2026-08-25T12:00:00Z")
	r.ImpactEstimates = []model.ImpactEstimate{{
		Identity:    model.ResourceIdentity{Type: "File", Title: "/etc/nginx/conf.d"},
		PQL:         `resources[certname] { type = "File" and title = "/etc/nginx/conf.d" }`,
		Request:     model.ImpactRequest{Path: "/pdb/query/v4", Limit: 1001},
		ResultLimit: 1000, Timeout: "10s", Status: model.ImpactStatusCompleted,
		Certnames: names, ResultCount: len(names),
	}}
	r.Reduce()

	capped, err := Text(r, nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	full, err := Text(r, nil, Options{ImpactNodes: true})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}

	if !strings.Contains(string(capped), "(+3 more)") {
		t.Errorf("the capped sample does not report the elided remainder\n---\n%s", capped)
	}
	last := names[len(names)-1]
	if strings.Contains(string(capped), last) {
		t.Errorf("the capped sample named %q past the cap", last)
	}
	if !strings.Contains(string(full), last) {
		t.Errorf("--impact-nodes did not name every certname\n---\n%s", full)
	}
	if strings.Contains(string(full), "more)") {
		t.Errorf("--impact-nodes still elided part of the sample\n---\n%s", full)
	}
	for _, want := range []string{"8 nodes"} {
		if !strings.Contains(string(capped), want) || !strings.Contains(string(full), want) {
			t.Errorf("the node count %q is not stated in both forms", want)
		}
	}
}

// TestText_NeverClaimsEstimatedNodesWillChange guards the prohibition on
// saying selected nodes will change, and CONTEXT.md's _Avoid_ wording
// for impact estimates.
func TestText_NeverClaimsEstimatedNodesWillChange(t *testing.T) {
	data, err := Text(sampleResult(), nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	// The fixed note is excluded from the scan: it is the one place the
	// report is allowed to say "will change", because it says PIACE does
	// *not* claim it.
	out := strings.ToLower(strings.ReplaceAll(string(data), ImpactEstimateNote, ""))
	for _, forbidden := range []string{"will change", "affected nodes", "blast radius", "impacted nodes"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("text report contains forbidden impact wording %q", forbidden)
		}
	}
}

// TestText_IsByteIdenticalForIdenticalInput applies the determinism
// property to the text report, which iterates provenance maps.
func TestText_IsByteIdenticalForIdenticalInput(t *testing.T) {
	first, err := Text(sampleResult(), nil, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := Text(sampleResult(), nil, Options{})
		if err != nil {
			t.Fatalf("Text: %v", err)
		}
		if string(next) != string(first) {
			t.Fatalf("render %d differs from the first render", i)
		}
	}
}

// TestText_NeutralizesTerminalControlCharacters is the regression test
// for the text report as a forgeable surface.
//
// A resource title, a diagnostic message and a change assessment's
// summary are all untrusted: the first two come from a catalog compiled
// from the change under review, the third is prose a model wrote. This
// format goes to a terminal, so an ESC in any of them is an ANSI control
// sequence, and ESC[2J ESC[H followed by a forged outcome line is enough
// to make a failing run read as a clean one to the person deciding
// whether to merge it.
func TestText_NeutralizesTerminalControlCharacters(t *testing.T) {
	const forgery = "\x1b[2J\x1b[1;1Houtcome: clean (exit 0)"

	r := sampleResult()
	r.Targets[0].NodeDiff.ResourceChanges[0].Identity.Title = forgery
	r.Targets[0].Certname = "web-01.example.test\rroot-01.example.test"
	r.Diagnostics = append(r.Diagnostics, model.Diagnostic{
		Severity: model.SeverityWarning, Operation: model.OperationLoadFacts,
		// U+009B is C1 CSI: a terminal decoding UTF-8 reads it exactly as
		// the two-character ESC [ introducer, so it has to go the same way.
		Message: "a message with a \x1b] hyperlink introducer and a  CSI",
	})

	assessment := &assess.Assessment{
		AISchemaVersion: assess.AISchemaVersion,
		Run: assess.RunAssessment{
			Risk:        assess.RiskLow,
			Summary:     forgery,
			ReviewFocus: []string{"first\nsecond"},
		},
	}

	got, err := Text(r, assessment, Options{})
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	text := string(got)

	for _, forbidden := range []string{"\x1b", "\r", "\x7f", ""} {
		if strings.Contains(text, forbidden) {
			t.Errorf("text report carries control character %q", forbidden)
		}
	}
	// Escaped rather than dropped, so a reader can tell something put an
	// escape sequence in a title rather than seeing a title that merely
	// looks odd.
	if !strings.Contains(text, `\x1b[2J`) {
		t.Errorf("an escaped ESC is not visible in the report:\n%s", text)
	}
	// The forged text must never be a line of its own: every line that
	// carries it must also carry the escape it was smuggled behind.
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "outcome: clean (exit 0)") && !strings.Contains(line, `\x1b`) {
			t.Errorf("a forged outcome line reached the report: %q", line)
		}
	}
	// The report's own line structure is untouched.
	if !strings.Contains(text, "\noutcome: "+string(wantOutcome)+" (exit 30)\n") {
		t.Errorf("the report's real outcome line was altered:\n%s", text)
	}
}
