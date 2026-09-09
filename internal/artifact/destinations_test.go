package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate_AcceptsDistinctDestinations(t *testing.T) {
	dir := t.TempDir()
	err := ValidateDestinations([]File{{Role: "--targets", Path: filepath.Join(dir, "targets.yaml")}},
		[]File{
			{Role: "--json-out", Path: filepath.Join(dir, "report.json")},
			{Role: "--html-out", Path: filepath.Join(dir, "report.html")},
		})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_RejectsTwoOutputsNamingOneFile(t *testing.T) {
	dir := t.TempDir()
	err := ValidateDestinations(nil, []File{
		{Role: "--json-out", Path: filepath.Join(dir, "report.json")},
		{Role: "--html-out", Path: filepath.Join(dir, "sub", "..", "report.json")},
	})
	if err == nil {
		t.Fatal("accepted two artifacts written to one path")
	}
	for _, want := range []string{"--json-out", "--html-out"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the diagnostic does not name %s: %v", want, err)
		}
	}
}

func TestValidate_RejectsAnOutputOverAnInput(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	err := ValidateDestinations([]File{{Role: "--json-in", Path: report}},
		[]File{{Role: "--ai-out", Path: report}})
	if err == nil {
		t.Fatal("accepted an artifact overwriting the document the run reads")
	}
	if !strings.Contains(err.Error(), "--json-in") || !strings.Contains(err.Error(), "--ai-out") {
		t.Errorf("the diagnostic does not name both roles: %v", err)
	}
}

// TestValidate_RejectsAliasesOfAnInput covers the identities a name
// comparison alone misses: a symlink and a hard link to the same file
// are the same file.
func TestValidate_RejectsAliasesOfAnInput(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	if err := os.WriteFile(report, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	symlink := filepath.Join(dir, "linked-report.json")
	if err := os.Symlink(report, symlink); err != nil {
		t.Skipf("this platform cannot create symlinks: %v", err)
	}
	hardlink := filepath.Join(dir, "hard-report.json")
	if err := os.Link(report, hardlink); err != nil {
		t.Skipf("this filesystem cannot create hard links: %v", err)
	}

	for name, alias := range map[string]string{"symlink": symlink, "hard link": hardlink} {
		t.Run(name, func(t *testing.T) {
			err := ValidateDestinations([]File{{Role: "--json-in", Path: report}},
				[]File{{Role: "--ai-out", Path: alias}})
			if err == nil {
				t.Fatalf("accepted an output that is a %s to the input", name)
			}
		})
	}
}

// TestValidate_RelativeAndAbsoluteFormsOfOnePath covers a path written
// two ways in two flags, which is what a CI job assembling arguments
// from different variables produces.
func TestValidate_RelativeAndAbsoluteFormsOfOnePath(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	err = ValidateDestinations(nil, []File{
		{Role: "--json-out", Path: "report.json"},
		{Role: "--text-out", Path: filepath.Join(dir, "report.json")},
	})
	if err == nil {
		t.Fatal("accepted the same file named relatively and absolutely")
	}
}

func TestValidate_SkipsUnrequestedAndStreamPaths(t *testing.T) {
	if err := ValidateDestinations([]File{{Role: "--json-in", Path: "-"}},
		[]File{
			{Role: "--json-out", Path: ""},
			{Role: "--html-out", Path: ""},
			{Role: "--text-out", Path: "-"},
		}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_TwoInputsMayNameOneFile(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "config.yaml")
	if err := ValidateDestinations([]File{
		{Role: "--targets", Path: shared},
		{Role: "--services", Path: shared},
	}, nil); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_ReportsEveryConflictAtOnce(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	err := ValidateDestinations([]File{{Role: "--json-in", Path: report}},
		[]File{
			{Role: "--json-out", Path: report},
			{Role: "--html-out", Path: report},
		})
	if err == nil {
		t.Fatal("expected conflicts")
	}
	if lines := strings.Count(err.Error(), "\n"); lines < 3 {
		t.Errorf("error reports %d problems, want all three:\n%v", lines, err)
	}
}
