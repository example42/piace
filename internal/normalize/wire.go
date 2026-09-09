package normalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// resourceWire is the subset of a catalog resource entry this package
// needs, common to both documented wire shapes (see doc.go): a PuppetDB
// query-API resources.data entry and a compiler plain-array resource
// entry both carry "type", "title", and "parameters" as top-level JSON
// object fields, whatever else they additionally carry. certname,
// resource, exported, tags, file, line and aliases are all intentionally
// undeclared here and therefore dropped by encoding/json.
type resourceWire struct {
	Type                string          `json:"type"`
	Title               string          `json:"title"`
	Parameters          json.RawMessage `json:"parameters"`
	SensitiveParameters json.RawMessage `json:"sensitive_parameters"`
}

// edgeWire is the normalized (shape-independent) form this package
// converts either documented edge wire shape into: a PuppetDB query-API
// edges.data entry's flat source_type/source_title/target_type/
// target_title fields, or a compiler plain-array edge entry's nested
// source/target resource-spec objects (see doc.go).
type edgeWire struct {
	SourceType  string
	SourceTitle string
	TargetType  string
	TargetTitle string
}

// pdbEdgeEntry is one element of a PuppetDB query-API edges.data array,
// per https://puppet.com/docs/puppetdb/8/catalogs.html's documented
// `<expanded edges>` shape: `{"relationship", "source_title",
// "source_type", "target_title", "target_type"}`.
//
// An edge's identity in the normalized model is solely the ordered
// (source, target) identity pair, direction alone being significant, so
// the relationship kind is not carried into it. It is decoded all the
// same, because it says which edges belong in the model at all: see
// containmentRelationship and doc.go.
type pdbEdgeEntry struct {
	SourceType   string `json:"source_type"`
	SourceTitle  string `json:"source_title"`
	TargetType   string `json:"target_type"`
	TargetTitle  string `json:"target_title"`
	Relationship string `json:"relationship"`
}

// containmentRelationship is the one edge relationship a compiled
// catalog carries. Puppet's PuppetDB terminus synthesizes the others,
// one per `require`, `before`, `notify` or `subscribe` metaparameter, on
// its way to storage: `synthesize_edges`, whose relationship names are
// its Relationships table ("before", "required-by", "notifies",
// "subscription-of"). A compiler's catalog response carries containment
// only, because relationship edges are resolved by the agent at apply
// time.
//
// So a stored baseline holds edges no candidate can hold, and comparing
// the two reports every relationship in the catalog as removed. Measured
// against a deployed OpenVox 8.15.2 installation on 2026-09-09,
// comparing one node's production environment with itself: 296 stored
// edges against 239 compiled ones, the 239 containment edges an
// identical multiset, and all 57 extras synthesized. The text report
// hides edge groups by design, so they were invisible there while
// reaching the JSON document, the aggregate and the inference request.
//
// Dropping them loses nothing. Each is derived from a metaparameter that
// is compared as a parameter in its own right, so a real relationship
// change is reported either way, and reported once rather than twice.
const containmentRelationship = "contains"

// isSynthesizedRelationship reports whether an edge carrying this
// relationship was added by the PuppetDB terminus rather than compiled.
// An absent relationship is a compiler-shaped edge, which is containment.
func isSynthesizedRelationship(relationship string) bool {
	return relationship != "" && relationship != containmentRelationship
}

// resourceReferencePattern is a Go port of the PuppetDB terminus's own
// resource_ref_to_hash regex, `/^([^\[\]]+)\[(.+)\]$/m` (puppetlabs/
// puppetdb, puppet/lib/puppet/indirector/catalog/puppetdb.rb). The
// semantics are load-bearing and are preserved exactly:
//
//   - `[^\[\]]+`: the type stops at the FIRST bracket of either kind;
//   - `(.+)` is greedy against a `$`-anchored `\]`, so the title runs to
//     the LAST `]`, which is what makes a composite title like
//     `File[/etc/foo[bar]]` split into `File` / `/etc/foo[bar]`;
//   - Ruby's `/m` (dot matches newline) is Go's `(?s)`, not `(?m)`.
//
// Porting it rather than approximating it with a Split is what makes the
// two sides of a comparison line up by construction: a PuppetDB baseline
// edge's source_type/source_title were produced by this exact function,
// applied to this exact reference string, when the terminus submitted
// the catalog.
var resourceReferencePattern = regexp.MustCompile(`(?s)^([^\[\]]+)\[(.+)\]$`)

