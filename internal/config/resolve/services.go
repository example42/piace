package resolve

import "github.com/example42/piace/internal/config"

// ResolveServices validates a `--services` file per design.md section 2.2:
// version 1, and for each of compiler/puppetdb, a non-empty https endpoint
// URL and non-empty, syntactically valid CA bundle/client-certificate/
// private-key paths. It does not check file existence or readability (see
// doc.go); that is task 3's concern once the transports are built.
//
// Like ResolveTargets, it accumulates every problem it finds into a single
// error rather than failing on the first one, per design.md section 3.2.
func ResolveServices(sf config.ServicesFile) (Services, error) {
	var c errorCollector

	if sf.Version != config.ServicesFileVersion {
		c.addf("services file: unsupported version %d, expected %d", sf.Version, config.ServicesFileVersion)
	}

	compiler := resolveEndpoint("compiler", sf.Compiler, &c)
	puppetdb := resolveEndpoint("puppetdb", sf.PuppetDB, &c)

	if c.hasErrors() {
		return Services{}, c.result()
	}
	return Services{Compiler: compiler, PuppetDB: puppetdb}, nil
}

func resolveEndpoint(section string, ep config.ServiceEndpoint, c *errorCollector) Endpoint {
	u, err := validateHTTPSEndpoint(ep.Endpoint)
	if err != nil {
		c.addf("services.%s.endpoint: %s", section, err)
	}
	if err := validateTLSPath("ca_bundle", ep.CABundle); err != nil {
		c.addf("services.%s.ca_bundle: %s", section, err)
	}
	if err := validateTLSPath("client_cert", ep.ClientCert); err != nil {
		c.addf("services.%s.client_cert: %s", section, err)
	}
	if err := validateTLSPath("private_key", ep.PrivateKey); err != nil {
		c.addf("services.%s.private_key: %s", section, err)
	}
	return Endpoint{
		URL:        u,
		CABundle:   ep.CABundle,
		ClientCert: ep.ClientCert,
		PrivateKey: ep.PrivateKey,
	}
}
