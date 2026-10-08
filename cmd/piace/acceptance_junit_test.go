package main

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"

	"github.com/example42/piace/internal/exitcode"
)

func TestAcceptance_JUnitPolicyDifference(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), changedResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")+"    fail_on_diff: true\n"))
	got := h.compare(t)
	if got.code != exitcode.PolicyDisallowedDifference {
		t.Fatalf("exit = %d, stderr: %s", got.code, got.stderr)
	}
	var document struct {
		Suite struct {
			Failures int `xml:"failures,attr"`
			Cases    []struct {
				Name    string `xml:"name,attr"`
				Failure *struct {
					Details string `xml:",chardata"`
				} `xml:"failure"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal([]byte(got.junit), &document); err != nil {
		t.Fatal(err)
	}
	if document.Suite.Failures != 1 || len(document.Suite.Cases) != 1 {
		t.Fatalf("incorrect suite: %+v", document.Suite)
	}
	c := document.Suite.Cases[0]
	if c.Name != "web-01.example.test" || c.Failure == nil || !strings.Contains(c.Failure.Details, "Service[nginx] ensure") {
		t.Fatalf("incorrect policy failure: %+v", c)
	}
}

func TestAcceptance_JUnitDestinationConflicts(t *testing.T) {
	for _, destination := range []string{"targets.yaml", "same.xml"} {
		t.Run(destination, func(t *testing.T) {
			h := newHarness(t)
			h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
			h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
			before := readFile(t, h.path("targets.yaml"))
			extra := []string{"--junit-out", h.path(destination)}
			if destination == "same.xml" {
				extra = append(extra, "--text-out", h.path(destination))
			}
			got := h.compare(t, extra...)
			if got.code != exitcode.OperationalError || !strings.Contains(got.stderr, "--junit-out") || !strings.Contains(got.stderr, "conflicting file destinations") {
				t.Fatalf("exit = %d, stderr: %s", got.code, got.stderr)
			}
			if readFile(t, h.path("targets.yaml")) != before || h.pdb.count() != 0 {
				t.Fatal("conflicting destination modified input or contacted services")
			}
		})
	}
}

func TestAcceptance_JUnitWriteFailure(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
	destination := h.path("report-directory")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	got := h.compare(t, "--junit-out", destination)
	if got.code != exitcode.OperationalError || !strings.Contains(got.stderr, "writing JUnit report") || got.stdout != "" {
		t.Fatalf("exit = %d, stdout: %s, stderr: %s", got.code, got.stdout, got.stderr)
	}
}
