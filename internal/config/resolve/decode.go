package resolve

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/example42/piace/internal/config"
	"gopkg.in/yaml.v3"
)

// MaxConfigBytes bounds a configuration file. A target file lists nodes
// and policy and a services file names three endpoints; neither is large,
// and a bound here means a file of any size can be handed to PIACE
// without the decoder allocating whatever it contains first.
const MaxConfigBytes = 4 * 1024 * 1024

// decodeTargetFile decodes a `--targets` YAML document with
// unknown-field rejection. gopkg.in/yaml.v3's default decoder is
// permissive, silently ignoring keys with no matching struct field;
// Decoder.KnownFields(true) switches it to strict mode, which is what a
// target file requires.
func decodeTargetFile(r io.Reader) (config.TargetFile, error) {
	var tf config.TargetFile
	if err := decodeSingleDocument(r, &tf); err != nil {
		return config.TargetFile{}, fmt.Errorf("decoding target file: %w", err)
	}
	return tf, nil
}

// decodeServicesFile decodes a `--services` YAML document with the same
// strict unknown-field rejection as decodeTargetFile.
func decodeServicesFile(r io.Reader) (config.ServicesFile, error) {
	var sf config.ServicesFile
	if err := decodeSingleDocument(r, &sf); err != nil {
		return config.ServicesFile{}, fmt.Errorf("decoding services file: %w", err)
	}
	return sf, nil
}

// decodeSingleDocument decodes exactly one bounded YAML document into
// out, strictly.
//
// "Exactly one" is not what a YAML decoder gives by default: Decode
// reads the first document of a stream and leaves the rest, so a file
// whose real configuration sits after a `---` separator would be read as
// whatever came before it, silently. A configuration file has one
// meaning or it is rejected, the same rule a stored result document
// follows (see internal/report's DecodeJSON).
func decodeSingleDocument(r io.Reader, out any) error {
	// One byte past the maximum is read deliberately: it is what makes
	// "too large" decidable without reading a file of arbitrary size, and
	// the check happens before the decoder allocates anything, since a
	// truncated document could otherwise parse as a valid smaller one.
	data, err := io.ReadAll(io.LimitReader(r, MaxConfigBytes+1))
	if err != nil {
		return err
	}
	if len(data) > MaxConfigBytes {
		return fmt.Errorf("the file exceeds the %d-byte configuration limit", MaxConfigBytes)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("the file is empty")
		}
		return err
	}
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("the file contains more than one YAML document; PIACE reads exactly one")
		}
		return err
	}
	return nil
}
