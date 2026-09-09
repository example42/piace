package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/example42/piace/internal/artifact"
	"github.com/example42/piace/internal/limits"
)

// readBounded reads at most max bytes from path and refuses a larger
// file rather than allocating it. A snapshot is written by PIACE, but it
// is read back from wherever a repository put it, and "it is our own
// format" is not a size guarantee.
func readBounded(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, fmt.Errorf("the file exceeds the %d-byte snapshot limit", max)
	}
	return data, nil
}

// ErrExists is returned by Write when path already exists and replace
// was not requested: "Capture refuses to overwrite a snapshot unless
// --replace is supplied." Callers can check for this with errors.Is to
// distinguish overwrite refusal from any other write failure.
var ErrExists = errors.New("snapshot: destination already exists (use --replace to overwrite)")

// Write publishes env at path as canonical, checksum-verified JSON,
// through internal/artifact: a same-directory temporary file at mode
// 0600, fsynced and then atomically put in place, so a concurrent reader
// sees either the previous snapshot or the complete new one.
//
// When replace is false the publication itself refuses an existing
// destination ("Capture refuses to overwrite a snapshot unless --replace
// is supplied"), and the returned error wraps ErrExists. The refusal and
// the write are one operation, so two capture runs racing for one
// destination cannot both believe they succeeded; see artifact.WriteNew.
//
// Write does not itself verify env.PayloadChecksum against env.Payload,
// callers being expected to have just computed it via Checksum (see the
// capture workflow), but it does refuse to write an envelope whose
// checksum field is empty, since that would silently produce a snapshot
// Load could never validate.
//
// The containing directory is created when absent, per artifact.Write:
// requiring the operator to pre-create every per-target snapshot
// directory before the command that populates it can run for the first
// time would make a repository's first capture fail. Overwrite
// protection is unaffected, being a property of the publication rather
// than of the directory.
func Write(path string, env Envelope, replace bool) error {
	if env.PayloadChecksum == "" {
		return fmt.Errorf("snapshot: writing %s: envelope has no payload_checksum set", path)
	}

	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("snapshot: encoding envelope for %s: %w", path, err)
	}

	if replace {
		if err := artifact.Write(path, data, 0o600); err != nil {
			return fmt.Errorf("snapshot: writing %s: %w", path, err)
		}
		return nil
	}
	if err := artifact.WriteNew(path, data, 0o600); err != nil {
		if errors.Is(err, artifact.ErrExists) {
			// ErrExists stays this package's own sentinel rather than the
			// artifact one: callers already match on it, and a snapshot
			// refusal names the flag that lifts it.
			return fmt.Errorf("%w: %s", ErrExists, path)
		}
		return fmt.Errorf("snapshot: writing %s: %w", path, err)
	}
	return nil
}

