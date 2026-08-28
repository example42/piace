package normalize

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
)

// pdbShapedCatalog builds a puppetdb.Catalog whose Resources/Edges use
// the documented PuppetDB query-API {href, data} expansion shape (see
// doc.go), for a baseline-catalog-shaped test input.
func pdbShapedCatalog(certname, environment, resourcesJSON, edgesJSON string) puppetdb.Catalog {
	return puppetdb.Catalog{
		Certname:    certname,
		Environment: environment,
		Resources:   json.RawMessage(`{"href":"/x","data":` + resourcesJSON + `}`),
		Edges:       json.RawMessage(`{"href":"/x","data":` + edgesJSON + `}`),
	}
}

// compilerShapedCatalog builds a puppetdb.Catalog whose Resources/Edges
// use the compiler's documented plain-array catalog wire format shape
// (see doc.go), for a candidate-catalog-shaped test input.
func compilerShapedCatalog(certname, environment, resourcesJSON, edgesJSON string) puppetdb.Catalog {
	return puppetdb.Catalog{
		Certname:    certname,
		Environment: environment,
		Resources:   json.RawMessage(resourcesJSON),
		Edges:       json.RawMessage(edgesJSON),
	}
}

// TestCatalog_PuppetDBShape_ResourceAndEdgeIdentity verifies a PuppetDB
// query-API {href, data}-shaped catalog normalizes into the exact
// Type[title] resource identities and (source, target) edge identities,
// per design.md section 7.1.
func TestCatalog_PuppetDBShape_ResourceAndEdgeIdentity(t *testing.T) {
	raw := pdbShapedCatalog("web-01.example.test", "production",
		`[
			{"certname":"web-01.example.test","resource":"aaa","type":"File","title":"/etc/motd","exported":false,"tags":["a"],"file":"/x.pp","line":3,"parameters":{"ensure":"file","mode":"0644"}},
			{"certname":"web-01.example.test","resource":"bbb","type":"Notify","title":"hello","exported":false,"tags":[],"file":"/x.pp","line":1,"parameters":{"message":"hi"}}
		]`,
		`[
			{"relationship":"before","source_type":"Notify","source_title":"hello","target_type":"File","target_title":"/etc/motd"}
		]`,
	)

	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}

	if len(got.Resources) != 2 {
		t.Fatalf("Resources = %+v, want 2 entries", got.Resources)
	}
	// Sorted by Type then Title: "File[/etc/motd]" before "Notify[hello]".
	if got.Resources[0].Identity != (model.ResourceIdentity{Type: "File", Title: "/etc/motd"}) {
		t.Errorf("Resources[0].Identity = %+v", got.Resources[0].Identity)
	}
	if got.Resources[0].Parameters["ensure"] != "file" {
		t.Errorf("Resources[0].Parameters[ensure] = %v", got.Resources[0].Parameters["ensure"])
	}
	if got.Resources[1].Identity != (model.ResourceIdentity{Type: "Notify", Title: "hello"}) {
		t.Errorf("Resources[1].Identity = %+v", got.Resources[1].Identity)
	}

	if len(got.Edges) != 1 {
		t.Fatalf("Edges = %+v, want 1 entry", got.Edges)
	}
	want := model.Edge{Source: "Notify[hello]", Target: "File[/etc/motd]"}
	if got.Edges[0] != want {
		t.Errorf("Edges[0] = %+v, want %+v", got.Edges[0], want)
	}
}

