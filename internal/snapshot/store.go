package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrExists is returned by Write when path already exists and replace
// was not requested: "Capture refuses to overwrite a snapshot unless
// --replace is supplied." Callers can check for this with errors.Is to
// distinguish overwrite refusal from any other write failure.
var ErrExists = errors.New("snapshot: destination already exists (use --replace to overwrite)")

// Write atomically writes env to path as canonical, checksum-verified
// JSON:
//
//   - refuses to overwrite an existing file at path unless replace is
//     true (returns ErrExists, wrapped, without touching path);
//   - writes to a same-directory temporary file, mode 0600;
//   - fsyncs the temporary file's contents before renaming;
//   - atomically renames the temporary file onto path (os.Rename, which
//     is atomic within one filesystem/directory on every platform Go
//     targets);
//   - best-effort fsyncs the containing directory afterward, so the
//     rename itself is durable — this is skipped, without failing the
//     write, on platforms/filesystems that reject an fsync on a
//     directory file descriptor (e.g. this is a documented no-op on
//     Windows; some filesystems return ENOTSUP), since a missing durable-
//     rename guarantee on such a platform is a pre-existing platform
//     limitation this package cannot fix, not a correctness regression
//     introduced here.
//
// Write does not itself verify env.PayloadChecksum against env.Payload —
// callers are expected to have just computed it via Checksum (see the
// capture workflow) — but it does refuse to write an envelope whose
// checksum field is empty, since that would silently produce a snapshot
// Load could never validate.
//
// Write creates path's containing directory (and any missing parents,
// mode 0700) if it does not already exist. Nothing states whether a
// first-time capture must have its destination directory pre-created by
// the operator or by PIACE itself, and requiring the operator to
// pre-create every per-target snapshot directory before the very command
// that populates it can run for the first time would make the snapshot
// workflow (CI refreshes catalog snapshots after a merge to the baseline
// branch) fail on a repository's first capture. So this package creates
// the directory rather than requiring that out-of-band step. This has no
// bearing on overwrite protection: the file-exists check above still
// runs first and is unaffected by whether the directory already existed.
func Write(path string, env Envelope, replace bool) error {
	if env.PayloadChecksum == "" {
		return fmt.Errorf("snapshot: writing %s: envelope has no payload_checksum set", path)
	}

	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%w: %s", ErrExists, path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("snapshot: checking %s: %w", path, err)
		}
	}

	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("snapshot: encoding envelope for %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("snapshot: creating directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".piace-snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("snapshot: creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	// Ensure the temp file never survives a failed write, but do not
	// clobber a successfully renamed file: once the rename below succeeds,
	// tmpPath no longer refers to anything, so this Remove is a harmless
	// no-op.
	defer os.Remove(tmpPath)

	writeErr := func() error {
		if err := tmp.Chmod(0o600); err != nil {
			return fmt.Errorf("setting temp file mode: %w", err)
		}
		if _, err := tmp.Write(data); err != nil {
			return fmt.Errorf("writing temp file: %w", err)
		}
		if err := tmp.Sync(); err != nil {
			return fmt.Errorf("fsyncing temp file: %w", err)
		}
		return tmp.Close()
	}()
	if writeErr != nil {
		tmp.Close()
		return fmt.Errorf("snapshot: writing %s: %w", path, writeErr)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("snapshot: renaming temp file onto %s: %w", path, err)
	}

	syncDirBestEffort(dir)
	return nil
}

// syncDirBestEffort opens dir and fsyncs it so the preceding atomic
// rename is durable against a crash. Any failure (permission, or a
// platform or filesystem that does not support fsync on a directory
// descriptor at all) is silently ignored: this is a durability
// best-effort, not a correctness requirement Write's success depends on,
// and the rename itself has already completed and is visible to any
// reader by the time this runs.
func syncDirBestEffort(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}

// Load reads path, decodes it as an Envelope, and verifies its
// format_version and payload_checksum: "Reuse validates version, kind,
// target, checksum, required metadata, and file decoding before it is
// accepted." Load itself checks format_version and the checksum (the two
// invariants that apply to every envelope regardless of caller intent);
// Kind/target/required-metadata/baseline- environment checks depend on
// what the caller expects and are Validate's job.
//
// On any failure, Load returns a zero Envelope: it never returns a
// partially-validated Envelope for a caller to accidentally use.
func Load(path string) (Envelope, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Envelope{}, fmt.Errorf("snapshot: reading %s: %w", path, err)
	}

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, fmt.Errorf("snapshot: decoding %s: %w", path, err)
	}

	if env.FormatVersion != FormatVersion {
		return Envelope{}, fmt.Errorf("snapshot: %s: unsupported format_version %d, expected %d",
			path, env.FormatVersion, FormatVersion)
	}

	if env.PayloadChecksum == "" {
		return Envelope{}, fmt.Errorf("snapshot: %s: envelope has no payload_checksum", path)
	}
	want, err := Checksum(env.Payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("snapshot: %s: computing payload checksum: %w", path, err)
	}
	if want != env.PayloadChecksum {
		return Envelope{}, fmt.Errorf("snapshot: %s: payload checksum mismatch: envelope declares %s, computed %s",
			path, env.PayloadChecksum, want)
	}

	return env, nil
}

// Validate checks env against the caller's expectations before it is
// reused: kind, target identity, and, for a catalog envelope, the
// catalog-only mandatory fields (RequestedEnvironment,
// CompilerAPIVersion, InputFactsetIdentity) plus a well-formed
// CapturedAt timestamp.
//
// wantTarget is compared against env.Target exactly (case-sensitive; a
// certname is not case-folded anywhere else in this codebase either).
// Validate does not check env.FormatVersion or env.PayloadChecksum —
// Load already enforces both unconditionally, and Validate is meant to be
// callable on any Envelope Load has already returned, not to re-verify
// what Load guarantees.
func Validate(env Envelope, wantKind Kind, wantTarget string) error {
	if env.Kind != wantKind {
		return fmt.Errorf("snapshot: target %q: envelope kind %q does not match expected kind %q",
			wantTarget, env.Kind, wantKind)
	}
	if env.Target != wantTarget {
		return fmt.Errorf("snapshot: envelope target %q does not match expected target %q",
			env.Target, wantTarget)
	}
	if _, err := time.Parse(time.RFC3339, env.CapturedAt); err != nil {
		return fmt.Errorf("snapshot: target %q: captured_at %q is not a valid RFC 3339 timestamp: %w",
			wantTarget, env.CapturedAt, err)
	}
	if env.Kind == KindCatalog {
		if env.RequestedEnvironment == "" {
			return fmt.Errorf("snapshot: target %q: catalog envelope is missing requested_environment", wantTarget)
		}
		if env.CompilerAPIVersion == "" {
			return fmt.Errorf("snapshot: target %q: catalog envelope is missing compiler_api", wantTarget)
		}
		if env.InputFactsetIdentity == "" {
			return fmt.Errorf("snapshot: target %q: catalog envelope is missing input_factset_identity", wantTarget)
		}
	}
	return nil
}
