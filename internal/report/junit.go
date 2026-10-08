package report

import (
	"bytes"
	"encoding/xml"
	"fmt"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

type junitSuites struct {
	XMLName  xml.Name   `xml:"testsuites"`
	Tests    int        `xml:"tests,attr"`
	Failures int        `xml:"failures,attr"`
	Errors   int        `xml:"errors,attr"`
	Suite    junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Timestamp string      `xml:"timestamp,attr,omitempty"`
	Cases     []junitCase `xml:"testcase"`
	Output    string      `xml:"system-out"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
	Output    string        `xml:"system-out"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Details string `xml:",chardata"`
}

// JUnit maps the already classified outcomes to CI test cases. Allowed
// differences pass because fail_on_diff is the policy that decides failure.
func JUnit(r model.Result) ([]byte, error) {
	suite := junitSuite{Name: "piace.compare", Timestamp: r.Invocation.TimestampUTC}
	for _, target := range r.Targets {
		var output bytes.Buffer
		writeTextTargets(&output, []model.TargetResult{target})
		// Edge-only differences must explain a failure even though the text
		// renderer normally omits edges for a compact CI log.
		if target.NodeDiff != nil {
			for _, change := range target.NodeDiff.EdgeChanges {
				textf(&output, "    %s %s -> %s\n", change.Kind, change.Edge.Source, change.Edge.Target)
			}
		}
		c, err := newJUnitCase(target.Certname, "piace.target", target.Outcome, output.String())
		if err != nil {
			return nil, err
		}
		suite.Cases = append(suite.Cases, c)
	}
	if len(r.Diagnostics) > 0 {
		// Reducing only run diagnostics avoids attributing a target's failure
		// to the synthetic run case as well.
		run := model.Result{Diagnostics: r.Diagnostics}
		run.Finalize()
		var output bytes.Buffer
		writeTextRunDiagnostics(&output, r.Diagnostics)
		c, err := newJUnitCase("run diagnostics", "piace.run", run.Outcome, output.String())
		if err != nil {
			return nil, err
		}
		suite.Cases = append(suite.Cases, c)
	}
	for _, c := range suite.Cases {
		if c.Failure != nil {
			suite.Failures++
		}
		if c.Error != nil {
			suite.Errors++
		}
	}
	suite.Tests = len(suite.Cases)
	var output bytes.Buffer
	textf(&output, "PIACE %s\noutcome: %s (exit %d)\n", r.Invocation.ToolVersion, r.Outcome, r.ExitCode)
	for _, reason := range r.Reasons {
		textf(&output, "  reason: %s\n", reason)
	}
	writeTextAggregate(&output, r.Aggregate)
	writeTextImpact(&output, r.ImpactEstimates, Options{ImpactNodes: true})
	suite.Output = output.String()
	document := junitSuites{Tests: suite.Tests, Failures: suite.Failures, Errors: suite.Errors, Suite: suite}
	data, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding JUnit report: %w", err)
	}
	return append([]byte(xml.Header), append(data, '\n')...), nil
}

func newJUnitCase(name, classname string, outcome exitcode.Outcome, output string) (junitCase, error) {
	c := junitCase{Name: name, Classname: classname, Output: output}
	problem := &junitProblem{Message: string(outcome), Type: string(outcome), Details: output}
	switch outcome {
	case exitcode.OutcomeClean, exitcode.OutcomeDifferencesAllowed:
	case exitcode.OutcomePolicyDisallowedDifference:
		c.Failure = problem
	case exitcode.OutcomeCompilationFailure, exitcode.OutcomeOperationalError:
		c.Error = problem
	default:
		return junitCase{}, fmt.Errorf("encoding JUnit report: unrecognized outcome %q for %s", outcome, name)
	}
	return c, nil
}
