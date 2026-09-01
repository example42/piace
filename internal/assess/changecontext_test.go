package assess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// TestLoadChangeContextReadsACallerSuppliedChange: the
// fully populated shape PIACE documents for CI to produce.
func TestLoadChangeContextReadsACallerSuppliedChange(t *testing.T) {
	path := writeFile(t, t.TempDir(), "change.yaml", `
version: 1
change:
  base_ref: main
  head_ref: feature-123
  commits:
    - sha: abc123
      subject: "profile::sudo: allow ops group"
      author: someone
  changed_paths:
    - manifests/profile/sudo.pp
    - hieradata/common.yaml
  title: "Allow the ops group to sudo"
  description: "Adds the ops group to the sudoers template."
`)

	cc, err := LoadChangeContext(path)
	if err != nil {
		t.Fatalf("LoadChangeContext: %v", err)
	}
	if cc.BaseRef != "main" || cc.HeadRef != "feature-123" {
		t.Errorf("refs = %q..%q", cc.BaseRef, cc.HeadRef)
	}
	if len(cc.Commits) != 1 || cc.Commits[0].SHA != "abc123" || cc.Commits[0].Author != "someone" {
		t.Errorf("Commits = %+v", cc.Commits)
	}
	if cc.Commits[0].Subject != "profile::sudo: allow ops group" {
		t.Errorf("Commits[0].Subject = %q", cc.Commits[0].Subject)
	}
	if len(cc.ChangedPaths) != 2 || cc.ChangedPaths[0] != "manifests/profile/sudo.pp" {
		t.Errorf("ChangedPaths = %v", cc.ChangedPaths)
	}
	if cc.Title == "" || cc.Description == "" {
		t.Errorf("Title = %q, Description = %q", cc.Title, cc.Description)
	}
	if len(cc.Truncated) != 0 {
		t.Errorf("Truncated = %v, want none", cc.Truncated)
	}
	if !cc.Present {
		t.Error("Present = false for a change context that was read")
	}
}

// TestLoadChangeContextRejectsWhatItDoesNotKnow. The
// unknown-field rule is what enforces "commit subjects, never bodies":
// a `body` key has no field to land in and is refused rather than
// forwarded to an inference service.
func TestLoadChangeContextRejectsWhatItDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"unknown version": "version: 2\nchange: {}\n",
		"missing version": "change: {}\n",
		"unknown top-level field": `
version: 1
change: {}
extra: true
`,
		"unknown change field": `
version: 1
change:
  diff: "a big blob"
`,
		"commit body": `
version: 1
change:
  commits:
    - sha: abc123
      subject: subject
      body: "pasted stack trace"
`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, dir, strings.ReplaceAll(name, " ", "-")+".yaml", content)
			if _, err := LoadChangeContext(path); err == nil {
				t.Errorf("LoadChangeContext accepted %s", name)
			}
		})
	}
}

// TestLoadChangeContextCapsFreeTextRatherThanFailing.
// A long pull-request description is not a reason to fail a pipeline, so
// it is truncated and the truncation is recorded where a reader can see
// it.
func TestLoadChangeContextCapsFreeTextRatherThanFailing(t *testing.T) {
	long := strings.Repeat("a", MaxDescriptionBytes*2)
	path := writeFile(t, t.TempDir(), "change.yaml",
		"version: 1\nchange:\n  title: \""+strings.Repeat("t", MaxTitleBytes*2)+"\"\n  description: \""+long+"\"\n")

	cc, err := LoadChangeContext(path)
	if err != nil {
		t.Fatalf("LoadChangeContext: %v", err)
	}
	if len(cc.Title) > MaxTitleBytes {
		t.Errorf("Title kept %d bytes, cap is %d", len(cc.Title), MaxTitleBytes)
	}
	if len(cc.Description) > MaxDescriptionBytes {
		t.Errorf("Description kept %d bytes, cap is %d", len(cc.Description), MaxDescriptionBytes)
	}
	if len(cc.Truncated) != 2 {
		t.Errorf("Truncated = %v, want both title and description named", cc.Truncated)
	}
}

// TestLoadChangeContextTruncatesOnRuneBoundaries guards the cap against
// splitting a multi-byte character, which would put invalid UTF-8 into a
// JSON request body.
func TestLoadChangeContextTruncatesOnRuneBoundaries(t *testing.T) {
	path := writeFile(t, t.TempDir(), "change.yaml",
		"version: 1\nchange:\n  title: \""+strings.Repeat("é", MaxTitleBytes)+"\"\n")

	cc, err := LoadChangeContext(path)
	if err != nil {
		t.Fatalf("LoadChangeContext: %v", err)
	}
	if !utf8ValidString(cc.Title) {
		t.Errorf("Title is not valid UTF-8 after truncation: %q", cc.Title)
	}
}

// TestLoadChangeContextIsOptional: a run with no
// repository change to describe is ordinary, not an error.
func TestLoadChangeContextIsOptional(t *testing.T) {
	cc, err := LoadChangeContext("")
	if err != nil {
		t.Fatalf("LoadChangeContext(\"\"): %v", err)
	}
	if cc.Present {
		t.Error("Present = true for an unspecified change context")
	}

	if _, err := LoadChangeContext(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("LoadChangeContext accepted a named file that does not exist")
	}
}
