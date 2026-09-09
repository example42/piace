package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File is one path a command reads or writes, with the flag or
// configuration key that named it, so a diagnostic can tell the operator
// which two of their arguments collide rather than only which two paths
// do.
type File struct {
	Role string
	Path string
}

// ValidateDestinations rejects a set of destinations that cannot all be honored:
// two outputs naming one file, or an output naming a file the same run
// reads. It is meant to be called before any service request, so a run
// that was always going to lose an artifact, or destroy its own input,
// costs nothing and changes nothing.
//
// Two inputs naming one file is not a conflict: reading a file twice is
// harmless.
//
// Paths are compared after being made absolute, cleaned, and having
// their containing directory's symlinks resolved, so `./out/report.json`
// and `/srv/ci/out/report.json` are recognized as one file. Where both
// paths already exist, os.SameFile settles it instead, which additionally
// catches a symlink or a hard link pointing at the other. Two paths that
// do not exist yet and would become hard links to each other cannot be
// detected here, and are not: nothing has linked them at the moment this
// runs.
//
// A path of "-" is the stdin/stdout convention and is skipped, as is an
// empty path, which means the caller was not asked for that artifact.
func ValidateDestinations(inputs, outputs []File) error {
	type resolved struct {
		File
		canonical string
		info      os.FileInfo
	}
	prepare := func(files []File) []resolved {
		out := make([]resolved, 0, len(files))
		for _, f := range files {
			if f.Path == "" || f.Path == "-" {
				continue
			}
			r := resolved{File: f, canonical: canonicalPath(f.Path)}
			if info, err := os.Stat(f.Path); err == nil {
				r.info = info
			}
			out = append(out, r)
		}
		return out
	}
	in, outs := prepare(inputs), prepare(outputs)

	same := func(a, b resolved) bool {
		if a.info != nil && b.info != nil {
			return os.SameFile(a.info, b.info)
		}
		return a.canonical == b.canonical
	}

	var problems []string
	for i := range outs {
		for j := i + 1; j < len(outs); j++ {
			if same(outs[i], outs[j]) {
				problems = append(problems, fmt.Sprintf("%s and %s name the same file (%s); each artifact needs its own destination",
					outs[i].Role, outs[j].Role, outs[i].Path))
			}
		}
		for _, source := range in {
			if same(outs[i], source) {
				problems = append(problems, fmt.Sprintf("%s would overwrite %s (%s), which this run reads",
					outs[i].Role, source.Role, source.Path))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("conflicting file destinations:\n  %s", strings.Join(problems, "\n  "))
}

// canonicalPath makes path absolute and cleans it, then resolves
// symlinks in its containing directory. The final element is
// deliberately left unresolved: it commonly does not exist yet, and
// where it does exist Validate compares identities with os.SameFile
// rather than by name.
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	dir, base := filepath.Split(abs)
	resolvedDir, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return abs
	}
	return filepath.Join(resolvedDir, base)
}
