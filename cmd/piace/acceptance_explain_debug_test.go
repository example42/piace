package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
)

// TestAcceptance_ExplainDebugPrintsInferenceStatus covers `explain
// --debug`: one stderr line for the inference request carrying the HTTP
// status and the response body's top-level JSON shape, and nothing from
// inside the body — which is exactly what diagnoses a provider that
// rejects the request with a 400.
func TestAcceptance_ExplainDebugPrintsInferenceStatus(t *testing.T) {
	h := newHarness(t)
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
	h.seedTarget("web-01.example.test", baseResources(), []resourceSpec{
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running", "enable": true}},
	}, baseEdges())

	// A real Anthropic OpenAI-compat rejection: an identity-linked API key
	// used without the workspace-id header PIACE does not send.
	stub := newInferenceStub(t)
	stub.status = 400
	stub.rawBody = `{"error":{"code":"invalid_request_error","message":"anthropic-workspace-id is required when authenticating with an identity-linked API key; send the id of the workspace this request acts in.","type":"invalid_request_error","param":null}}`

	got := h.explain(t, stub, h.storedReport(t), "--debug")

	for _, want := range []string{
		"debug #001 POST " + stub.server.URL,
		"-> 400 in ",
		"top-level keys: error",
	} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("--debug stderr does not contain %q:\n%s", want, got.stderr)
		}
	}
	// The line is metadata only: no message value from the error body, and
	// nothing from the request payload, reaches stderr.
	for _, forbidden := range []string{"anthropic-workspace-id", "identity-linked", "comparison_data", "Service[nginx]"} {
		if strings.Contains(got.stderr, forbidden) {
			t.Errorf("--debug stderr leaked body content %q:\n%s", forbidden, got.stderr)
		}
	}
	// A failed assessment still exits 0 without --fail-on-inference-error.
	if got.code != exitcode.Success {
		t.Errorf("explain --debug exited %d, want %d", got.code, exitcode.Success)
	}
}

// TestAcceptance_ExplainDebugDumpDirWritesRequestAndResponse covers the
// other tier: --debug-dump-dir writes the raw request and response
// bodies to 0600 files, never to the console. The response body of a 4xx
// is the only place the provider names the field it rejected, and the
// request-body dump shows exactly what PIACE sent.
func TestAcceptance_ExplainDebugDumpDirWritesRequestAndResponse(t *testing.T) {
	h := newHarness(t)
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
	h.seedTarget("web-01.example.test", baseResources(), []resourceSpec{
		{Type: "Service", Title: "nginx", Parameters: map[string]any{"ensure": "running", "enable": true}},
	}, baseEdges())

	// A real OpenAI GPT-5 rejection: max_tokens is not accepted, the API
	// wants max_completion_tokens instead.
	stub := newInferenceStub(t)
	stub.status = 400
	stub.rawBody = `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`

	dumpDir := h.path("infer-dump")
	got := h.explain(t, stub, h.storedReport(t), "--debug-dump-dir", dumpDir)

	if !strings.Contains(got.stderr, "writing raw request/response bodies to "+dumpDir) {
		t.Errorf("no dump-dir notice printed:\n%s", got.stderr)
	}

	entries, err := os.ReadDir(dumpDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dumpDir, err)
	}

	var sawRequest, sawResponse bool
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Info(%s): %v", e.Name(), err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", e.Name(), perm)
		}
		body, err := os.ReadFile(filepath.Join(dumpDir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", e.Name(), err)
		}
		switch {
		case strings.HasSuffix(e.Name(), ".request.json"):
			sawRequest = true
			if !strings.Contains(string(body), "comparison_data") || !strings.Contains(string(body), `"model":`) {
				t.Errorf("request dump is not the sent payload:\n%s", body)
			}
		case strings.HasSuffix(e.Name(), ".response.json"):
			sawResponse = true
			if !strings.Contains(string(body), "Use 'max_completion_tokens' instead") {
				t.Errorf("response dump is not the raw error body:\n%s", body)
			}
		}
	}
	if !sawRequest {
		t.Errorf("no inference request dump written; got %v", names(entries))
	}
	if !sawResponse {
		t.Errorf("no inference response dump written; got %v", names(entries))
	}

	// The bearer token is a header, never a body, so it cannot be in a
	// dump file; and no raw body reaches the console.
	if strings.Contains(got.stderr, "a-bearer-token") || strings.Contains(got.stdout, "invalid_request_error") {
		t.Error("raw content reached stdout/stderr")
	}
}
