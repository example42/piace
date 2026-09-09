package puppetdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/snapshot"
)

func writeFactsetSnapshot(t *testing.T, path, certname, environment string) {
	t.Helper()
	fs := Factset{
		Certname:          certname,
		Environment:       environment,
		ProducerTimestamp: "2015-06-04T15:27:56.893Z",
		Producer:          "compiler-01.example.test",
		Hash:              "deadbeef",
		Facts:             json.RawMessage(`{"data":[]}`),
	}
	payload, err := json.Marshal(fs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	sum, err := snapshot.Checksum(payload)
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	env := snapshot.Envelope{
		FormatVersion:   snapshot.FormatVersion,
		Kind:            snapshot.KindFactset,
		Target:          certname,
		Source:          snapshot.Source{Kind: "puppetdb"},
		CapturedAt:      "2026-08-24T00:00:00Z",
		PayloadChecksum: sum,
		Payload:         payload,
	}
	if err := snapshot.Write(path, env, false); err != nil {
		t.Fatalf("snapshot.Write: %v", err)
	}
}

func writeCatalogSnapshot(t *testing.T, path, certname, environment string) {
	t.Helper()
	writeCatalogSnapshotWithContent(t, path, certname, environment, nil)
}

// writeCatalogSnapshotWithContent writes a catalog snapshot carrying
// captured content evidence, so a test can put evidence of a shape the
// comparison could never use into an otherwise intact, correctly
// checksummed snapshot.
func writeCatalogSnapshotWithContent(t *testing.T, path, certname, environment string, captured map[string]model.ContentDigest) {
	t.Helper()
	cat := Catalog{
		Certname:          certname,
		Environment:       environment,
		ProducerTimestamp: "2014-10-13T20:46:00.000Z",
		Producer:          "compiler-01.example.test",
		Hash:              "cafebabe",
		Resources:         json.RawMessage(`[]`),
		Edges:             json.RawMessage(`[]`),
		CapturedContent:   captured,
	}
	payload, err := json.Marshal(cat)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	sum, err := snapshot.Checksum(payload)
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	env := snapshot.Envelope{
		FormatVersion:        snapshot.FormatVersion,
		Kind:                 snapshot.KindCatalog,
		Target:               certname,
		Source:               snapshot.Source{Kind: "compiler"},
		CapturedAt:           "2026-08-24T00:00:00Z",
		RequestedEnvironment: environment,
		Capture: &snapshot.CaptureProvenance{
			RequestedAPI:       snapshot.CompilerAPIv4,
			EffectiveAPI:       snapshot.CompilerAPIv4,
			TrustedFactsSource: snapshot.TrustedFactsProvided,
			FactSource:         snapshot.FactSourcePuppetDB,
		},
		InputFactsetIdentity: "sha256:abc",
		PayloadChecksum:      sum,
		Payload:              payload,
	}
	if err := snapshot.Write(path, env, false); err != nil {
		t.Fatalf("snapshot.Write: %v", err)
	}
}

func fileTarget(certname, factsFile, baselineFile, baselineEnv string) resolve.Target {
	return resolve.Target{
		Certname: certname,
		Facts:    resolve.Facts{Source: config.FactSourceFile, File: factsFile},
		Baseline: resolve.Baseline{
			Source:      config.BaselineSourceFile,
			Environment: baselineEnv,
			File:        baselineFile,
		},
	}
}

func TestFileSource_Load_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01.json")
	writeFactsetSnapshot(t, path, "web-01.example.test", "production")

	fsrc := NewFileSource()
	fs, prov, diag := fsrc.Load(context.Background(), fileTarget("web-01.example.test", path, "", "production"))
	if diag != nil {
		t.Fatalf("Load returned diagnostic: %+v", diag)
	}
	if fs.Certname != "web-01.example.test" {
		t.Errorf("Certname = %q", fs.Certname)
	}
	if prov.Kind != model.SourceKindFile {
		t.Errorf("prov.Kind = %q, want %q", prov.Kind, model.SourceKindFile)
	}
}

func TestFileSource_Load_MissingFile(t *testing.T) {
	dir := t.TempDir()
	fsrc := NewFileSource()
	_, _, diag := fsrc.Load(context.Background(), fileTarget("web-01.example.test", filepath.Join(dir, "nope.json"), "", "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for missing file, got nil")
	}
	if diag.Operation != model.OperationLoadFacts {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationLoadFacts)
	}
}

func TestFileSource_Load_TargetMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01.json")
	writeFactsetSnapshot(t, path, "other-host.example.test", "production")

	fsrc := NewFileSource()
	_, _, diag := fsrc.Load(context.Background(), fileTarget("web-01.example.test", path, "", "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for target mismatch, got nil")
	}
}

