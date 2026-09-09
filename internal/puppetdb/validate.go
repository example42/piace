package puppetdb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// FlattenFacts validates the expanded query-API collection before flattening.
// A present empty data array is valid; absent, null and duplicate data is not.
func FlattenFacts(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var collection struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &collection) != nil || len(collection.Data) == 0 || bytes.TrimSpace(collection.Data)[0] != '[' {
		return nil, fmt.Errorf("facts must contain a data array")
	}
	var entries []struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(collection.Data, &entries) != nil {
		return nil, fmt.Errorf("malformed fact entry")
	}
	out := make(map[string]json.RawMessage, len(entries))
	for _, entry := range entries {
		if entry.Name == "" || len(entry.Value) == 0 {
			return nil, fmt.Errorf("fact entry requires a name and value")
		}
		if _, exists := out[entry.Name]; exists {
			return nil, fmt.Errorf("duplicate fact name")
		}
		out[entry.Name] = entry.Value
	}
	return out, nil
}

// ValidateFactset applies the same target and trusted-input contract to file,
// PuppetDB and compiler inputs. Missing trusted facts are a compiler policy
// decision; supplied malformed facts cannot fall back to a lookup.
func ValidateFactset(fs Factset, certname string) (map[string]json.RawMessage, error) {
	if certname == "" || fs.Certname != certname {
		return nil, fmt.Errorf("factset certname does not match the requested target")
	}
	flat, err := FlattenFacts(fs.Facts)
	if err != nil {
		return nil, err
	}
	if raw, present := flat["trusted"]; present {
		if err := ValidateTrustedFacts(raw, certname); err != nil {
			return nil, err
		}
	}
	return flat, nil
}

// ValidateTrustedFacts checks Puppet::Context::TrustedInformation's wire
// fields. An unauthenticated remote context (false, nil certname) cannot
// establish the target identity required for supplied compilation input.
func ValidateTrustedFacts(raw json.RawMessage, certname string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return fmt.Errorf("trusted facts must be an object")
	}
	var name, authenticated string
	if json.Unmarshal(fields["certname"], &name) != nil || name == "" || name != certname {
		return fmt.Errorf("trusted certname does not match the requested target")
	}
	if json.Unmarshal(fields["authenticated"], &authenticated) != nil || (authenticated != "remote" && authenticated != "local") {
		return fmt.Errorf("trusted authenticated must establish a remote or local identity")
	}
	for _, field := range []string{"extensions", "external"} {
		if value, present := fields[field]; present {
			var object map[string]json.RawMessage
			if json.Unmarshal(value, &object) != nil || object == nil {
				return fmt.Errorf("trusted %s must be an object", field)
			}
		}
	}
	hostname, domain, hasDomain := strings.Cut(certname, ".")
	for _, item := range []struct{ field, expected string }{{"hostname", hostname}, {"domain", domain}} {
		field, expected := item.field, item.expected
		if raw, present := fields[field]; present {
			if field == "domain" && !hasDomain && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				continue
			}
			var value string
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil || value != expected {
				return fmt.Errorf("trusted %s does not agree with certname", field)
			}
		}
	}
	return nil
}
