package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAcceptance_DebugPrintsSafeRequestMetadata covers the --debug
// option's contract: one stderr line per service request, carrying
// enough to diagnose a wire-shape mismatch (status, timing, sizes, and
// the response body's top-level JSON keys) and nothing the redaction
// rules forbid in a log.
func TestAcceptance_DebugPrintsSafeRequestMetadata(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))

	got := h.compare(t, "--debug")

	for _, want := range []string{
		"POST https://" + h.compilerServer.Listener.Addr().String() + "/puppet/v4/catalog",
		"/pdb/query/v4/catalogs/web-01.example.test",
		"-> 200 in ",
		"top-level keys: catalog",
	} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("--debug stderr does not contain %q:\n%s", want, got.stderr)
		}
	}

	// The debug lines are metadata only: no catalog member below the top
	// level, and no resource content, reaches stderr.
	for _, forbidden := range []string{"Service[nginx]", "\"resources\"", "ensure"} {
		if strings.Contains(got.stderr, forbidden) {
			t.Errorf("--debug stderr leaked body content %q:\n%s", forbidden, got.stderr)
		}
	}
}

// TestAcceptance_DebugLeavesStdoutUnchanged asserts observation is inert:
// enabling --debug must not alter the report or the exit code, since an
// operator turns it on precisely to inspect a run they need to reproduce.
func TestAcceptance_DebugLeavesStdoutUnchanged(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))

	plain := h.compare(t)
	debugged := h.compare(t, "--debug")

	if plain.code != debugged.code {
		t.Errorf("exit code changed with --debug: %d -> %d", plain.code, debugged.code)
	}
	if plain.stdout != debugged.stdout {
		t.Errorf("stdout changed with --debug:\n%s\n---\n%s", plain.stdout, debugged.stdout)
	}
	if plain.stderr != "" {
		t.Errorf("stderr is not empty without --debug:\n%s", plain.stderr)
	}
}

// TestAcceptance_DebugDumpDirWritesRestrictedFiles covers the other side
// of the redaction boundary: raw bodies land in 0600 files under an
// operator-named directory, never on stdout or stderr, and the command
// says out loud that they can contain sensitive values.
func TestAcceptance_DebugDumpDirWritesRestrictedFiles(t *testing.T) {
	h := newHarness(t)
	h.seedTarget("web-01.example.test", baseResources(), baseResources(), baseEdges())
	h.writeConfigs(t, targetsYAML(defaultDefaults, target("web-01.example.test")))

	dumpDir := h.path("dump")
	got := h.compare(t, "--debug-dump-dir", dumpDir)

	if !strings.Contains(got.stderr, "may contain sensitive catalog values") {
		t.Errorf("no warning printed for --debug-dump-dir:\n%s", got.stderr)
	}

	entries, err := os.ReadDir(dumpDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dumpDir, err)
	}
	if len(entries) == 0 {
		t.Fatal("--debug-dump-dir produced no files")
	}

	dirInfo, err := os.Stat(dumpDir)
	if err != nil {
		t.Fatalf("Stat(%s): %v", dumpDir, err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("dump directory mode = %04o, want 0700", perm)
	}

	sawV4Response := false
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Info(%s): %v", e.Name(), err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", e.Name(), perm)
		}
		if !strings.HasSuffix(e.Name(), "puppet-v4-catalog.response.json") {
			continue
		}
		sawV4Response = true
		body, err := os.ReadFile(filepath.Join(dumpDir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", e.Name(), err)
		}
		// The dump is the verbatim response, envelope included, which is the
		// whole point of having it.
		if !strings.HasPrefix(strings.TrimSpace(string(body)), `{"catalog":`) {
			t.Errorf("v4 response dump is not the raw enveloped body:\n%s", body)
		}
	}
	if !sawV4Response {
		t.Errorf("no v4 catalog response dump written; got %v", names(entries))
	}

	// Nothing raw reaches the console.
	if strings.Contains(got.stderr, "Service") || strings.Contains(got.stdout, `{"catalog":`) {
		t.Error("raw body content reached stdout/stderr")
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
