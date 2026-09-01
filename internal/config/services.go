package config

// ServicesFileVersion is the only supported `version` value for a services
// file.
const ServicesFileVersion = 1

// ServicesFile is the root document of a `--services` YAML file. It
// keeps endpoint and mTLS settings out of the reviewable target
// selection file;2 ("Service configuration").
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
// service. Endpoint must be an `https` URL, and each of the CA bundle,
// client certificate and private key must be named exactly once, either
// as a path in the file or as the name of an environment variable
// holding one. Both forms carry a file path: PIACE never accepts inline
// key material or bearer tokens, and never mints or discovers
// credentials on its own.
//
// The `_env` variants exist so a services file committed to a control
// repository can be read in place, unmodified, by a CI job whose
// credential directory does not exist until the job starts. They mirror
// the inference section's TokenEnv: the file holds a reference to a
// credential, never the credential.
type ServiceEndpoint struct {
	Endpoint string `json:"endpoint" yaml:"endpoint"`

	CABundle      string `json:"ca_bundle" yaml:"ca_bundle"`
	CABundleEnv   string `json:"ca_bundle_env" yaml:"ca_bundle_env"`
	ClientCert    string `json:"client_cert" yaml:"client_cert"`
	ClientCertEnv string `json:"client_cert_env" yaml:"client_cert_env"`
	PrivateKey    string `json:"private_key" yaml:"private_key"`
	PrivateKeyEnv string `json:"private_key_env" yaml:"private_key_env"`
}
