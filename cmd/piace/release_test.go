package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRelease_NoNonStandardDependenciesBeyondYAML discharges
// requirements.md 12.2 at the level it can be checked mechanically: the
// binary's transitive dependency set contains only the Go standard
// library, this module's own packages, and gopkg.in/yaml.v3.
//
// requirement 12.2's substance — "no Ruby, Puppet agent, Facter, package
// manager, or runtime dependency resolution" — follows from that set
// being closed: nothing in it shells out to a Puppet toolchain or
// resolves a package at run time. A new third-party dependency would fail
// this test and force that judgement to be made deliberately rather than
// noticed after a release.
func TestRelease_NoNonStandardDependenciesBeyondYAML(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	const modulePrefix = "github.com/example42/piace/"
	allowed := map[string]bool{"gopkg.in/yaml.v3": true}

	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		dep = strings.TrimSpace(dep)
		if dep == "" || allowed[dep] || strings.HasPrefix(dep, modulePrefix) {
			continue
		}
		// A standard-library package's import path has no dot in its
		// first element; every module path does (a domain name).
		first := dep
		if i := strings.Index(dep, "/"); i >= 0 {
			first = dep[:i]
		}
		if !strings.Contains(first, ".") {
			continue
		}
		t.Errorf("unexpected non-standard dependency %q: requirements.md 12.2 restricts the binary to the standard library plus gopkg.in/yaml.v3", dep)
	}
}

// TestRelease_BuildsWithCGODisabled discharges requirements.md 12.1: the
// artifact is a CGO-free Go binary. A build that silently required cgo
// would produce a binary linked against the host's libc and would not be
// the statically linked, air-gap-installable artifact design.md section
// 11 describes.
func TestRelease_BuildsWithCGODisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the release build in -short mode")
	}
	cmd := exec.Command("go", "build", "-o", t.TempDir()+"/piace", ".")
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("CGO_ENABLED=0 go build failed: %v\n%s", err, out)
	}
}
