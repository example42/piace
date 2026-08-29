package config

// ServicesFileVersion is the only supported `version` value for a services
// file.
const ServicesFileVersion = 1

// ServicesFile is the root document of a `--services` YAML file. It keeps
// endpoint and mTLS settings out of the reviewable target selection file;
// see design.md section 2.2 ("Service configuration").
type ServicesFile struct {
	Version  int             `json:"version" yaml:"version"`
	Compiler ServiceEndpoint `json:"compiler" yaml:"compiler"`
	PuppetDB ServiceEndpoint `json:"puppetdb" yaml:"puppetdb"`
	// Inference is optional and loads independently of the other two.
	// `piace compare` ignores it entirely and contacts no inference
	// service; `piace explain` reads only this section.
	Inference *InferenceSection `json:"inference,omitempty" yaml:"inference"`
}

// ServiceEndpoint describes one independently configured mTLS HTTP
// service. All four fields are required after resolution. Endpoint must be
// an `https` URL; CA bundle, client certificate, and private key are file
// paths only. PIACE never accepts inline key material or bearer tokens, and
// never mints or discovers credentials on its own. See design.md section
// 2.2 and requirements.md 3.1-3.5.
type ServiceEndpoint struct {
	Endpoint   string `json:"endpoint" yaml:"endpoint"`
	CABundle   string `json:"ca_bundle" yaml:"ca_bundle"`
	ClientCert string `json:"client_cert" yaml:"client_cert"`
	PrivateKey string `json:"private_key" yaml:"private_key"`
}
