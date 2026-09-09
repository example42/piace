package resolve

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/example42/piace/internal/config"
)

// LoadTargetFile decodes and fully resolves a `--targets` YAML file from
// path for cmd: strict decode (unknown-field rejection), version check,
// invocation overrides, default resolution, per-target override, exclude
// and redact append-only merge, and every validation rule. Relative
// facts.file and baseline.file values resolve against path's containing
// directory.
//
// cmd decides which fields must be present; see Command.
//
// ov is applied to the decoded document before resolution, so an
// overridden field is validated and reported exactly as a file-supplied
// one; see Overrides. Pass the zero value to override nothing.
//
// It performs no network or service I/O; only local filesystem access to
// read path itself.
func LoadTargetFile(path string, cmd Command, ov Overrides) ([]Target, error) {
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
	return ResolveTargets(ov.apply(tf), dir, cmd)
}

// LoadServicesFile decodes and fully resolves the sections of a
// `--services` YAML file that need names in. A section outside need is
// neither required nor validated and comes back zero. Relative TLS paths
// resolve against path's containing directory. It performs no network or
// service I/O; only local filesystem access to read path itself, and to
// read any file an environment variable the file names points at. TLS
// file existence/readability is validated later, at transport
// construction (internal/transport).
func LoadServicesFile(path string, need ServiceSet) (Services, error) {
	f, err := os.Open(path)
	if err != nil {
		return Services{}, fmt.Errorf("opening services file: %w", err)
	}
	defer f.Close()

	sf, err := decodeServicesFile(f)
	if err != nil {
		return Services{}, err
	}

	return ResolveServices(sf, filepath.Dir(path), need)
}

// Load decodes and resolves both the target and services files for cmd,
// applying ov to the target file as LoadTargetFile documents. Both files
// are always attempted and every problem from both is accumulated into
// one error: a user configuring both files wrong sees every problem from
// one run: invalid configuration is one operational diagnostic.
//
// Which service endpoints are required depends on cmd and on what the
// targets select, so the target file is decoded first and the answer is
// computed from it (Command.services). A services file naming only the
// endpoints a run actually uses is valid; the returned Config records
// the answer in Required so a caller builds exactly those clients.
func Load(cmd Command, targetsPath, servicesPath string, ov Overrides) (Config, error) {
	var c errorCollector

	need, decodeErr := serviceSetFor(cmd, targetsPath, ov)
	// A target file that cannot even be decoded is reported by
	// LoadTargetFile below, with the same error text; here it only means
	// the target-dependent part of the answer is unknown. The
	// command-static part is not, so fall back to the requirements of an
	// empty target file rather than to "everything": demanding a compiler
	// identity from `capture facts` because its target file has a typo
	// would report a problem that does not exist.
	if decodeErr != nil {
		need = cmd.services(config.TargetFile{})
	}

	targets, targetsErr := LoadTargetFile(targetsPath, cmd, ov)
	appendErr(&c, targetsErr)

	services, servicesErr := LoadServicesFile(servicesPath, need)
	appendErr(&c, servicesErr)

	if c.hasErrors() {
		return Config{}, c.result()
	}
	return Config{Targets: targets, Services: services, Required: need}, nil
}

// serviceSetFor decodes the target file a second time, without
// validating it, to ask which services cmd will need. Decoding is cheap
// and pure, and doing it separately is what lets Load report target and
// services problems together instead of refusing to look at the services
// file until the target file is perfect.
func serviceSetFor(cmd Command, targetsPath string, ov Overrides) (ServiceSet, error) {
	f, err := os.Open(targetsPath)
	if err != nil {
		return ServiceSet{}, err
	}
	defer f.Close()

	tf, err := decodeTargetFile(f)
	if err != nil {
		return ServiceSet{}, err
	}
	return cmd.services(ov.apply(tf)), nil
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
