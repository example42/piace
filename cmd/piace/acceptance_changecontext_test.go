package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/exitcode"
)

// changeContextRepo builds a two-branch fixture repository and returns
// its path. The committer identity and the explicit initial branch keep
// it independent of the developer's global git config.
func changeContextRepo(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}

	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Someone", "GIT_AUTHOR_EMAIL=someone@example.test",
			"GIT_COMMITTER_NAME=Someone", "GIT_COMMITTER_EMAIL=someone@example.test",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	run("init", "--initial-branch=main")
	write("common.yaml", "---\n")
	run("add", ".")
	run("commit", "-m", "initial")
	run("checkout", "-b", "feature-123")
	write("sudo.pp", "class profile::sudo {}\n")
	run("add", ".")
	// A subject carrying a double quote is what YAML quoting exists for,
	// and a tab is what the record separator exists for: either would
	// have ended a scalar early or invented a field in a hand-rolled
	// emitter.
	run("commit", "-m", "profile::sudo: allow \"ops\"\tto restart nginx")
	return repo
}

// runChangeContextIn runs the subcommand with the fixture repository as
// the working directory, capturing stdout, because the command reads the
// repository it is standing in.
func runChangeContextIn(t *testing.T, repo string, args ...string) (string, exitcode.Code) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	outPath := filepath.Join(t.TempDir(), "stdout")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	errPath := filepath.Join(t.TempDir(), "stderr")
	errFile, err := os.Create(errPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	code := run(append([]string{"change-context"}, args...), out, errFile)
	out.Close()
	errFile.Close()

	if code != exitcode.Success {
		raw, _ := os.ReadFile(errPath)
		return string(raw), code
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(raw), code
}

// TestAcceptance_ChangeContextRoundTripsThroughExplain is the assertion
// that matters: what the generator writes is what `explain --change`
// reads. A generator whose output that decoder refuses would fail every
// pipeline following the documentation, and nothing else would notice.
func TestAcceptance_ChangeContextRoundTripsThroughExplain(t *testing.T) {
	repo := changeContextRepo(t)
	t.Setenv("PR_TITLE", "  Allow ops to restart nginx  ")
	t.Setenv("PR_BODY", "Two lines.\nThe second one: with a colon.\n")

	out, code := runChangeContextIn(t, repo,
		"--base-ref", "main", "--title-env", "PR_TITLE", "--description-env", "PR_BODY")
	if code != exitcode.Success {
		t.Fatalf("exit = %d, want 0:\n%s", code, out)
	}

	path := filepath.Join(t.TempDir(), "change.yaml")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cc, err := assess.LoadChangeContext(path)
	if err != nil {
		t.Fatalf("LoadChangeContext over the generator's own output: %v\n%s", err, out)
	}

	if !cc.Present || cc.BaseRef != "main" || cc.HeadRef != "feature-123" {
		t.Errorf("change context = %+v", cc)
	}
	if len(cc.Commits) != 1 {
		t.Fatalf("commits = %d, want 1 (only the branch's own commit):\n%s", len(cc.Commits), out)
	}
	if want := "profile::sudo: allow \"ops\"\tto restart nginx"; cc.Commits[0].Subject != want {
		t.Errorf("subject = %q, want %q", cc.Commits[0].Subject, want)
	}
	if cc.Commits[0].Author != "Someone" {
		t.Errorf("author = %q", cc.Commits[0].Author)
	}
	if len(cc.ChangedPaths) != 1 || cc.ChangedPaths[0] != "sudo.pp" {
		t.Errorf("changed paths = %v, want [sudo.pp]", cc.ChangedPaths)
	}
	if cc.Title != "Allow ops to restart nginx" {
		t.Errorf("title = %q, want it trimmed", cc.Title)
	}
	if cc.Description != "Two lines.\nThe second one: with a colon." {
		t.Errorf("description = %q", cc.Description)
	}
}

// TestAcceptance_ChangeContextNeverTakesFreeTextOnTheCommandLine locks
// the reason the subcommand exists. A --title flag would let a CI system
// that substitutes a pull request title into script text before a shell
// runs turn that title into a command.
func TestAcceptance_ChangeContextNeverTakesFreeTextOnTheCommandLine(t *testing.T) {
	repo := changeContextRepo(t)
	for _, flagName := range []string{"--title", "--description"} {
		t.Run(flagName, func(t *testing.T) {
			out, code := runChangeContextIn(t, repo, "--base-ref", "main", flagName, "anything")
			if code != exitcode.OperationalError {
				t.Fatalf("exit = %d, want 30: %s must not exist\n%s", code, flagName, out)
			}
		})
	}
}

func TestAcceptance_ChangeContextUsageErrors(t *testing.T) {
	repo := changeContextRepo(t)
	t.Setenv("PR_TITLE", "a title")
	t.Setenv("BASE", "main")

	tests := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{
			name:    "no base ref",
			args:    []string{"--title-env", "PR_TITLE"},
			wantMsg: "--base-ref or --base-ref-env is required",
		},
		{
			name:    "both base ref forms",
			args:    []string{"--base-ref", "main", "--base-ref-env", "BASE"},
			wantMsg: "not both",
		},
		{
			name:    "both title forms",
			args:    []string{"--base-ref", "main", "--title-env", "PR_TITLE", "--title-file", "/dev/null"},
			wantMsg: "not both",
		},
		{
			name:    "base ref variable unset",
			args:    []string{"--base-ref-env", "PIACE_UNSET_BASE"},
			wantMsg: "is unset or empty",
		},
		{
			name:    "unknown ref",
			args:    []string{"--base-ref", "no-such-branch"},
			wantMsg: "git merge-base",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runChangeContextIn(t, repo, tc.args...)
			if code != exitcode.OperationalError {
				t.Fatalf("exit = %d, want 30\n%s", code, out)
			}
			if !strings.Contains(out, tc.wantMsg) {
				t.Errorf("stderr = %q, want it to contain %q", out, tc.wantMsg)
			}
		})
	}
}

// TestAcceptance_ChangeContextOmitsUnsetFreeText asserts that a change
// with no title or description is ordinary rather than an error, and
// that the generated document does not spell the empty fields.
func TestAcceptance_ChangeContextOmitsUnsetFreeText(t *testing.T) {
	repo := changeContextRepo(t)
	out, code := runChangeContextIn(t, repo, "--base-ref", "main")
	if code != exitcode.Success {
		t.Fatalf("exit = %d, want 0:\n%s", code, out)
	}
	for _, key := range []string{"title:", "description:"} {
		if strings.Contains(out, key) {
			t.Errorf("output spells %q for an unset field:\n%s", key, out)
		}
	}
}
