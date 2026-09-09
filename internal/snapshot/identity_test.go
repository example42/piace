package snapshot

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestChecksummedEnvelopeMustAgreeWithPayload(t *testing.T) {
	for _, kind := range []Kind{KindFactset, KindCatalog} {
		env := validFactsetEnvelope(t)
		env.Kind = kind
		env.Payload = json.RawMessage(`{"certname":"different-node","environment":"production","facts":{"data":[]},"resources":[],"edges":[]}`)
		if kind == KindCatalog {
			env.Source.Kind = "compiler"
			env.RequestedEnvironment = "production"
			env.Capture = validCaptureProvenance()
			env.InputFactsetIdentity = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		}
		var err error
		env.PayloadChecksum, err = Checksum(env.Payload)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "snapshot.json")
		if err := Write(path, env, false); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(loaded, kind, env.Target); err == nil {
			t.Fatal("accepted valid checksum with wrong payload identity")
		}
	}
}

func TestRequiredSnapshotMetadata(t *testing.T) {
	base := validFactsetEnvelope(t)
	base.Kind, base.Source.Kind = KindCatalog, "compiler"
	base.Payload = json.RawMessage(`{"certname":"web-01.example.test","environment":"production"}`)
	base.RequestedEnvironment, base.Capture, base.InputFactsetIdentity = "production", validCaptureProvenance(), "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := Validate(base, KindCatalog, base.Target); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Envelope){
		"source":         func(e *Envelope) { e.Source.Kind = "" },
		"unknown source": func(e *Envelope) { e.Source.Kind = "unknown" },
		"api": func(e *Envelope) {
			e.Capture = &CaptureProvenance{RequestedAPI: "v5", EffectiveAPI: "v5", FactSource: FactSourcePuppetDB}
		},
		"missing api":                func(e *Envelope) { e.Capture = &CaptureProvenance{FactSource: FactSourcePuppetDB} },
		"missing capture provenance": func(e *Envelope) { e.Capture = nil },
		"fact source": func(e *Envelope) {
			e.Capture = &CaptureProvenance{RequestedAPI: CompilerAPIv3, EffectiveAPI: CompilerAPIv3}
		},
		"identity":    func(e *Envelope) { e.InputFactsetIdentity = "" },
		"timestamp":   func(e *Envelope) { e.CapturedAt = "" },
		"environment": func(e *Envelope) { e.RequestedEnvironment = "different" },
	} {
		t.Run(name, func(t *testing.T) {
			env := base
			change(&env)
			if err := Validate(env, KindCatalog, env.Target); err == nil {
				t.Fatal("accepted invalid metadata")
			}
		})
	}
}

// validCaptureProvenance is the capture provenance of an ordinary v4
// capture: no fallback, trusted facts supplied by the input factset.
func validCaptureProvenance() *CaptureProvenance {
	return &CaptureProvenance{
		RequestedAPI:       CompilerAPIv4,
		EffectiveAPI:       CompilerAPIv4,
		TrustedFactsSource: TrustedFactsProvided,
		FactSource:         FactSourcePuppetDB,
	}
}

// TestCaptureProvenanceConsistency covers the request sequences a
// compiler adapter cannot have produced. Each is a snapshot claiming an
// audit record of something that did not happen, which is exactly the
// class of defect a capture envelope exists to prevent.
func TestCaptureProvenanceConsistency(t *testing.T) {
	base := validFactsetEnvelope(t)
	base.Kind, base.Source.Kind = KindCatalog, "compiler"
	base.Payload = json.RawMessage(`{"certname":"web-01.example.test","environment":"production"}`)
	base.RequestedEnvironment, base.InputFactsetIdentity = "production", "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	valid := map[string]CaptureProvenance{
		"v4": *validCaptureProvenance(),
		"v4 by compiler lookup": {RequestedAPI: CompilerAPIv4, EffectiveAPI: CompilerAPIv4,
			TrustedFactsSource: TrustedFactsCompilerLookup, FactSource: FactSourceFile},
		"configured v3": {RequestedAPI: CompilerAPIv3, EffectiveAPI: CompilerAPIv3, FactSource: FactSourcePuppetDB},
		"v4 fallback to v3": {RequestedAPI: CompilerAPIv4, EffectiveAPI: CompilerAPIv3,
			FellBackFromV4: true, FactSource: FactSourcePuppetDB},
	}
	for name, provenance := range valid {
		t.Run(name, func(t *testing.T) {
			env := base
			env.Capture = &provenance
			if err := Validate(env, KindCatalog, env.Target); err != nil {
				t.Fatalf("rejected a capture the compiler adapter can produce: %v", err)
			}
		})
	}

	invalid := map[string]CaptureProvenance{
		"fallback without a v4 request": {RequestedAPI: CompilerAPIv3, EffectiveAPI: CompilerAPIv3,
			FellBackFromV4: true, FactSource: FactSourcePuppetDB},
		"fallback that stayed on v4": {RequestedAPI: CompilerAPIv4, EffectiveAPI: CompilerAPIv4,
			FellBackFromV4: true, TrustedFactsSource: TrustedFactsProvided, FactSource: FactSourcePuppetDB},
		"downgrade without a recorded fallback": {RequestedAPI: CompilerAPIv4, EffectiveAPI: CompilerAPIv3,
			FactSource: FactSourcePuppetDB},
		"upgraded v3 request": {RequestedAPI: CompilerAPIv3, EffectiveAPI: CompilerAPIv4,
			TrustedFactsSource: TrustedFactsProvided, FactSource: FactSourcePuppetDB},
		"v3 claiming trusted facts": {RequestedAPI: CompilerAPIv3, EffectiveAPI: CompilerAPIv3,
			TrustedFactsSource: TrustedFactsProvided, FactSource: FactSourcePuppetDB},
		"v4 without trusted facts": {RequestedAPI: CompilerAPIv4, EffectiveAPI: CompilerAPIv4,
			FactSource: FactSourcePuppetDB},
		"unsupported trusted-fact source": {RequestedAPI: CompilerAPIv4, EffectiveAPI: CompilerAPIv4,
			TrustedFactsSource: "guessed", FactSource: FactSourcePuppetDB},
		"unsupported fact source": {RequestedAPI: CompilerAPIv4, EffectiveAPI: CompilerAPIv4,
			TrustedFactsSource: TrustedFactsProvided, FactSource: "invented"},
	}
	for name, provenance := range invalid {
		t.Run(name, func(t *testing.T) {
			env := base
			env.Capture = &provenance
			if err := Validate(env, KindCatalog, env.Target); err == nil {
				t.Fatal("accepted capture provenance describing a request sequence that cannot happen")
			}
		})
	}
}
