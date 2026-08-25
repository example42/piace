package resolve

import (
	"fmt"
	"os"
	"path/filepath"
)

// LoadTargetFile decodes and fully resolves a `--targets` YAML file from
// path: strict decode (unknown-field rejection), version check, default
// resolution, per-target override, exclude/redact append-only merge, and
// every validation rule in design.md section 3.2. Relative facts.file/
// baseline.file values resolve against path's containing directory.
//
// It performs no network or service I/O; only local filesystem access to
// read path itself.
func LoadTargetFile(path string) ([]Target, error) {
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
	return ResolveTargets(tf, dir)
}

// LoadServicesFile decodes and fully resolves a `--services` YAML file
// from path. It performs no network or service I/O; only local filesystem
// access to read path itself. TLS file existence/readability is validated
// later, at transport construction (task 3).
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

	return ResolveServices(sf)
}

// Load decodes and resolves both the target and services files, per
// design.md section 3. Both files are always attempted and every problem
// from both is accumulated into one error: a user configuring both files
// wrong sees every problem from one run, per design.md section 3.2's
// "Invalid configuration is one operational diagnostic" rule.
func Load(targetsPath, servicesPath string) (Config, error) {
	var c errorCollector

	targets, targetsErr := LoadTargetFile(targetsPath)
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