// TestCatalog_CompilerShape_ResourceAndEdgeIdentity verifies the
// compiler's plain-array catalog wire format normalizes identically to
// the PuppetDB shape for equivalent semantic content, since a candidate
// catalog (compiler-sourced) must be comparable against a baseline
// catalog (PuppetDB- or file-sourced) by the differ.
func TestCatalog_CompilerShape_ResourceAndEdgeIdentity(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[
			{"type":"File","title":"/etc/motd","aliases":[],"exported":false,"file":"/x.pp","line":3,"tags":["a"],"parameters":{"ensure":"file","mode":"0644"}},
			{"type":"Notify","title":"hello","aliases":[],"exported":false,"file":"/x.pp","line":1,"tags":[],"parameters":{"message":"hi"}}
		]`,
		`[
			{"source":{"type":"Notify","title":"hello"},"target":{"type":"File","title":"/etc/motd"},"relationship":"before"}
		]`,
	)

	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}

	if len(got.Resources) != 2 {
		t.Fatalf("Resources = %+v, want 2 entries", got.Resources)
	}
	if got.Resources[0].Identity != (model.ResourceIdentity{Type: "File", Title: "/etc/motd"}) {
		t.Errorf("Resources[0].Identity = %+v", got.Resources[0].Identity)
	}
	if len(got.Edges) != 1 {
		t.Fatalf("Edges = %+v, want 1 entry", got.Edges)
	}
	want := model.Edge{Source: "Notify[hello]", Target: "File[/etc/motd]"}
	if got.Edges[0] != want {
		t.Errorf("Edges[0] = %+v, want %+v", got.Edges[0], want)
	}
}

// TestCatalog_DropsTagsFileLineAndOtherMetadata verifies that tags,
// source file/line, exported, aliases, certname/resource fields never
// appear anywhere in the normalized model.Resource, per requirements.md
// 5.9. Since model.Resource has no field at all for these, this test
// verifies indirectly: parameters are exactly what was supplied, and
// nothing else leaked in as an extra "parameter".
func TestCatalog_DropsTagsFileLineAndOtherMetadata(t *testing.T) {
	raw := pdbShapedCatalog("web-01.example.test", "production",
		`[{"certname":"web-01.example.test","resource":"aaa","type":"File","title":"/etc/motd","exported":true,"tags":["a","b"],"file":"/manifests/site.pp","line":42,"parameters":{"ensure":"file"}}]`,
		`[]`,
	)

	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("Resources = %+v", got.Resources)
	}
	params := got.Resources[0].Parameters
	if len(params) != 1 {
		t.Fatalf("Parameters = %+v, want exactly {ensure: file}", params)
	}
	if params["ensure"] != "file" {
		t.Errorf("Parameters[ensure] = %v", params["ensure"])
	}
	// Serialize just the resources (not the whole catalog, which
	// legitimately carries its own top-level "certname" field per
	// model.NormalizedCatalog) and confirm no tag/file/line/exported
	// metadata *key* appears anywhere in a resource. Matching on the
	// JSON key form ("file":) rather than a bare substring avoids a
	// false positive on a legitimate parameter value that happens to
	// contain one of these words (e.g. a File resource's own
	// "ensure":"file" parameter value).
	data, err := json.Marshal(got.Resources)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, forbidden := range []string{`"tags":`, `"file":`, `"line":`, `"exported":`, `"aliases":`, `"certname":`, `"resource":`} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("serialized normalized resources unexpectedly contain %s: %s", forbidden, data)
		}
	}
}

// TestCatalog_DropsAliasParameter verifies the `alias` metaparameter the
// PuppetDB terminus injects into a stored catalog is dropped from both
// wire shapes, so a PuppetDB baseline and a compiled candidate do not
// differ by it alone (requirements.md 5.9; see doc.go).
func TestCatalog_DropsAliasParameter(t *testing.T) {
	resources := `[{"type":"File","title":"info scripts","parameters":{"path":"/etc/tp/run_info","alias":["/etc/tp/run_info"]}}]`

	for _, tc := range []struct {
		name string
		raw  puppetdb.Catalog
	}{
		{"puppetdb shape", pdbShapedCatalog("web-01.example.test", "production", resources, `[]`)},
		{"compiler shape", compilerShapedCatalog("web-01.example.test", "production", resources, `[]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, diag := Catalog(tc.raw)
			if diag != nil {
				t.Fatalf("unexpected diagnostic: %+v", diag)
			}
			params := got.Resources[0].Parameters
			if _, ok := params["alias"]; ok {
				t.Errorf("alias parameter was not dropped: %+v", params)
			}
			if params["path"] != "/etc/tp/run_info" {
				t.Errorf("Parameters[path] = %v, want the declared value preserved", params["path"])
			}
			if len(params) != 1 {
				t.Errorf("Parameters = %+v, want exactly {path: ...}", params)
			}
		})
	}
}

