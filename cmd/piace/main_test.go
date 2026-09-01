package main

import (
	"os"
	"testing"

	"github.com/example42/piace/internal/exitcode"
)

// TestRun_NoArgsIsOperationalError verifies invoking piace with no
// subcommand is treated as an operational error (exit 30), never success.
func TestRun_NoArgsIsOperationalError(t *testing.T) {
	if got := run(nil, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(nil) = %d, want %d", got, exitcode.OperationalError)
	}
}

// TestRun_UnknownCommandIsOperationalError verifies an unrecognized
// subcommand is an operational error.
func TestRun_UnknownCommandIsOperationalError(t *testing.T) {
	if got := run([]string{"frobnicate"}, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(frobnicate) = %d, want %d", got, exitcode.OperationalError)
	}
}

// TestRun_HelpAndVersionAreSuccess verifies informational commands exit 0.
func TestRun_HelpAndVersionAreSuccess(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"version"}} {
		if got := run(args, os.Stdout, os.Stderr); got != exitcode.Success {
			t.Errorf("run(%v) = %d, want %d", args, got, exitcode.Success)
		}
	}
}

// TestRun_CompareMissingFlagsIsOperationalError verifies `compare` without
// required flags fails as an operational error rather than panicking or
// silently succeeding.
func TestRun_CompareMissingFlagsIsOperationalError(t *testing.T) {
	if got := run([]string{"compare"}, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(compare) = %d, want %d", got, exitcode.OperationalError)
	}
}

// TestRun_CompareMissingConfigFilesIsOperationalError verifies `compare`
// with flags pointing at nonexistent target/services files is an
// operational error. It exercises config resolution at the CLI boundary,
// distinguishing "config failed to load" from a panic.
func TestRun_CompareMissingConfigFilesIsOperationalError(t *testing.T) {
	args := []string{"compare", "--targets", "/nonexistent/targets.yaml", "--services", "/nonexistent/services.yaml"}
	if got := run(args, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(compare with missing config files) = %d, want %d", got, exitcode.OperationalError)
	}
}

// TestRun_CaptureFactsMissingFlagsIsOperationalError mirrors the compare
// case for `capture facts`.
func TestRun_CaptureFactsMissingFlagsIsOperationalError(t *testing.T) {
	if got := run([]string{"capture", "facts"}, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(capture facts) = %d, want %d", got, exitcode.OperationalError)
	}
}

// TestRun_CaptureCatalogMissingFlagsIsOperationalError mirrors the compare
// case for `capture catalog`.
func TestRun_CaptureCatalogMissingFlagsIsOperationalError(t *testing.T) {
	if got := run([]string{"capture", "catalog"}, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(capture catalog) = %d, want %d", got, exitcode.OperationalError)
	}
}

// TestRun_CaptureUnknownSubcommandIsOperationalError verifies `capture`
// with neither "facts" nor "catalog" is an operational error.
func TestRun_CaptureUnknownSubcommandIsOperationalError(t *testing.T) {
	if got := run([]string{"capture", "nonsense"}, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(capture nonsense) = %d, want %d", got, exitcode.OperationalError)
	}
	if got := run([]string{"capture"}, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(capture) = %d, want %d", got, exitcode.OperationalError)
	}
}

// TestRun_ExplainMissingFlagsIsOperationalError mirrors the compare case
// for `explain`, whose required flags are --json-in and --services.
func TestRun_ExplainMissingFlagsIsOperationalError(t *testing.T) {
	if got := run([]string{"explain"}, os.Stdout, os.Stderr); got != exitcode.OperationalError {
		t.Errorf("run(explain) = %d, want %d", got, exitcode.OperationalError)
	}
}
