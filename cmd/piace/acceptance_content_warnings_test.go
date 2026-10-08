package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

func TestAcceptance_SuppressSourceContentWarnings(t *testing.T) {
	h := newHarness(t)
	baseline := []resourceSpec{
		{Type: "File", Title: "/same", Parameters: map[string]any{"source": "puppet:///modules/app/same"}},
		{Type: "File", Title: "/changed", Parameters: map[string]any{"source": "puppet:///modules/app/old"}},
		{Type: "File", Title: "/directory", Parameters: map[string]any{"source": "puppet:///modules/app/dir", "recurse": true}},
		{Type: "File", Title: "/local", Parameters: map[string]any{"source": "/local/file"}},
	}
	candidate := append([]resourceSpec(nil), baseline...)
	candidate[1].Parameters = map[string]any{"source": "puppet:///modules/app/new"}
	h.seedTarget("web-01.example.test", baseline, candidate, nil)
	h.compiler.fileContent["modules/app/same"] = "same bytes"
	h.compiler.fileContent["modules/app/new"] = "new bytes"
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
	before := h.compare(t)
	after := h.compare(t, "--suppress-source-content-warnings")
	if before.code != exitcode.Success || after.code != before.code {
		t.Fatalf("exits = %d/%d, stderr: %s / %s", before.code, after.code, before.stderr, after.stderr)
	}
	for format, artifact := range after.all() {
		if strings.Contains(artifact, "verify_content") {
			t.Errorf("%s still carries source warnings", format)
		}
	}
	for format, artifact := range before.all() {
		if !strings.Contains(artifact, "verify_content") {
			t.Errorf("%s default warnings absent", format)
		}
	}
	var original, quiet model.Result
	if err := json.Unmarshal([]byte(before.json), &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(after.json), &quiet); err != nil {
		t.Fatal(err)
	}
	if len(original.Targets[0].Diagnostics) != 4 || len(quiet.Targets[0].Diagnostics) != 0 {
		t.Fatalf("diagnostic counts = %d/%d", len(original.Targets[0].Diagnostics), len(quiet.Targets[0].Diagnostics))
	}
	if !reflect.DeepEqual(original.Targets[0].NodeDiff, quiet.Targets[0].NodeDiff) || !reflect.DeepEqual(original.Aggregate, quiet.Aggregate) || original.Outcome != quiet.Outcome {
		t.Fatal("suppression changed comparison evidence or outcome")
	}
	changes := quiet.Targets[0].NodeDiff.ResourceChanges
	if len(changes) != 1 || changes[0].FileContent == nil || changes[0].FileContent.State != model.FileContentReferenceChanged {
		t.Fatalf("source-reference difference disappeared: %+v", changes)
	}
}

func TestAcceptance_SuppressSourceWarningsPreservesErrors(t *testing.T) {
	h := newHarness(t)
	resources := []resourceSpec{{Type: "File", Title: "/missing", Parameters: map[string]any{"source": "puppet:///modules/app/missing"}}}
	h.seedTarget("web-01.example.test", resources, resources, nil)
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
	got := h.compare(t, "--suppress-source-content-warnings")
	if got.code != exitcode.OperationalError {
		t.Fatalf("retrieval failure was silenced: exit %d, stderr: %s", got.code, got.stderr)
	}
	for format, artifact := range got.all() {
		if !strings.Contains(artifact, "verify_content") {
			t.Errorf("%s lost the retrieval error", format)
		}
	}
}