// TestCatalog_ResourceWithOnlyAliasParameter verifies a resource whose
// only parameter is the dropped `alias` (Stage[main] and Class[main] are
// exactly this in a stored catalog) normalizes to an empty parameter map,
// not to a diagnostic or a resource carrying a leftover key.
func TestCatalog_ResourceWithOnlyAliasParameter(t *testing.T) {
	raw := pdbShapedCatalog("web-01.example.test", "production",
		`[{"type":"Stage","title":"main","parameters":{"alias":["main"]}}]`, `[]`)

	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("Resources = %+v", got.Resources)
	}
	if len(got.Resources[0].Parameters) != 0 {
		t.Errorf("Parameters = %+v, want empty", got.Resources[0].Parameters)
	}
}

// TestCatalog_RejectsMalformedResourcesShape verifies an unrecognized
// "resources" shape (neither object nor array) produces a reported
// model.OperationNormalize diagnostic and a zero NormalizedCatalog,
// rather than silently discarding the catalog, per this task's brief.
func TestCatalog_RejectsMalformedResourcesShape(t *testing.T) {
	raw := puppetdb.Catalog{
		Certname:    "web-01.example.test",
		Environment: "production",
		Resources:   json.RawMessage(`"not-an-object-or-array"`),
		Edges:       json.RawMessage(`[]`),
	}
	got, diag := Catalog(raw)
	if diag == nil {
		t.Fatal("expected a diagnostic for malformed resources shape, got none")
	}
	if diag.Operation != model.OperationNormalize {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationNormalize)
	}
	if diag.Severity != model.SeverityError {
		t.Errorf("Severity = %q, want %q", diag.Severity, model.SeverityError)
	}
	if len(got.Resources) != 0 || len(got.Edges) != 0 {
		t.Errorf("expected a zero NormalizedCatalog alongside a diagnostic, got %+v", got)
	}
}

// TestCatalog_RejectsMissingDataKeyInHrefShape verifies a {href}-object
// missing its required "data" key is a reported error, not an empty
// resource list.
func TestCatalog_RejectsMissingDataKeyInHrefShape(t *testing.T) {
	raw := puppetdb.Catalog{
		Certname:  "web-01.example.test",
		Resources: json.RawMessage(`{"href":"/x"}`),
		Edges:     json.RawMessage(`[]`),
	}
	_, diag := Catalog(raw)
	if diag == nil {
		t.Fatal("expected a diagnostic for a {href}-object missing \"data\", got none")
	}
	if diag.Operation != model.OperationNormalize {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationNormalize)
	}
}

// TestCatalog_RejectsResourceMissingTypeOrTitle verifies a resource
// entry missing its required type or title is a reported error.
func TestCatalog_RejectsResourceMissingTypeOrTitle(t *testing.T) {
	cases := []string{
		`[{"type":"","title":"/etc/motd","parameters":{}}]`,
		`[{"type":"File","title":"","parameters":{}}]`,
	}
	for _, resourcesJSON := range cases {
		raw := compilerShapedCatalog("web-01.example.test", "production", resourcesJSON, `[]`)
		_, diag := Catalog(raw)
		if diag == nil {
			t.Errorf("resources=%s: expected a diagnostic, got none", resourcesJSON)
			continue
		}
		if diag.Operation != model.OperationNormalize {
			t.Errorf("resources=%s: Operation = %q, want %q", resourcesJSON, diag.Operation, model.OperationNormalize)
		}
	}
}

// TestCatalog_RejectsDuplicateResourceIdentity verifies two resources
// sharing the same Type[title] identity is a reported error rather than
// silently keeping one and dropping the other.
func TestCatalog_RejectsDuplicateResourceIdentity(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[{"type":"File","title":"/etc/motd","parameters":{}},{"type":"File","title":"/etc/motd","parameters":{"ensure":"file"}}]`,
		`[]`,
	)
	_, diag := Catalog(raw)
	if diag == nil {
		t.Fatal("expected a diagnostic for a duplicate resource identity, got none")
	}
	if diag.Operation != model.OperationNormalize {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationNormalize)
	}
}

// TestCatalog_RejectsEdgeMissingEndpointTypeOrTitle verifies an edge
// entry with a missing source/target type or title is a reported error.
func TestCatalog_RejectsEdgeMissingEndpointTypeOrTitle(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[]`,
		`[{"source":{"type":"Notify","title":""},"target":{"type":"File","title":"/etc/motd"}}]`,
	)
	_, diag := Catalog(raw)
	if diag == nil {
		t.Fatal("expected a diagnostic for an edge missing an endpoint title, got none")
	}
	if diag.Operation != model.OperationNormalize {
		t.Errorf("Operation = %q, want %q", diag.Operation, model.OperationNormalize)
	}
}