// Load reads path, decodes it as an Envelope, and performs the
// integrity check: "Reuse validates version, kind, target, checksum,
// required metadata, and file decoding before it is accepted." Load
// itself checks format_version and the payload checksum (the two
// invariants that apply to every envelope regardless of caller intent);
// the attribution checks (kind, target, required metadata, provenance
// consistency, baseline environment) depend on what the caller expects
// and are Validate's job. See the package comment for why the two stay
// separate.
//
// On any failure, Load returns a zero Envelope: it never returns a
// partially-validated Envelope for a caller to accidentally use.
func Load(path string) (Envelope, error) {
	data, err := readBounded(path, limits.Snapshot)
	if err != nil {
		return Envelope{}, fmt.Errorf("snapshot: reading %s: %w", path, err)
	}

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, fmt.Errorf("snapshot: decoding %s: %w", path, err)
	}

	if env.FormatVersion != FormatVersion {
		// Naming the remedy matters here more than in the other failures:
		// an unsupported version is what an operator sees after upgrading
		// PIACE with snapshots already in the repository, and the file is
		// not damaged, only written against a schema this build no longer
		// reads. Recapture is the fix, and the message says so rather than
		// leaving it to be inferred from a version number.
		return Envelope{}, fmt.Errorf("snapshot: %s: unsupported format_version %d, expected %d; recapture the snapshot with `piace capture`",
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

// Validate is the attribution check (see the package comment): kind,
// source kind, envelope/payload target agreement, and, for a catalog
// envelope, the catalog-only mandatory fields (RequestedEnvironment,
// Capture, InputFactsetIdentity), the internal consistency of the
// capture provenance, and a well-formed CapturedAt timestamp.
//
// wantTarget is compared against env.Target exactly, case-sensitively, a
// certname not being case-folded anywhere else in this codebase either.
// Validate does not check env.FormatVersion or env.PayloadChecksum: Load
// already enforces both unconditionally as the integrity check, and
// Validate is meant to be callable on any Envelope Load has already
// returned rather than to re-verify what Load guarantees.
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
	if env.Target == "" {
		return fmt.Errorf("snapshot: missing target")
	}
	if env.Kind != KindFactset && env.Kind != KindCatalog {
		return fmt.Errorf("snapshot: unsupported kind")
	}
	if (env.Kind == KindFactset && env.Source.Kind != "puppetdb") || (env.Kind == KindCatalog && env.Source.Kind != "compiler") {
		return fmt.Errorf("snapshot: invalid source kind")
	}
	var payload struct {
		Certname    string `json:"certname"`
		Environment string `json:"environment"`
	}
	if json.Unmarshal(env.Payload, &payload) != nil || payload.Certname != env.Target {
		return fmt.Errorf("snapshot: payload certname does not match envelope target")
	}
	if env.Kind == KindCatalog && payload.Environment != env.RequestedEnvironment {
		return fmt.Errorf("snapshot: payload environment does not match requested_environment")
	}
	if env.Kind == KindCatalog {
		if env.RequestedEnvironment == "" {
			return fmt.Errorf("snapshot: target %q: catalog envelope is missing requested_environment", wantTarget)
		}
		if env.InputFactsetIdentity == "" {
			return fmt.Errorf("snapshot: target %q: catalog envelope is missing input_factset_identity", wantTarget)
		}
		if err := validateCaptureProvenance(env.Capture, wantTarget); err != nil {
			return err
		}
	}
	return nil
}

// validateCaptureProvenance checks a catalog envelope's capture
// provenance for internal consistency. The relationships it enforces are
// the ones the compiler adapter can actually produce, so a snapshot
// whose provenance describes a request sequence that cannot have
// happened is rejected as inconsistent rather than reused as an audit
// record of something else:
//
//   - both API values are supported (v3 or v4);
//   - fell_back_from_v4 means exactly what it says, a v4 request that
//     ended up compiling through v3, so it requires requested v4 and
//     effective v3;
//   - a requested v3 stays v3, PIACE never upgrading a request;
//   - without a fallback, the effective API is the requested one;
//   - trusted_facts_source is a v4 concept (v3 has no trusted-fact
//     request field), so it is required for an effective-v4 capture, one
//     of the two supported values, and absent for v3;
//   - fact_source names one of the two supported fact sources.
func validateCaptureProvenance(p *CaptureProvenance, wantTarget string) error {
	if p == nil {
		return fmt.Errorf("snapshot: target %q: catalog envelope is missing capture provenance", wantTarget)
	}
	supportedAPI := func(api CompilerAPI) bool { return api == CompilerAPIv3 || api == CompilerAPIv4 }
	if !supportedAPI(p.RequestedAPI) || !supportedAPI(p.EffectiveAPI) {
		return fmt.Errorf("snapshot: target %q: catalog envelope capture provenance has missing or unsupported requested_api/effective_api", wantTarget)
	}
	switch {
	case p.FellBackFromV4 && (p.RequestedAPI != CompilerAPIv4 || p.EffectiveAPI != CompilerAPIv3):
		return fmt.Errorf("snapshot: target %q: catalog envelope records fell_back_from_v4 without a v4 request compiled through v3", wantTarget)
	case !p.FellBackFromV4 && p.RequestedAPI != p.EffectiveAPI:
		return fmt.Errorf("snapshot: target %q: catalog envelope records requested_api %q and effective_api %q without a recorded fallback",
			wantTarget, p.RequestedAPI, p.EffectiveAPI)
	}
	switch p.EffectiveAPI {
	case CompilerAPIv4:
		if p.TrustedFactsSource != TrustedFactsProvided && p.TrustedFactsSource != TrustedFactsCompilerLookup {
			return fmt.Errorf("snapshot: target %q: catalog envelope has missing or unsupported trusted_facts_source for a v4 capture", wantTarget)
		}
	case CompilerAPIv3:
		if p.TrustedFactsSource != "" {
			return fmt.Errorf("snapshot: target %q: catalog envelope records trusted_facts_source for a v3 capture, which has no trusted-fact request field", wantTarget)
		}
	}
	if p.FactSource != FactSourcePuppetDB && p.FactSource != FactSourceFile {
		return fmt.Errorf("snapshot: target %q: catalog envelope capture provenance has missing or unsupported fact_source", wantTarget)
	}
	return nil
}
