package resolve

import (
	"os"
	"path/filepath"

	"github.com/example42/piace/internal/config"
)

// ResolveServices validates a `--services` file per design.md section 2.2:
// version 1, and for each of compiler/puppetdb, a non-empty https endpoint
// URL and exactly one usable reference to each of the CA bundle, client
// certificate and private key.
//
// dir is the services file's directory. A relative path written in the file
// resolves against it, because every file path named in a config file
// resolves against the directory of the file that names it: the same rule
// snapshot paths follow relative to the target file, and policy_notes_file
// follows relative to this one.
//
// It does not check file existence or readability (see doc.go); that is the
// transport's concern once the clients are built. Every path returned is
// absolute, so the open error the transport reports names one unambiguous
// location rather than a relative path the reader has to resolve by hand.
//
// Like ResolveTargets, it accumulates every problem it finds into a single
// error rather than failing on the first one, per design.md section 3.2.
func ResolveServices(sf config.ServicesFile, dir string) (Services, error) {
	var c errorCollector

	if sf.Version != config.ServicesFileVersion {
		c.addf("services file: unsupported version %d, expected %d", sf.Version, config.ServicesFileVersion)
	}

	compiler := resolveEndpoint("compiler", sf.Compiler, dir, &c)
	puppetdb := resolveEndpoint("puppetdb", sf.PuppetDB, dir, &c)

	if c.hasErrors() {
		return Services{}, c.result()
	}
	return Services{Compiler: compiler, PuppetDB: puppetdb}, nil
}

func resolveEndpoint(section string, ep config.ServiceEndpoint, dir string, c *errorCollector) Endpoint {
	u, err := validateHTTPSEndpoint(ep.Endpoint)
	if err != nil {
		c.addf("services.%s.endpoint: %s", section, err)
	}
	return Endpoint{
		URL:        u,
		CABundle:   resolveTLSPath(section, "ca_bundle", ep.CABundle, ep.CABundleEnv, dir, c),
		ClientCert: resolveTLSPath(section, "client_cert", ep.ClientCert, ep.ClientCertEnv, dir, c),
		PrivateKey: resolveTLSPath(section, "private_key", ep.PrivateKey, ep.PrivateKeyEnv, dir, c),
	}
}

// resolveTLSPath reads one TLS file location from exactly one of the two
// references a service section may carry, the same way the inference
// section reads its bearer token. Naming both is a configuration error
// rather than a precedence rule nobody remembers, and naming neither is
// refused outright: PIACE never discovers a credential on its own.
//
// A path written in the file resolves against dir when relative. A path
// arriving through the environment must already be absolute: it is named
// in no file, so there is no directory it could sensibly resolve against,
// and the per-job credential directory this form exists to serve is
// absolute anyway.
func resolveTLSPath(section, key, path, env, dir string, c *errorCollector) string {
	switch {
	case path != "" && env != "":
		c.addf("services.%s: set %s or %s_env, not both", section, key, key)
		return ""

	case env != "":
		value := os.Getenv(env)
		if value == "" {
			c.addf("services.%s.%s_env: environment variable %s is unset or empty", section, key, env)
			return ""
		}
		if err := validateTLSPath(key, value); err != nil {
			c.addf("services.%s.%s_env: environment variable %s: %s", section, key, env, err)
			return ""
		}
		if !filepath.IsAbs(value) {
			c.addf("services.%s.%s_env: environment variable %s must hold an absolute path, got %q", section, key, env, value)
			return ""
		}
		return value

	case path != "":
		if err := validateTLSPath(key, path); err != nil {
			c.addf("services.%s.%s: %s", section, key, err)
			return ""
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		return path

	default:
		c.addf("services.%s: set %s or %s_env", section, key, key)
		return ""
	}
}