// TestCatalog_CaseSensitiveIdentity verifies resource identity uses no
// case folding: "file[/x]" and "File[/x]" are distinct identities, per
// design.md section 7.1 ("type and title are strings with no case
// folding").
func TestCatalog_CaseSensitiveIdentity(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[{"type":"File","title":"/x","parameters":{}},{"type":"file","title":"/x","parameters":{}}]`,
		`[]`,
	)
	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if len(got.Resources) != 2 {
		t.Fatalf("Resources = %+v, want 2 distinct case-sensitive identities", got.Resources)
	}
}

// TestCatalog_NumberCanonicalization verifies a numeric parameter value
// normalizes to its exact decimal digits (Property 1), matching
// internal/snapshot's canonical number algorithm, regardless of how the
// literal was spelled.
func TestCatalog_NumberCanonicalization(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[{"type":"File","title":"/x","parameters":{"mode":1.50,"count":100}}]`,
		`[]`,
	)
	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	params := got.Resources[0].Parameters
	if params["mode"] != model.Number("1.5") {
		t.Errorf("Parameters[mode] = %#v, want model.Number(\"1.5\")", params["mode"])
	}
	if params["count"] != model.Number("100") {
		t.Errorf("Parameters[count] = %#v, want model.Number(\"100\")", params["count"])
	}
}

// TestCatalog_ArrayOrderPreservedObjectKeysCanonical verifies array
// parameter values retain order while nested object parameter values are
// still exactly comparable (map equality does not depend on encounter
// order), per design.md section 7.1: "arrays retain order; object keys
// sort recursively."
func TestCatalog_ArrayOrderPreservedObjectKeysCanonical(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[{"type":"File","title":"/x","parameters":{"list":[3,1,2],"nested":{"b":1,"a":2}}}]`,
		`[]`,
	)
	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	params := got.Resources[0].Parameters
	list, ok := params["list"].([]model.Value)
	if !ok || len(list) != 3 {
		t.Fatalf("Parameters[list] = %#v, want a 3-element []model.Value", params["list"])
	}
	if list[0] != model.Number("3") || list[1] != model.Number("1") || list[2] != model.Number("2") {
		t.Errorf("Parameters[list] order not preserved: %#v", list)
	}
	nested, ok := params["nested"].(map[string]model.Value)
	if !ok {
		t.Fatalf("Parameters[nested] = %#v, want a map[string]model.Value", params["nested"])
	}
	if nested["a"] != model.Number("2") || nested["b"] != model.Number("1") {
		t.Errorf("Parameters[nested] = %#v", nested)
	}
}

// TestCatalog_EmptyResourcesAndEdges verifies an empty (but present)
// resources/edges array normalizes to an empty catalog, not an error.
func TestCatalog_EmptyResourcesAndEdges(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production", `[]`, `[]`)
	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if len(got.Resources) != 0 || len(got.Edges) != 0 {
		t.Errorf("got %+v, want empty resources/edges", got)
	}
	if got.Certname != "web-01.example.test" || got.Environment != "production" {
		t.Errorf("top-level identity fields not preserved: %+v", got)
	}
}

// TestCatalog_LargeIntegerPreservesAllDigits verifies a large integer
// parameter value (far beyond float64's 53-bit mantissa precision) keeps
// every digit exactly, per this task's brief and design.md section 7.1's
// "number comparisons use exact normalized decimal values rather than
// machine floating point."
func TestCatalog_LargeIntegerPreservesAllDigits(t *testing.T) {
	const bigDigits = "123456789012345678901234567890"
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[{"type":"File","title":"/x","parameters":{"serial":`+bigDigits+`}}]`,
		`[]`,
	)
	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if got.Resources[0].Parameters["serial"] != model.Number(bigDigits) {
		t.Errorf("Parameters[serial] = %#v, want model.Number(%q)", got.Resources[0].Parameters["serial"], bigDigits)
	}
}