// resourceSpecWire is one vertex of a plain-array catalog edge. It
// accepts both forms that legitimately occur there:
//
//   - a JSON object, `{"type": <string>, "title": <string>}`: PuppetDB's
//     documented catalog wire format v8 `<resource-spec>`
//     (https://puppet.com/docs/puppetdb/8/catalog_format_v8.html), which
//     is what the terminus submits;
//   - a JSON string in `Type[title]` reference form: what a compiler's
//     own v3/v4 catalog response carries, because
//     Puppet::Relationship#to_data_hash serializes each vertex as
//     `source.to_s` / `target.to_s`, and Puppet::Resource#to_s is its
//     `Type[title]` ref (openvoxproject/openvox, lib/puppet/relationship.rb).
//
// Accepting both is not the "sniff the shape" behavior
// internal/compiler/doc.go rules out for the v4 response envelope.
// There, one endpoint has exactly one envelope and the version is known
// at the call site. Here a single documented container, the plain array,
// genuinely carries either vertex form, and the PuppetDB terminus itself
// branches on precisely this (`edge[vertex] = resource_ref_to_hash(...)
// if edge[vertex].is_a?(String)` in munge_edges). This type mirrors the
// terminus rather than inventing a rule.
type resourceSpecWire struct {
	Type  string `json:"type"`
	Title string `json:"title"`
}

// resourceSpecObject is resourceSpecWire's object form, declared
// separately so UnmarshalJSON can decode into it without recursing into
// itself.
type resourceSpecObject struct {
	Type  string `json:"type"`
	Title string `json:"title"`
}

// UnmarshalJSON decodes either vertex form. A reference string that does
// not parse, or a form missing its type or title, is an error rather
// than a silently zero-valued vertex: the Ruby original yields `{nil,
// nil}` on a non-matching ref, which this package's contract forbids,
// since unknown or malformed catalog data is an operational
// normalization failure and never an empty catalog. The offending
// reference is named in the error because a resource identity is not
// secret in this model, every report printing identities like
// `Service[nginx]`, and it is the one detail that makes the failure
// actionable.
func (s *resourceSpecWire) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var ref string
		if err := json.Unmarshal(trimmed, &ref); err != nil {
			return fmt.Errorf("decoding resource reference: %w", err)
		}
		match := resourceReferencePattern.FindStringSubmatch(ref)
		if match == nil {
			return fmt.Errorf("resource reference %q is not in Type[title] form", ref)
		}
		s.Type, s.Title = match[1], match[2]
		return nil
	}

	var obj resourceSpecObject
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return fmt.Errorf("decoding resource spec: %w", err)
	}
	if obj.Type == "" || obj.Title == "" {
		return fmt.Errorf(`resource spec is missing its "type" or "title"`)
	}
	s.Type, s.Title = obj.Type, obj.Title
	return nil
}

// compilerEdgeEntry is one element of a plain-array edges list:
// `{"source": <vertex>, "target": <vertex>, "relationship":
// <relationship>}`, where each vertex is either form resourceSpecWire
// accepts. A live compiler response carries no relationship field at
// all; it is decoded here so the filter cannot be escaped by writing a
// stored catalog's edges in this shape. See containmentRelationship.
type compilerEdgeEntry struct {
	Source       resourceSpecWire `json:"source"`
	Target       resourceSpecWire `json:"target"`
	Relationship string           `json:"relationship"`
}

