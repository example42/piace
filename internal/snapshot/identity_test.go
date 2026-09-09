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
			env.CompilerAPIVersion = CompilerAPIv4
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
	base.RequestedEnvironment, base.CompilerAPIVersion, base.InputFactsetIdentity = "production", CompilerAPIv4, "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := Validate(base, KindCatalog, base.Target); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Envelope){
		"source":         func(e *Envelope) { e.Source.Kind = "" },
		"unknown source": func(e *Envelope) { e.Source.Kind = "unknown" },
		"api":            func(e *Envelope) { e.CompilerAPIVersion = "v5" },
		"missing api":    func(e *Envelope) { e.CompilerAPIVersion = "" },
		"identity":       func(e *Envelope) { e.InputFactsetIdentity = "" },
		"timestamp":      func(e *Envelope) { e.CapturedAt = "" },
		"environment":    func(e *Envelope) { e.RequestedEnvironment = "different" },
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