// TestCatalog_CompilerShape_StringResourceReferenceEdges covers the edge
// vertex form a real compiler actually returns: a `Type[title]` reference
// string, not a `{type, title}` object. Puppet::Relationship#to_data_hash
// serializes each vertex as `source.to_s`/`target.to_s`, so this is what
// every v3/v4 catalog response and every `capture catalog` snapshot
// carries — the object form only appears in a terminus-submitted wire
// format v8 catalog.
func TestCatalog_CompilerShape_StringResourceReferenceEdges(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[
			{"type":"File","title":"/etc/motd","parameters":{"ensure":"file"}},
			{"type":"Notify","title":"hello","parameters":{"message":"hi"}}
		]`,
		`[
			{"source":"Notify[hello]","target":"File[/etc/motd]"},
			{"source":"Class[Main]","target":"Notify[hello]"}
		]`,
	)

	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	want := []model.Edge{
		{Source: "Class[Main]", Target: "Notify[hello]"},
		{Source: "Notify[hello]", Target: "File[/etc/motd]"},
	}
	if len(got.Edges) != len(want) {
		t.Fatalf("Edges = %+v, want %+v", got.Edges, want)
	}
	for i := range want {
		if got.Edges[i] != want[i] {
			t.Errorf("Edges[%d] = %+v, want %+v", i, got.Edges[i], want[i])
		}
	}
}

// TestCatalog_ResourceReferenceCompositeTitle pins the split semantics
// ported from the PuppetDB terminus's resource_ref_to_hash regex: the
// type stops at the first bracket and the title runs to the last one, so
// a title that itself contains brackets survives intact. A naive split on
// the first "]" would truncate it, and the resulting identity would not
// match the same resource's identity on the PuppetDB side of the
// comparison.
func TestCatalog_ResourceReferenceCompositeTitle(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[]`,
		`[{"source":"Class[Main]","target":"File[/etc/foo[bar]]"}]`,
	)

	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	want := model.Edge{Source: "Class[Main]", Target: "File[/etc/foo[bar]]"}
	if len(got.Edges) != 1 || got.Edges[0] != want {
		t.Fatalf("Edges = %+v, want [%+v]", got.Edges, want)
	}
}

// TestCatalog_MixedEdgeVertexForms covers one edge carrying one vertex of
// each form. The terminus converts per vertex (`%w[source target].each`),
// so a half-converted edge is representable and must not be rejected.
func TestCatalog_MixedEdgeVertexForms(t *testing.T) {
	raw := compilerShapedCatalog("web-01.example.test", "production",
		`[]`,
		`[{"source":"Class[Main]","target":{"type":"Notify","title":"hello"}}]`,
	)

	got, diag := Catalog(raw)
	if diag != nil {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	want := model.Edge{Source: "Class[Main]", Target: "Notify[hello]"}
	if len(got.Edges) != 1 || got.Edges[0] != want {
		t.Fatalf("Edges = %+v, want [%+v]", got.Edges, want)
	}
}

// TestCatalog_RejectsUnparseableResourceReference asserts a reference
// string that does not match Type[title] is a reported normalization
// failure. The Ruby original silently yields {nil, nil} there; this
// package's contract forbids a silently empty result.
func TestCatalog_RejectsUnparseableResourceReference(t *testing.T) {
	for _, ref := range []string{"Notify hello", "Notify[]", "[hello]", ""} {
		body, err := json.Marshal(map[string]any{"source": ref, "target": "Notify[hello]"})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		raw := compilerShapedCatalog("web-01.example.test", "production", `[]`, "["+string(body)+"]")

		_, diag := Catalog(raw)
		if diag == nil {
			t.Errorf("ref %q: expected a diagnostic, got none", ref)
			continue
		}
		if diag.Operation != model.OperationNormalize {
			t.Errorf("ref %q: Operation = %q, want %q", ref, diag.Operation, model.OperationNormalize)
		}
	}
}
