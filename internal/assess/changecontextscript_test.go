package assess

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// scripts/change-context.sh is shipped as the CI-facing way to produce a
// change context, so it is verified by loading its real output through
// LoadChangeContext rather than by reading it. A script that emits YAML
// this decoder refuses would fail every pipeline that followed the
// README, and nothing else in the suite would have noticed.
func TestTheShippedScriptProducesALoadableChangeContext(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "change-context.sh"))
	if err != nil {
		t.Fatalf("resolving the script path: %v", err)
	}

	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		// A committer identity and an explicit branch name keep the
		// fixture independent of the developer's global git config.
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
	// A subject carrying a double quote is the case the script's YAML
	// escaping exists for; an unescaped one would end the scalar early.
	run("commit", "-m", `profile::sudo: allow "ops" to restart nginx`)

	cmd := exec.Command(script, "main", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("change-context.sh: %v", err)
	}

	path := filepath.Join(t.TempDir(), "change.yaml")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("writing the change context: %v", err)
	}
	cc, err := LoadChangeContext(path)
	if err != nil {
		t.Fatalf("LoadChangeContext over the script's own output: %v\n%s", err, out)
	}

	if !cc.Present || cc.BaseRef != "main" || cc.HeadRef != "feature-123" {
		t.Errorf("change context = %+v", cc)
	}
	if len(cc.Commits) != 1 {
		t.Fatalf("commits = %d, want 1\n%s", len(cc.Commits), out)
	}
	if want := `profile::sudo: allow "ops" to restart nginx`; cc.Commits[0].Subject != want {
		t.Errorf("subject = %q, want %q", cc.Commits[0].Subject, want)
	}
	if len(cc.ChangedPaths) != 1 || cc.ChangedPaths[0] != "sudo.pp" {
		t.Errorf("changed_paths = %v, want [sudo.pp]", cc.ChangedPaths)
	}
}
