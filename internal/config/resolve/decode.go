package resolve

import (
	"fmt"
	"io"

	"github.com/example42/piace/internal/config"
	"gopkg.in/yaml.v3"
)

// decodeTargetFile decodes a `--targets` YAML document with
// unknown-field rejection. gopkg.in/yaml.v3's default decoder is
// permissive, silently ignoring keys with no matching struct field;
// Decoder.KnownFields(true) switches it to strict mode, which is what a
// target file requires.
func decodeTargetFile(r io.Reader) (config.TargetFile, error) {
	var tf config.TargetFile
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&tf); err != nil {
		return config.TargetFile{}, fmt.Errorf("decoding target file: %w", err)
	}
	return tf, nil
}

// decodeServicesFile decodes a `--services` YAML document with the same
// strict unknown-field rejection as decodeTargetFile.
func decodeServicesFile(r io.Reader) (config.ServicesFile, error) {
	var sf config.ServicesFile
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&sf); err != nil {
		return config.ServicesFile{}, fmt.Errorf("decoding services file: %w", err)
	}
	return sf, nil
}
