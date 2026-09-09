package main

import (
	"strings"
	"testing"

	"github.com/example42/piace/internal/inference"
	"github.com/example42/piace/internal/transport"
)

func TestDebugMetadataCannotInjectLines(t *testing.T) {
	value := "evil\nline\r\x1b\u2028" + strings.Repeat("x", 10000)
	for _, line := range []string{
		describeEvent(transport.Event{Method: "GET", URL: "https://example.test", Shape: transport.ShapeObject, TopLevelKeys: []string{value}, ContentType: value}),
		describeInferenceEvent(inference.Event{Method: "POST", URL: "https://example.test", Shape: inference.ShapeObject, TopLevelKeys: []string{value}, ContentType: value}),
	} {
		if strings.ContainsAny(line, "\n\r\x1b\u2028") || len(line) > 1000 {
			t.Fatal("unsafe debug line")
		}
	}
}
