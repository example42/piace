package compiler

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// wireVersion accepts a catalog response's `version` field as either a
// JSON string or a bare JSON number: OpenVox's documented v3 example
// response shows a bare integer (`"version": 1377473054`), while
// PuppetDB's own query-API catalog documentation shows a string (e.g.
// `"e4c339f"`). puppetdb.Catalog.Version is a string, so this type
// normalizes either wire representation to its exact decimal/string text
// before that assignment, rather than picking one shape and rejecting the
// other compiler's documented response.
type wireVersion string

func (v *wireVersion) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*v = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = wireVersion(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("version is neither a string nor a number: %w", err)
	}
	*v = wireVersion(n.String())
	return nil
}

// v4CatalogEnvelope is the POST /puppet/v4/catalog response wrapper.
// Unlike v3, which returns the catalog document directly, the v4
// endpoint returns it under a "catalog" member (alongside an optional
// "logs" member when the request asked for captured logs, which PIACE
// never does). See doc.go's wire-shape section for the primary sources:
// puppetserver's compiler.rb returns `{ catalog: ... }` and its
// master_core.clj v4 handler JSON-encodes that hash verbatim as the
// response body.
//
// Logs is intentionally not declared: PIACE never sets
// options.capture_logs, and a compiler that returned logs anyway would
// have them dropped rather than mistaken for catalog content.
type v4CatalogEnvelope struct {
	Catalog json.RawMessage `json:"catalog"`
}

// wireCatalog is the compiler's catalog document shape: `{"name",
// "environment", "code_id", "catalog_uuid", "transaction_uuid",
// "resources", "edges", ...}`. It is the whole v3 response body, and the
// value of a v4 response's "catalog" member (see v4CatalogEnvelope).
// Unlike internal/puppetdb's query-API Catalog carrier, the identity
// field here is `name`, not `certname`, and resources/edges are
// plain JSON arrays, not a `{href, data}` expansion. Fields this package
// does not consume (tags, classes, catalog_format, metadata,
// recursive_metadata) are intentionally not declared and are dropped by
// encoding/json on unmarshal — the same lossy-typed-struct-roundtrip
// approach internal/puppetdb's Factset/Catalog carriers already use for
// snapshot payload construction (see internal/capture/workflow.go's
// buildCatalogEnvelope, which marshals the typed puppetdb.Catalog, not
// raw response bytes).
type wireCatalog struct {
	Name            string          `json:"name"`
	Version         wireVersion     `json:"version"`
	Environment     string          `json:"environment"`
	CodeID          *string         `json:"code_id"`
	CatalogUUID     *string         `json:"catalog_uuid"`
	TransactionUUID *string         `json:"transaction_uuid"`
	Resources       json.RawMessage `json:"resources"`
	Edges           json.RawMessage `json:"edges"`
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// wireErrorProbe detects a compiler response that decodes as valid JSON
// but reports a semantic rejection via an "error" field, mirroring
// internal/puppetdb/adapter.go's probeBody pattern for PuppetDB's
// documented not-found shape. It is checked ahead of wireCatalog
// decoding so a semantic-rejection response is classified as a semantic
// request rejection rather than a malformed response. The two map to the
// same compilation-failure diagnostic today, but are worth
// distinguishing in the message for an operator reading logs.
type wireErrorProbe struct {
	Error string `json:"error"`
}

// factEntry is one element of a factset's expanded `facts.data` array
// (PuppetDB's documented `{name, value}` shape; see
// internal/puppetdb/doc.go).
type factEntry struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// expandedFacts is the `facts` field shape puppetdb.Factset.Facts always
// carries, per internal/puppetdb/doc.go: `{"href": <url>, "data":
// [{"name", "value"}, ...]}`. This holds regardless of whether the
// Factset came from a live PuppetDB query or a file-backed snapshot,
// since a snapshot's payload is the captured PuppetDB response verbatim
// (see internal/capture's buildFactsetEnvelope).
type expandedFacts struct {
	Href string      `json:"href"`
	Data []factEntry `json:"data"`
}

// flattenFacts converts a factset's expanded `facts` field into the flat
// `{"<fact name>": <fact value>, ...}` hash the v3 and v4 catalog
// request wire formats require, as documented in doc.go.
func flattenFacts(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var ef expandedFacts
	if err := json.Unmarshal(raw, &ef); err != nil {
		return nil, fmt.Errorf("decoding expanded facts: %w", err)
	}
	out := make(map[string]json.RawMessage, len(ef.Data))
	for _, e := range ef.Data {
		out[e.Name] = e.Value
	}
	return out, nil
}

// trustedFactsProbe validates a candidate "trusted" fact value against
// Puppet's documented trusted-fact shape: a non-empty "certname" string
// and a present "authenticated" field. See doc.go's documented assumption
// for exactly why these two fields (and not, say, requiring "extensions"
// too) are the validity criteria.
type trustedFactsProbe struct {
	Certname      string          `json:"certname"`
	Authenticated json.RawMessage `json:"authenticated"`
}

// extractTrustedFacts looks up the "trusted" entry in flat and returns
// its raw value plus true only when it decodes to Puppet's documented
// trusted-fact shape. It never fabricates a trusted-fact structure: a
// missing "trusted" fact, or one that fails validation, returns (nil,
// false), the case that forces the caller to decide between the
// compiler-lookup path and failing the request outright.
func extractTrustedFacts(flat map[string]json.RawMessage) (json.RawMessage, bool) {
	raw, ok := flat["trusted"]
	if !ok {
		return nil, false
	}
	var probe trustedFactsProbe
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, false
	}
	if probe.Certname == "" || len(probe.Authenticated) == 0 {
		return nil, false
	}
	return raw, true
}

// v4Persistence is always {false, false} in every request this package
// builds. PIACE never persists candidate facts or candidate catalogs to
// PuppetDB, which makes this non-negotiable rather than a
// caller-configurable option.
type v4Persistence struct {
	Facts   bool `json:"facts"`
	Catalog bool `json:"catalog"`
}

type v4FactsField struct {
	Values map[string]json.RawMessage `json:"values"`
}

type v4TrustedFactsField struct {
	Values json.RawMessage `json:"values"`
}

// v4Request is the POST /puppet/v4/catalog request body shape, per
// doc.go's documented assumption from Puppet Server's v4 catalog API.
type v4Request struct {
	Certname        string               `json:"certname"`
	Persistence     v4Persistence        `json:"persistence"`
	Environment     string               `json:"environment"`
	Facts           v4FactsField         `json:"facts"`
	TrustedFacts    *v4TrustedFactsField `json:"trusted_facts,omitempty"`
	TransactionUUID string               `json:"transaction_uuid,omitempty"`
}

// v3Facts is the JSON value the v3 catalog request's form-encoded `facts`
// parameter carries: `{"name": <node>, "values": {...}}`, per doc.go's
// v3 request bullet.
type v3Facts struct {
	Name   string                     `json:"name"`
	Values map[string]json.RawMessage `json:"values"`
}

// newTransactionUUID generates a random RFC 4122 version-4 UUID for the
// v3/v4 catalog request's `transaction_uuid` field. PIACE has no
// transaction to correlate against a Puppet report (it never triggers a
// run or persists anything, per v4Persistence above), so this value only
// needs to be a syntactically valid, unique identifier for the single
// request it accompanies — not sourced from, or matched against, any
// other PIACE-generated identifier.
func newTransactionUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating transaction_uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
