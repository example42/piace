package assess

import (
	"strings"
	"testing"
)

func plannedFixture(t *testing.T) ([]PlannedGroup, Pseudonyms) {
	t.Helper()
	r := assessableResult()
	planned, _, _ := PlanGroups(r, DefaultMaxGroups)
	return planned, newPseudonyms(r, true)
}

// a well-formed response becomes an assessment, with any
// pseudonym the model used in its prose put back to the real certname.
func TestInterpretReadsAWellFormedResponse(t *testing.T) {
	planned, p := plannedFixture(t)
	alias := p.Of(realCertname)

	raw := `{
      "run": {"risk":"medium","summary":"Restarting nginx on ` + alias + ` is routine.","review_focus":["` + alias + `"]},
      "groups": [
        {"id":"g001","risk":"low","rationale":"A service ensure flip.","review_focus":[]},
        {"id":"g002","risk":"high","rationale":"Touches ` + alias + `.","review_focus":["g002"]}
      ]}`

	a, diags := Interpret([]byte(raw), planned, p)
	if len(diags) != 0 {
		t.Errorf("diagnostics = %+v, want none", diags)
	}
	if a.Run.Risk != RiskMedium {
		t.Errorf("Run.Risk = %q", a.Run.Risk)
	}
	if strings.Contains(a.Run.Summary, alias) || !strings.Contains(a.Run.Summary, realCertname) {
		t.Errorf("Run.Summary still pseudonymous: %q", a.Run.Summary)
	}
	if len(a.Run.ReviewFocus) != 1 || a.Run.ReviewFocus[0] != realCertname {
		t.Errorf("Run.ReviewFocus = %v", a.Run.ReviewFocus)
	}
	if len(a.Groups) != 2 {
		t.Fatalf("Groups = %d, want 2", len(a.Groups))
	}
	if a.Groups[0].Identity != "Service[nginx]" || a.Groups[0].Risk != RiskLow {
		t.Errorf("Groups[0] = %+v", a.Groups[0])
	}
	if len(a.Groups[0].Certnames) != 2 || a.Groups[0].Certnames[0] != realCertname {
		t.Errorf("Groups[0].Certnames = %v, want the real names", a.Groups[0].Certnames)
	}
	if strings.Contains(a.Groups[1].Rationale, alias) {
		t.Errorf("Groups[1].Rationale still pseudonymous: %q", a.Groups[1].Rationale)
	}
}

// a response wrapped in a Markdown code fence is read anyway, and
// the wrapper is recorded: the alternative is discarding a complete
// assessment over its packaging.
func TestInterpretUnwrapsACodeFence(t *testing.T) {
	planned, p := plannedFixture(t)
	raw := "```json\n" + `{"run":{"risk":"low","summary":"Routine.","review_focus":[]},
      "groups":[{"id":"g001","risk":"low","rationale":"A service ensure flip.","review_focus":[]}]}` + "\n```"

	a, diags := Interpret([]byte(raw), planned, p)
	if a.Run.Risk != RiskLow {
		t.Errorf("Run.Risk = %q, want the fenced response to have been read", a.Run.Risk)
	}
	if a.Groups[0].Risk != RiskLow {
		t.Errorf("Groups[0].Risk = %q", a.Groups[0].Risk)
	}
	for _, d := range diags {
		if d.Severity == SeverityError {
			t.Errorf("unexpected error diagnostic: %q", d.Message)
		}
	}
	var noted bool
	for _, d := range diags {
		if strings.Contains(d.Message, "code fence") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the code fence was unwrapped without a diagnostic: %+v", diags)
	}
}

