package assess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The artifact carries real certnames: it never leaves the machine that
// produced it, and a reader who has to map node-007 back by hand has been
// given a puzzle rather than a report.
func TestArtifactCarriesRealCertnamesAndItsProvenance(t *testing.T) {
	planned, _, _ := PlanGroups(assessableResult(), DefaultMaxGroups)
	a := unknownAssessment(planned)
	a.AISchemaVersion = AISchemaVersion
	a.ModelID = "test-model"
	a.EndpointAuthority = "api.example.com"
	a.SourceReportChecksum = "sha256:abc"

	path := filepath.Join(t.TempDir(), "assessment.json")
	if err := WriteArtifact(path, a); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading artifact: %v", err)
	}
	body := string(raw)

	if !strings.Contains(body, realCertname) {
		t.Error("the artifact does not carry real certnames")
	}
	if strings.Contains(body, "node-00") {
		t.Error("a pseudonym reached the artifact")
	}
	for _, want := range []string{`"ai_schema_version":2`, `"model_id":"test-model"`, `"source_report_checksum":"sha256:abc"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the artifact is missing %s", want)
		}
	}
	if !strings.HasSuffix(body, "\n") {
		t.Error("the artifact is not newline terminated")
	}
}

func TestArtifactEncodingIsStableForAnUnchangedAssessment(t *testing.T) {
	planned, _, _ := PlanGroups(assessableResult(), DefaultMaxGroups)
	a := unknownAssessment(planned)

	first, err := JSON(a)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	second, err := JSON(a)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if string(first) != string(second) {
		t.Error("encoding the same assessment twice produced different bytes")
	}
}