func TestFileSource_Load_TamperedChecksumRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01.json")
	writeFactsetSnapshot(t, path, "web-01.example.test", "production")

	// Tamper with the file's payload without updating the checksum.
	tamperFileContent(t, path, `"compiler-01.example.test"`, `"compiler-02.example.test"`)

	fsrc := NewFileSource()
	_, _, diag := fsrc.Load(context.Background(), fileTarget("web-01.example.test", path, "", "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for tampered checksum, got nil")
	}
}

func TestFileSource_LoadBaseline_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01-catalog.json")
	writeCatalogSnapshot(t, path, "web-01.example.test", "production")

	fsrc := NewFileSource()
	cat, prov, diag := fsrc.LoadBaseline(context.Background(), fileTarget("web-01.example.test", "", path, "production"))
	if diag != nil {
		t.Fatalf("LoadBaseline returned diagnostic: %+v", diag)
	}
	if cat.Certname != "web-01.example.test" {
		t.Errorf("Certname = %q", cat.Certname)
	}
	if prov.Kind != model.SourceKindFile {
		t.Errorf("prov.Kind = %q, want %q", prov.Kind, model.SourceKindFile)
	}
}

// TestFileSource_LoadBaseline_EnvironmentMismatchRejectedUnconditionally
// verifies the file-backed baseline source enforces its environment
// check strictly, unlike the "regardless of its environment" framing
// that applies to a live puppetdb source (see filesource.go's doc
// comment).
func TestFileSource_LoadBaseline_EnvironmentMismatchRejectedUnconditionally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01-catalog.json")
	writeCatalogSnapshot(t, path, "web-01.example.test", "feature-999")

	fsrc := NewFileSource()
	_, _, diag := fsrc.LoadBaseline(context.Background(), fileTarget("web-01.example.test", "", path, "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for baseline environment mismatch, got nil")
	}
	if diag.Operation != model.OperationLoadBaseline {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationLoadBaseline)
	}
}

func TestFileSource_LoadBaseline_WrongKindRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01.json")
	// A factset snapshot presented where a catalog snapshot is expected.
	writeFactsetSnapshot(t, path, "web-01.example.test", "production")

	fsrc := NewFileSource()
	_, _, diag := fsrc.LoadBaseline(context.Background(), fileTarget("web-01.example.test", "", path, "production"))
	if diag == nil {
		t.Fatal("expected a diagnostic for kind mismatch, got nil")
	}
}

// tamperFileContent replaces old with newStr in path's contents. It fails
// the test if old is not present so a test does not silently pass because
// its precondition changed.
func tamperFileContent(t *testing.T, path, old, newStr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("expected %q to contain %q", data, old)
	}
	tampered := strings.Replace(string(data), old, newStr, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestFileSource_LoadBaseline_RejectsUnusableCapturedContent verifies
// captured content evidence is shape-checked when the snapshot is
// loaded. A digest PIACE cannot use is a defect in the snapshot; letting
// it through would surface at comparison time as indeterminate content,
// which describes a snapshot that recorded no evidence at all.
func TestFileSource_LoadBaseline_RejectsUnusableCapturedContent(t *testing.T) {
	cases := map[string]model.ContentDigest{
		"unsupported algorithm": {Algorithm: "crc32", Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		"truncated digest":      {Algorithm: "sha256", Digest: "0123456789abcdef"},
		"non-hexadecimal":       {Algorithm: "sha256", Digest: strings.Repeat("z", 64)},
		"empty digest":          {Algorithm: "sha256", Digest: ""},
	}
	for name, digest := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "web-01-catalog.json")
			writeCatalogSnapshotWithContent(t, path, "web-01.example.test", "production",
				map[string]model.ContentDigest{"/etc/motd": digest})

			_, _, diag := NewFileSource().LoadBaseline(context.Background(),
				fileTarget("web-01.example.test", "", path, "production"))
			if diag == nil {
				t.Fatal("accepted captured content evidence the comparison could never use")
			}
			if !strings.Contains(diag.Message, "/etc/motd") {
				t.Errorf("the diagnostic does not identify the file it came from: %q", diag.Message)
			}
		})
	}
}

// TestFileSource_LoadBaseline_AcceptsSupportedCapturedContent keeps the
// check above from becoming a blanket rejection of captured evidence.
func TestFileSource_LoadBaseline_AcceptsSupportedCapturedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01-catalog.json")
	digest := model.ContentDigest{Algorithm: "sha256", Digest: strings.Repeat("ab", 32)}
	writeCatalogSnapshotWithContent(t, path, "web-01.example.test", "production",
		map[string]model.ContentDigest{"/etc/motd": digest})

	cat, _, diag := NewFileSource().LoadBaseline(context.Background(),
		fileTarget("web-01.example.test", "", path, "production"))
	if diag != nil {
		t.Fatalf("LoadBaseline returned diagnostic: %+v", diag)
	}
	if got := cat.CapturedContent["/etc/motd"]; got != digest {
		t.Errorf("captured content = %+v, want %+v", got, digest)
	}
}