// shapeContainer decides, from the first non-whitespace byte of raw,
// which of the two documented top-level shapes (doc.go) a "resources" or
// "edges" catalog field uses: a JSON object (PuppetDB's `{href, data}`
// expansion) or a JSON array (the compiler's plain array). Any other
// leading byte (or an empty/all-whitespace field) is an unrecognized
// shape and returns an error, never a silently empty result: an unknown
// required shape is a reported normalization error, never silently
// discarded.
func shapeContainer(raw json.RawMessage) (isObject bool, trimmed []byte, err error) {
	trimmed = bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false, nil, fmt.Errorf("field is missing or empty")
	}
	switch trimmed[0] {
	case '{':
		return true, trimmed, nil
	case '[':
		return false, trimmed, nil
	default:
		return false, nil, fmt.Errorf("field is neither a {href, data} object nor an array")
	}
}

// hrefDataArray decodes a PuppetDB `{href, data}` expansion's "data"
// array into dest (a pointer to a slice type), requiring "data" to be
// present as a JSON object key. A missing "data" key is treated as an
// unrecognized/malformed shape rather than silently yielding an empty
// slice, since encoding/json would otherwise leave dest at its zero value
// with no indication the field was absent from the response at all.
func hrefDataArray(trimmed []byte, dest any) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return fmt.Errorf("decoding {href, data} object: %w", err)
	}
	dataRaw, ok := probe["data"]
	if !ok {
		return fmt.Errorf(`{href, data}-shaped field is missing its required "data" key`)
	}
	if trimmed := bytes.TrimSpace(dataRaw); len(trimmed) == 0 || trimmed[0] != '[' {
		return fmt.Errorf("data must be an array")
	}
	if err := json.Unmarshal(dataRaw, dest); err != nil {
		return fmt.Errorf("decoding \"data\" array: %w", err)
	}
	return nil
}

// extractResources converts raw's "resources" catalog field, in either
// documented wire shape, into a flat list of resourceWire entries. See
// doc.go for the two shapes recognized.
func extractResources(raw json.RawMessage) ([]resourceWire, error) {
	isObject, trimmed, err := shapeContainer(raw)
	if err != nil {
		return nil, fmt.Errorf("resources: %w", err)
	}
	if isObject {
		var list []resourceWire
		if err := hrefDataArray(trimmed, &list); err != nil {
			return nil, fmt.Errorf("resources: %w", err)
		}
		return list, nil
	}
	var list []resourceWire
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return nil, fmt.Errorf("resources: decoding array: %w", err)
	}
	return list, nil
}

// extractEdges converts raw's "edges" catalog field, in either documented
// wire shape, into a flat list of shape-independent edgeWire entries. See
// doc.go for the two shapes recognized.
func extractEdges(raw json.RawMessage) ([]edgeWire, error) {
	isObject, trimmed, err := shapeContainer(raw)
	if err != nil {
		return nil, fmt.Errorf("edges: %w", err)
	}
	if isObject {
		var pdbEntries []pdbEdgeEntry
		if err := hrefDataArray(trimmed, &pdbEntries); err != nil {
			return nil, fmt.Errorf("edges: %w", err)
		}
		out := make([]edgeWire, 0, len(pdbEntries))
		for _, e := range pdbEntries {
			if isSynthesizedRelationship(e.Relationship) {
				continue
			}
			out = append(out, edgeWire{
				SourceType:  e.SourceType,
				SourceTitle: e.SourceTitle,
				TargetType:  e.TargetType,
				TargetTitle: e.TargetTitle,
			})
		}
		return out, nil
	}
	var compilerEntries []compilerEdgeEntry
	if err := json.Unmarshal(trimmed, &compilerEntries); err != nil {
		return nil, fmt.Errorf("edges: decoding array: %w", err)
	}
	out := make([]edgeWire, 0, len(compilerEntries))
	for _, e := range compilerEntries {
		if isSynthesizedRelationship(e.Relationship) {
			continue
		}
		out = append(out, edgeWire{
			SourceType:  e.Source.Type,
			SourceTitle: e.Source.Title,
			TargetType:  e.Target.Type,
			TargetTitle: e.Target.Title,
		})
	}
	return out, nil
}

func decodeSensitivity(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var names []string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &names) != nil {
		return nil, fmt.Errorf("sensitive_parameters must be an array of parameter names")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			return nil, fmt.Errorf("sensitive_parameters contains an empty or duplicate name")
		}
		seen[name] = true
	}
	sort.Strings(names)
	return names, nil
}
