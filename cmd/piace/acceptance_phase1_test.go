package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

func TestAcceptance_ResourceSensitivityProtectsReportsAndInference(t *testing.T) {
	for _, side := range []string{"baseline", "candidate", "both"} {
		t.Run(side, func(t *testing.T) {
			h := newHarness(t)
			before := []resourceSpec{{Type: "User", Title: "app", Parameters: map[string]any{"password": "phase1-old-secret"}}}
			after := []resourceSpec{{Type: "User", Title: "app", Parameters: map[string]any{"password": "phase1-new-secret"}}}
			baseline := pdbCatalog("web-01.example.test", "production", before, nil)
			candidate := compilerCatalog("web-01.example.test", "feature-123", after, nil)
			if side != "candidate" {
				baseline["resources"].(map[string]any)["data"].([]map[string]any)[0]["sensitive_parameters"] = []string{"password"}
			}
			if side != "baseline" {
				candidate["resources"].([]map[string]any)[0]["sensitive_parameters"] = []string{"password"}
			}
			h.pdb.factsets["web-01.example.test"] = pdbFactset("web-01.example.test", true)
			h.pdb.catalogs["web-01.example.test"] = baseline
			h.compiler.catalogs["web-01.example.test"] = candidate
			h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
			got := h.compare(t, "--debug", "--text-out", h.path("report.txt"))
			if got.code != exitcode.Success {
				t.Fatalf("compare failed: %s", got.stderr)
			}
			var report model.Result
			if err := json.Unmarshal([]byte(got.json), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Targets) != 1 || report.Targets[0].NodeDiff == nil || !report.Targets[0].NodeDiff.HasDifference {
				t.Fatal("missing sensitive change")
			}
			artifacts := got.all()
			artifacts["debug"] = got.stderr
			stub := newInferenceStub(t)
			explained := h.explain(t, stub, h.path("report0.json"), "--debug")
			if explained.code != exitcode.Success || stub.count() == 0 {
				t.Fatalf("explain failed: %s", explained.stderr)
			}
			artifacts["inference"] = strings.Join(stub.requests, "\n")
			artifacts["inference debug"] = explained.stderr
			artifacts["assessment"] = explained.assessment
			for name, artifact := range artifacts {
				if strings.Contains(artifact, "phase1-old-secret") || strings.Contains(artifact, "phase1-new-secret") {
					t.Errorf("%s disclosed a protected value", name)
				}
			}
		})
	}
}

func TestAcceptance_WrongInputIdentityStopsBeforeCompilation(t *testing.T) {
	for _, kind := range []string{"factset", "baseline", "trusted"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			facts := pdbFactset("web-01.example.test", true)
			baseline := pdbCatalog("web-01.example.test", "production", nil, nil)
			switch kind {
			case "factset":
				facts["certname"] = "different-node"
			case "baseline":
				baseline["certname"] = "different-node"
			case "trusted":
				facts["facts"].(map[string]any)["data"].([]map[string]any)[2]["value"].(map[string]any)["certname"] = "different-node"
			}
			h.pdb.factsets["web-01.example.test"] = facts
			h.pdb.catalogs["web-01.example.test"] = baseline
			h.compiler.catalogs["web-01.example.test"] = compilerCatalog("web-01.example.test", "feature-123", nil, nil)
			h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))
			got := h.compare(t)
			if got.code != exitcode.OperationalError {
				t.Fatalf("accepted wrong %s identity: code=%d, %s", kind, got.code, got.stdout)
			}
			if h.compiler.count() != 0 {
				t.Fatal("candidate compilation ran on invalid input")
			}
		})
	}
}

func TestAcceptance_ChecksummedWrongSnapshotIdentityStopsCompilation(t *testing.T) {
	for _, kind := range []snapshot.Kind{snapshot.KindFactset, snapshot.KindCatalog} {
		t.Run(string(kind), func(t *testing.T) {
			h := newHarness(t)
			certname := "web-01.example.test"
			h.writeConfigs(t, targetsYAML(snapshotDefaults, target(certname)))
			fs, _ := json.Marshal(pdbFactset(certname, true))
			facts := snapshot.Envelope{FormatVersion: snapshot.FormatVersion, Kind: snapshot.KindFactset, Target: certname, Source: snapshot.Source{Kind: "puppetdb"}, CapturedAt: fixedTimestamp.Format(time.RFC3339), Payload: fs}
			if kind == snapshot.KindFactset {
				facts.Payload = json.RawMessage(strings.ReplaceAll(string(fs), certname, "different-node"))
			}
			var err error
			facts.PayloadChecksum, err = snapshot.Checksum(facts.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := snapshot.Write(h.path("snapshots/facts/"+certname+".json"), facts, false); err != nil {
				t.Fatal(err)
			}
			if kind == snapshot.KindCatalog {
				catalog := snapshot.Envelope{FormatVersion: snapshot.FormatVersion, Kind: snapshot.KindCatalog, Target: certname, Source: snapshot.Source{Kind: "compiler"}, CapturedAt: fixedTimestamp.Format(time.RFC3339), RequestedEnvironment: "production", CompilerAPIVersion: snapshot.CompilerAPIv4, InputFactsetIdentity: facts.PayloadChecksum, Payload: json.RawMessage(`{"certname":"different-node","environment":"production","resources":[],"edges":[]}`)}
				catalog.PayloadChecksum, err = snapshot.Checksum(catalog.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if err := snapshot.Write(h.path("snapshots/catalogs/"+certname+".json"), catalog, false); err != nil {
					t.Fatal(err)
				}
			}
			got := h.compare(t)
			if got.code != exitcode.OperationalError || h.compiler.count() != 0 || h.pdb.count() != 0 {
				t.Fatalf("invalid snapshot triggered compilation or retrieval: code=%d", got.code)
			}
			if !strings.Contains(got.stdout, "payload certname") {
				t.Fatalf("unexpected rejection: %s", got.stdout)
			}
		})
	}
}

func TestAcceptance_SensitiveFileSourceStaysOutOfDebug(t *testing.T) {
	h := newHarness(t)
	certname := "web-01.example.test"
	before := []resourceSpec{{Type: "File", Title: "/config", Parameters: map[string]any{"source": "puppet:///modules/app/private-before"}}}
	after := []resourceSpec{{Type: "File", Title: "/config", Parameters: map[string]any{"source": "puppet:///modules/app/private-after"}}}
	h.pdb.factsets[certname] = pdbFactset(certname, true)
	h.pdb.catalogs[certname] = pdbCatalog(certname, "production", before, nil)
	candidate := compilerCatalog(certname, "feature-123", after, nil)
	candidate["resources"].([]map[string]any)[0]["sensitive_parameters"] = []string{"source"}
	h.compiler.catalogs[certname] = candidate
	h.compiler.fileContent["modules/app/private-before"] = "secret-file-before"
	h.compiler.fileContent["modules/app/private-after"] = "secret-file-after"
	h.writeConfigs(t, targetsYAML(defaultDefaults, target(certname)))
	got := h.compare(t, "--debug")
	if got.code != exitcode.Success || !strings.Contains(got.json, `"redacted":true`) {
		t.Fatalf("missing redacted file evidence: %s", got.stdout)
	}
	for _, text := range []string{got.stdout, got.json, got.html, got.stderr} {
		for _, secret := range []string{"private-before", "private-after", "secret-file-before", "secret-file-after"} {
			if strings.Contains(text, secret) {
				t.Error("disclosed sensitive source or content")
			}
		}
	}
}