// an unfenced response is passed through untouched, backticks in a
// rationale included. Only a fence wrapping the whole response is a
// wrapper; a backtick anywhere else is content.
func TestInterpretLeavesBackticksInsideAResponseAlone(t *testing.T) {
	planned, p := plannedFixture(t)
	raw := "{\"run\":{\"risk\":\"low\",\"summary\":\"Adds `Package[openssh-server]`.\",\"review_focus\":[]}," +
		"\"groups\":[{\"id\":\"g001\",\"risk\":\"low\",\"rationale\":\"```\",\"review_focus\":[]}]}"

	a, diags := Interpret([]byte(raw), planned, p)
	for _, d := range diags {
		if d.Severity == SeverityError {
			t.Fatalf("unexpected error diagnostic: %q", d.Message)
		}
		if strings.Contains(d.Message, "code fence") {
			t.Errorf("an unfenced response was reported as fenced: %q", d.Message)
		}
	}
	if !strings.Contains(a.Run.Summary, "`Package[openssh-server]`") {
		t.Errorf("Run.Summary = %q, want its backticks kept", a.Run.Summary)
	}
	if a.Groups[0].Rationale != "```" {
		t.Errorf("Groups[0].Rationale = %q, want it untouched", a.Groups[0].Rationale)
	}
}

// an id that was never sent is a hallucinated anchor. It is
// dropped and recorded, which is the check per-group assessment exists to
// make possible.
func TestInterpretDropsAGroupItNeverSent(t *testing.T) {
	planned, p := plannedFixture(t)
	raw := `{"run":{"risk":"low","summary":"","review_focus":[]},
             "groups":[{"id":"g001","risk":"low","rationale":"","review_focus":[]},
                       {"id":"g999","risk":"high","rationale":"invented","review_focus":[]}]}`

	a, diags := Interpret([]byte(raw), planned, p)
	for _, g := range a.Groups {
		if g.ID == "g999" {
			t.Fatal("an invented group id reached the assessment")
		}
	}
	if !hasDiagnostic(diags, "g999") {
		t.Errorf("no diagnostic names the dropped id: %+v", diags)
	}
}

// a group that went out and came back unmentioned is unknown,
// never silently absent.
func TestInterpretMarksAnUnansweredGroupUnknown(t *testing.T) {
	planned, p := plannedFixture(t)
	raw := `{"run":{"risk":"low","summary":"","review_focus":[]},
             "groups":[{"id":"g001","risk":"low","rationale":"","review_focus":[]}]}`

	a, _ := Interpret([]byte(raw), planned, p)
	if len(a.Groups) != len(planned) {
		t.Fatalf("Groups = %d, want %d: every group sent must be accounted for", len(a.Groups), len(planned))
	}
	for _, g := range a.Groups[1:] {
		if g.Risk != RiskUnknown {
			t.Errorf("group %s = %q, want unknown", g.ID, g.Risk)
		}
	}
}

// a risk indication outside the enum becomes unknown plus a
// diagnostic; it never reaches a renderer as prose.
func TestInterpretRefusesARiskOutsideTheEnum(t *testing.T) {
	planned, p := plannedFixture(t)
	raw := `{"run":{"risk":"catastrophic","summary":"","review_focus":[]},
             "groups":[{"id":"g001","risk":"pretty bad honestly","rationale":"","review_focus":[]}]}`

	a, diags := Interpret([]byte(raw), planned, p)
	if a.Run.Risk != RiskUnknown {
		t.Errorf("Run.Risk = %q, want unknown", a.Run.Risk)
	}
	if a.Groups[0].Risk != RiskUnknown {
		t.Errorf("Groups[0].Risk = %q, want unknown", a.Groups[0].Risk)
	}
	if len(diags) < 2 {
		t.Errorf("diagnostics = %+v, want one per rejected risk", diags)
	}
}

// an unparseable response is an error the caller
// can retry on, not a partial assessment.
func TestInterpretRejectsAnUnparseableResponse(t *testing.T) {
	planned, p := plannedFixture(t)
	for name, raw := range map[string]string{
		"not json":     "I'm sorry, I can't help with that.",
		"wrong shape":  `{"run":"medium"}`,
		"empty":        "",
		"json but nil": "null",
	} {
		t.Run(name, func(t *testing.T) {
			if _, diags := Interpret([]byte(raw), planned, p); !hasError(diags) {
				t.Errorf("Interpret accepted %s without an error diagnostic", name)
			}
		})
	}
}

func hasDiagnostic(diags []Diagnostic, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func hasError(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}
