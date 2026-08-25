package transport

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example42/piace/internal/model"
)

const samplePEMKey = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIKZ3s3TopSecretBytesThatMustNeverAppearInAnyLogLine1234
oAoGCCqGSM49AwEHoUQDQgAEexamplepublickeymaterialalsoirrelevant==
-----END EC PRIVATE KEY-----`

const sampleAuthHeaderValue = "Bearer sUpErSeCrEtToKeN12345"

func TestSanitize_RedactsAuthorizationHeaderValue(t *testing.T) {
	input := "sending request with header Authorization: " + sampleAuthHeaderValue + " to host"
	out := Sanitize(input)
	if strings.Contains(out, sampleAuthHeaderValue) {
		t.Errorf("Sanitize output still contains the secret token: %q", out)
	}
	if !strings.Contains(out, "<redacted>") {
		t.Errorf("Sanitize output missing redaction marker: %q", out)
	}
}

func TestSanitize_RedactsPEMKeyContent(t *testing.T) {
	input := "failed to load key:\n" + samplePEMKey + "\nadditional context"
	out := Sanitize(input)
	if strings.Contains(out, "TopSecretBytes") {
		t.Errorf("Sanitize output still contains PEM key material: %q", out)
	}
	if strings.Contains(out, "-----BEGIN") {
		t.Errorf("Sanitize output still contains a PEM header: %q", out)
	}
}

func TestDiagnostic_NeverContainsAuthorizationOrPEMContent(t *testing.T) {
	err := errors.New("request failed, saw header Authorization: " + sampleAuthHeaderValue +
		" and key material " + samplePEMKey)

	d := Diagnostic(model.OperationRequestCandidate, "web-01.example.test",
		Summary{Host: "compiler.example.test:8140", StatusCode: 0, Duration: 5 * time.Second},
		SafeMessage(err))

	if strings.Contains(d.Message, sampleAuthHeaderValue) {
		t.Errorf("Diagnostic.Message leaks the Authorization value: %q", d.Message)
	}
	if strings.Contains(d.Message, "TopSecretBytes") || strings.Contains(d.Message, "-----BEGIN") {
		t.Errorf("Diagnostic.Message leaks PEM key content: %q", d.Message)
	}
	if d.Operation != model.OperationRequestCandidate {
		t.Errorf("Operation = %q, want %q", d.Operation, model.OperationRequestCandidate)
	}
	if d.Certname != "web-01.example.test" {
		t.Errorf("Certname = %q", d.Certname)
	}
	if !strings.Contains(d.Message, "compiler.example.test:8140") {
		t.Errorf("Message = %q, want it to include the safe host", d.Message)
	}
}

func TestSafeMessage_TruncatesAtFirstNewline(t *testing.T) {
	err := errors.New("first line\nsecond line with " + samplePEMKey)
	msg := SafeMessage(err)
	if strings.Contains(msg, "second line") {
		t.Errorf("SafeMessage did not truncate at newline: %q", msg)
	}
}

func TestSummary_DescribeNeverIncludesHeadersOrBody(t *testing.T) {
	s := Summary{Host: "puppetdb.example.test:8081", StatusCode: 503, Duration: 2 * time.Second}
	desc := s.Describe()
	if !strings.Contains(desc, "puppetdb.example.test:8081") {
		t.Errorf("Describe() = %q, want host present", desc)
	}
	if !strings.Contains(desc, "503") {
		t.Errorf("Describe() = %q, want status present", desc)
	}
}
