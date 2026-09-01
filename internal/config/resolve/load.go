package resolve

import (
	"fmt"
	"os"
	"path/filepath"
)

// LoadTargetFile decodes and fully resolves a `--targets` YAML file from
// path: strict decode (unknown-field rejection), version check,
// invocation overrides, default resolution, per-target override, exclude
// and redact append-only merge, and every validation rule. Relative
// facts.file and baseline.file values resolve against path's containing
// directory.
//
// ov is applied to the decoded document before resolution, so an
// overridden field is validated and reported exactly as a file-supplied
// one; see Overrides. Pass the zero value to override nothing.
//
// It performs no network or service I/O; only local filesystem access to
// read path itself.
func LoadTargetFile(path string, ov Overrides) ([]Target, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening target file: %w", err)
	}
	defer f.Close()

	tf, err := decodeTargetFile(f)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	return ResolveTargets(ov.apply(tf), dir)
}

// LoadServicesFile decodes and fully resolves a `--services` YAML file
// from path. Relative TLS paths resolve against path's containing
// directory. It performs no network or service I/O; only local filesystem
// access to read path itself, and to read any file an environment variable
// the file names points at. TLS file existence/readability is validated
// later, at transport construction (internal/transport).
func LoadServicesFile(path string) (Services, error) {
	f, err := os.Open(path)
	if err != nil {
		return Services{}, fmt.Errorf("opening services file: %w", err)
	}
	defer f.Close()

	sf, err := decodeServicesFile(f)
	if err != nil {
		return Services{}, err
	}

	return ResolveServices(sf, filepath.Dir(path))
}

// Load decodes and resolves both the target and services files applying
// ov to the target file as LoadTargetFile documents. Both files are
// always attempted and every problem from both is accumulated into one
// error: a user configuring both files wrong sees every problem from one
// run: invalid configuration is one operational diagnostic.
func Load(targetsPath, servicesPath string, ov Overrides) (Config, error) {
	var c errorCollector

	targets, targetsErr := LoadTargetFile(targetsPath, ov)
	appendErr(&c, targetsErr)

	services, servicesErr := LoadServicesFile(servicesPath)
	appendErr(&c, servicesErr)

	if c.hasErrors() {
		return Config{}, c.result()
	}
	return Config{Targets: targets, Services: services}, nil
}

// appendErr flattens a *ValidationError's individual problems into c
// rather than nesting its multi-line Error() string as a single problem,
// so Load's combined error reads as one flat accumulated list. A non-
// ValidationError (e.g. a file-open or YAML syntax error) is added as a
// single problem.
func appendErr(c *errorCollector, err error) {
	if err == nil {
		return
	}
	if ve, ok := err.(*ValidationError); ok {
		for _, p := range ve.Problems() {
			c.err.add(p)
		}
		return
	}
	c.err.add(err.Error())
}
