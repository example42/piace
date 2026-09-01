package snapshot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func validFactsetEnvelope(t *testing.T) Envelope {
	t.Helper()
	payload := json.RawMessage(`{"certname":"web-01.example.test","values":{"os":"linux"}}`)
	sum, err := Checksum(payload)
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	return Envelope{
		FormatVersion:   FormatVersion,
		Kind:            KindFactset,
		Target:          "web-01.example.test",
		Source:          Source{Kind: "puppetdb", Producer: "puppetdb-01"},
		CapturedAt:      "2026-08-24T00:00:00Z",
		PayloadChecksum: sum,
		Payload:         payload,
	}
}

func TestWrite_ThenLoad_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-01.example.test.json")
	env := validFactsetEnvelope(t)

	if err := Write(path, env, false); err != nil {
		t.Fatalf("Write: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Target != env.Target || loaded.PayloadChecksum != env.PayloadChecksum {
		t.Errorf("loaded = %+v, want target/checksum to match %+v", loaded, env)
	}
	if string(loaded.Payload) != string(env.Payload) {
		t.Errorf("loaded.Payload = %s, want %s", loaded.Payload, env.Payload)
	}
}

// TestWrite_CorrectFinalPermissions verifies the written file has mode
// 0600.
func TestWrite_CorrectFinalPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode bits are not meaningful on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	if err := Write(path, validFactsetEnvelope(t), false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want %o", perm, 0o600)
	}
}

// TestWrite_NoTempFileLeftBehind verifies the same-directory temp file
// used for the atomic write does not survive a successful write.
func TestWrite_NoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	if err := Write(path, validFactsetEnvelope(t), false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "snap.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("directory contents = %v, want only [snap.json]", names)
	}
}

// TestWrite_RefusesOverwriteWithoutReplace verifies the
// overwrite-protection rule.
func TestWrite_RefusesOverwriteWithoutReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	env := validFactsetEnvelope(t)
	if err := Write(path, env, false); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	err := Write(path, env, false)
	if err == nil {
		t.Fatal("expected an error overwriting without replace, got nil")
	}
	if !errors.Is(err, ErrExists) {
		t.Errorf("error = %v, want errors.Is(err, ErrExists)", err)
	}
}

// TestWrite_SucceedsWithReplace verifies --replace allows an overwrite,
// and that the new content actually takes effect.
func TestWrite_SucceedsWithReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	env := validFactsetEnvelope(t)
	if err := Write(path, env, false); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	updatedPayload := json.RawMessage(`{"certname":"web-01.example.test","values":{"os":"windows"}}`)
	sum, err := Checksum(updatedPayload)
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	env.Payload = updatedPayload
	env.PayloadChecksum = sum

	if err := Write(path, env, true); err != nil {
		t.Fatalf("replace Write: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(loaded.Payload) != string(updatedPayload) {
		t.Errorf("loaded.Payload = %s, want %s", loaded.Payload, updatedPayload)
	}
}

// TestWrite_CreatesMissingDestinationDirectory verifies a first-time
// capture whose destination directory does not exist yet succeeds by
// creating it, rather than requiring the operator to pre-create every
// per-target snapshot directory.
func TestWrite_CreatesMissingDestinationDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshots", "facts", "web-01.example.test.json")
	if err := Write(path, validFactsetEnvelope(t), false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat: %v", err)
	}
}

// TestLoad_RejectsWrongFormatVersion verifies Load rejects an envelope
// declaring an unsupported format_version.
func TestLoad_RejectsWrongFormatVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	env := validFactsetEnvelope(t)
	env.FormatVersion = 99
	writeRawEnvelope(t, path, env)

	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for wrong format_version, got nil")
	}
}

// TestLoad_RejectsWrongChecksum verifies Load rejects an envelope whose
// declared checksum does not match its payload (tampered or corrupted
// file).
func TestLoad_RejectsWrongChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	env := validFactsetEnvelope(t)
	env.PayloadChecksum = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	writeRawEnvelope(t, path, env)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for checksum mismatch, got nil")
	}
}

// TestLoad_RejectsMalformedJSON verifies Load rejects a file that is not
// valid JSON at all.
func TestLoad_RejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	if err := os.WriteFile(path, []byte(`{not valid json`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}
}

// TestLoad_MissingFile verifies Load surfaces a clear error for a
// nonexistent snapshot file.
func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "nope.json")); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}

// TestValidate_AcceptsMatchingFactsetEnvelope verifies a well-formed
// factset envelope passes Validate for its own kind/target.
func TestValidate_AcceptsMatchingFactsetEnvelope(t *testing.T) {
	env := validFactsetEnvelope(t)
	if err := Validate(env, KindFactset, "web-01.example.test"); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// TestValidate_RejectsKindMismatch verifies Validate rejects a factset
// envelope presented where a catalog was expected.
func TestValidate_RejectsKindMismatch(t *testing.T) {
	env := validFactsetEnvelope(t)
	if err := Validate(env, KindCatalog, "web-01.example.test"); err == nil {
		t.Fatal("expected an error for kind mismatch, got nil")
	}
}

// TestValidate_RejectsTargetMismatch verifies Validate rejects an envelope
// recorded for a different certname than expected.
func TestValidate_RejectsTargetMismatch(t *testing.T) {
	env := validFactsetEnvelope(t)
	if err := Validate(env, KindFactset, "other-host.example.test"); err == nil {
		t.Fatal("expected an error for target mismatch, got nil")
	}
}

// TestValidate_RejectsMissingCatalogMetadata verifies a catalog envelope
// missing its mandatory requested_environment/compiler_api/
// input_factset_identity fields is rejected.
func TestValidate_RejectsMissingCatalogMetadata(t *testing.T) {
	payload := json.RawMessage(`{"certname":"web-01.example.test"}`)
	sum, err := Checksum(payload)
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	env := Envelope{
		FormatVersion:   FormatVersion,
		Kind:            KindCatalog,
		Target:          "web-01.example.test",
		Source:          Source{Kind: "compiler"},
		CapturedAt:      "2026-08-24T00:00:00Z",
		PayloadChecksum: sum,
		Payload:         payload,
		// RequestedEnvironment/CompilerAPIVersion/InputFactsetIdentity
		// intentionally left empty.
	}
	if err := Validate(env, KindCatalog, "web-01.example.test"); err == nil {
		t.Fatal("expected an error for missing catalog metadata, got nil")
	}
}

// TestValidate_RejectsMalformedCapturedAt verifies a non-RFC3339
// captured_at value is rejected.
func TestValidate_RejectsMalformedCapturedAt(t *testing.T) {
	env := validFactsetEnvelope(t)
	env.CapturedAt = "not-a-timestamp"
	if err := Validate(env, KindFactset, "web-01.example.test"); err == nil {
		t.Fatal("expected an error for malformed captured_at, got nil")
	}
}

// writeRawEnvelope writes env to path without going through Write's
// checksum-presence guard, so tests can construct a deliberately invalid
// on-disk envelope for Load to reject.
func writeRawEnvelope(t *testing.T, path string, env Envelope) {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
