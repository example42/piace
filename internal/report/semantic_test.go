package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

// The distinction the projection exists to make: two runs that reached
// the same comparison at different instants, with different binaries,
// against differently named services.
func TestSemanticProjection_IgnoresInvocationMetadata(t *testing.T) {
	first := sampleResult()
	first.Invocation = model.Invocation{ToolVersion: "0.5.0", TimestampUTC: "2026-09-10T08:00:00Z"}

	second := sampleResult()
	second.Invocation = model.Invocation{ToolVersion: "0.6.0", TimestampUTC: "2026-11-02T17:43:11Z"}

	firstBytes, err := JSON(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := JSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("the two documents are byte-identical, so this asserts nothing")
	}

	firstSemantic, err := SemanticProjection(first)
	if err != nil {
		t.Fatal(err)
	}
	secondSemantic, err := SemanticProjection(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstSemantic, secondSemantic) {
		t.Errorf("two runs reaching the same comparison projected differently:\n%s\n%s", firstSemantic, secondSemantic)
	}
	if strings.Contains(string(firstSemantic), "2026-09-10T08:00:00Z") ||
		strings.Contains(string(firstSemantic), `"invocation"`) {
		t.Errorf("the projection retains invocation metadata:\n%s", firstSemantic)
	}
}

// A difference in the comparison itself must survive the projection, or
// it would erase what it is meant to compare.
func TestSemanticProjection_KeepsTheComparison(t *testing.T) {
	first := sampleResult()
	second := sampleResult()
	second.Targets[0].NodeDiff.ResourceChanges[1].After = "restarted"

	a, err := SemanticProjection(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SemanticProjection(second)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("a changed parameter value did not change the semantic projection")
	}
}

// The whole document, invocation metadata included, still encodes to the
// same bytes every time. That is the other claim, and it is unaffected.
func TestJSON_IsByteIdenticalForOneResult(t *testing.T) {
	r := sampleResult()
	a, err := JSON(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := JSON(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("encoding one result twice produced different bytes")
	}
}
