package diff

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/filecontent"
	"github.com/example42/piace/internal/model"
)

// --- fixtures ---

const testCertname = "node1.example.com"

func target(exclude []config.ExclusionRule, redact []config.RedactionSelector) resolve.Target {
	return resolve.Target{
		Certname: testCertname,
		Candidate: resolve.Candidate{
			Environment: "candidate",
			CatalogAPI:  config.CatalogAPIv4,
		},
		Exclude: exclude,
		Redact:  redact,
	}
}

func catalog(resources []model.Resource, edges []model.Edge) model.NormalizedCatalog {
	return model.NormalizedCatalog{
		Certname:    testCertname,
		Environment: "candidate",
		Resources:   resources,
		Edges:       edges,
	}
}

func resource(resourceType, title string, params map[string]model.Value) model.Resource {
	return model.Resource{
		Identity:   model.ResourceIdentity{Type: resourceType, Title: title},
		Parameters: params,
	}
}

// sensitive builds the Pcore generic-data encoding of a Sensitive-wrapped
// payload, as documented in doc.go's "Redaction source 1" section.
func sensitive(payload model.Value) map[string]model.Value {
	return map[string]model.Value{"__ptype": "Sensitive", "__pvalue": payload}
}

// stubRetriever returns a fixed digest for every reference, or a fixed
// error. It never sees or returns content bytes.
type stubRetriever struct {
	digests map[string]string
	err     error
}

func (s stubRetriever) Digest(_ context.Context, reference string, _ filecontent.RetrievalContext) (filecontent.DigestEvidence, error) {
	if s.err != nil {
		return filecontent.DigestEvidence{}, s.err
	}
	digest, ok := s.digests[reference]
	if !ok {
		return filecontent.DigestEvidence{}, errors.New("no stub digest for reference")
	}
	return filecontent.DigestEvidence{Algorithm: "sha256", Digest: digest}, nil
}

func run(t *testing.T, tgt resolve.Target, before, after model.NormalizedCatalog, retriever filecontent.ContentRetriever) (model.NodeDiff, []model.Diagnostic) {
	t.Helper()
	return Diff(context.Background(), tgt, before, after, retriever)
}

func findChange(t *testing.T, nd model.NodeDiff, resourceType, title, parameter string) model.ResourceChange {
	t.Helper()
	for _, c := range nd.ResourceChanges {
		if c.Identity.Type == resourceType && c.Identity.Title == title && c.Parameter == parameter {
			return c
		}
	}
	t.Fatalf("no change for %s[%s] parameter %q in %+v", resourceType, title, parameter, nd.ResourceChanges)
	return model.ResourceChange{}
}

// --- pass 1: graph diff ---

func TestDiff_ResourceAddedRemovedAndParameterChanged(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Package", "nginx", map[string]model.Value{"ensure": "1.0"}),
		resource("Service", "old", nil),
	}, nil)
	after := catalog([]model.Resource{
		resource("Package", "nginx", map[string]model.Value{"ensure": "2.0"}),
		resource("Service", "new", nil),
	}, nil)

	nd, diags := run(t, target(nil, nil), before, after, nil)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if !nd.HasDifference {
		t.Fatal("expected HasDifference")
	}
	if nd.Certname != testCertname {
		t.Fatalf("certname = %q, want %q", nd.Certname, testCertname)
	}
	if len(nd.ResourceChanges) != 3 {
		t.Fatalf("got %d resource changes, want 3: %+v", len(nd.ResourceChanges), nd.ResourceChanges)
	}

	// Sorted by (Type, Title): Package[nginx], Service[new], Service[old].
	want := []struct {
		kind      model.ChangeKind
		identity  string
		parameter string
	}{
		{model.ChangeParameterChanged, "Package[nginx]", "ensure"},
		{model.ChangeResourceAdded, "Service[new]", ""},
		{model.ChangeResourceRemoved, "Service[old]", ""},
	}
	for i, w := range want {
		got := nd.ResourceChanges[i]
		if got.Kind != w.kind || got.Identity.String() != w.identity || got.Parameter != w.parameter {
			t.Errorf("change %d = {%s %s %q}, want {%s %s %q}",
				i, got.Kind, got.Identity, got.Parameter, w.kind, w.identity, w.parameter)
		}
	}

	changed := nd.ResourceChanges[0]
	if changed.Before != "1.0" || changed.After != "2.0" {
		t.Errorf("before/after = %v/%v, want 1.0/2.0", changed.Before, changed.After)
	}
}

// Added and removed resources are identified by identity only. Their
// parameters, which for a File resource would be the managed content
// bytes themselves, must never reach the projection.
func TestDiff_AddedResourceCarriesNoParameterProjection(t *testing.T) {
	after := catalog([]model.Resource{
		resource("File", "/etc/secret.conf", map[string]model.Value{
			"content": "top-secret-managed-bytes",
			"owner":   "root",
		}),
	}, nil)

	nd, _ := run(t, target(nil, nil), catalog(nil, nil), after, nil)
	if len(nd.ResourceChanges) != 1 {
		t.Fatalf("got %d changes, want 1", len(nd.ResourceChanges))
	}
	change := nd.ResourceChanges[0]
	if change.Kind != model.ChangeResourceAdded {
		t.Fatalf("kind = %s, want resource_added", change.Kind)
	}
	if change.Before != nil || change.After != nil {
		t.Errorf("added resource carries a value projection: before=%v after=%v", change.Before, change.After)
	}
	encoded, err := json.Marshal(nd)
	if err != nil {
		t.Fatalf("marshaling node diff: %v", err)
	}
	if strings.Contains(string(encoded), "top-secret-managed-bytes") {
		t.Errorf("serialized node diff contains managed content bytes: %s", encoded)
	}
}

func TestDiff_EdgeAddedAndRemovedSortedDeterministically(t *testing.T) {
	before := catalog(nil, []model.Edge{
		{Source: "Package[nginx]", Target: "Service[nginx]"},
		{Source: "File[/etc/a]", Target: "Service[nginx]"},
	})
	after := catalog(nil, []model.Edge{
		{Source: "Package[nginx]", Target: "Service[nginx]"},
		{Source: "File[/etc/b]", Target: "Service[nginx]"},
	})

	nd, _ := run(t, target(nil, nil), before, after, nil)
	if len(nd.EdgeChanges) != 2 {
		t.Fatalf("got %d edge changes, want 2: %+v", len(nd.EdgeChanges), nd.EdgeChanges)
	}
	if nd.EdgeChanges[0].Edge.Source != "File[/etc/a]" || nd.EdgeChanges[0].Kind != model.ChangeEdgeRemoved {
		t.Errorf("edge change 0 = %+v, want removed File[/etc/a]", nd.EdgeChanges[0])
	}
	if nd.EdgeChanges[1].Edge.Source != "File[/etc/b]" || nd.EdgeChanges[1].Kind != model.ChangeEdgeAdded {
		t.Errorf("edge change 1 = %+v, want added File[/etc/b]", nd.EdgeChanges[1])
	}
	if !nd.HasDifference {
		t.Error("expected HasDifference for an edge-only difference")
	}
}

// Edge direction is significant.
func TestDiff_EdgeDirectionIsSignificant(t *testing.T) {
	before := catalog(nil, []model.Edge{{Source: "A[x]", Target: "B[y]"}})
	after := catalog(nil, []model.Edge{{Source: "B[y]", Target: "A[x]"}})

	nd, _ := run(t, target(nil, nil), before, after, nil)
	if len(nd.EdgeChanges) != 2 {
		t.Fatalf("got %d edge changes, want 2 (one removed, one added): %+v", len(nd.EdgeChanges), nd.EdgeChanges)
	}
}

func TestDiff_IdentityIsCaseSensitive(t *testing.T) {
	before := catalog([]model.Resource{resource("File", "/etc/A", nil)}, nil)
	after := catalog([]model.Resource{resource("File", "/etc/a", nil)}, nil)

	nd, _ := run(t, target(nil, nil), before, after, nil)
	if len(nd.ResourceChanges) != 2 {
		t.Fatalf("got %d changes, want 2 (case differences are distinct identities): %+v",
			len(nd.ResourceChanges), nd.ResourceChanges)
	}
}

func TestDiff_NoDifferences(t *testing.T) {
	c := catalog([]model.Resource{
		resource("Package", "nginx", map[string]model.Value{"ensure": "1.0", "n": model.Number("42")}),
	}, []model.Edge{{Source: "Package[nginx]", Target: "Service[nginx]"}})

	nd, diags := run(t, target(nil, nil), c, c, nil)
	if nd.HasDifference {
		t.Errorf("expected no difference, got %+v", nd)
	}
	if len(nd.ResourceChanges) != 0 || len(nd.EdgeChanges) != 0 {
		t.Errorf("expected empty diff, got %+v", nd)
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
}

// An absent parameter and one explicitly present with undef are the same
// semantic state (see diffParameters); reporting the pair would emit a
// change row with nothing in it.
func TestDiff_AbsentParameterEqualsExplicitUndef(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Package", "nginx", map[string]model.Value{"ensure": "1.0", "provider": nil}),
	}, nil)
	after := catalog([]model.Resource{
		resource("Package", "nginx", map[string]model.Value{"ensure": "1.0"}),
	}, nil)

	nd, _ := run(t, target(nil, nil), before, after, nil)
	if nd.HasDifference {
		t.Errorf("absent vs explicit undef reported as a difference: %+v", nd.ResourceChanges)
	}
}

func TestDiff_NumbersCompareByCanonicalDecimal(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Package", "nginx", map[string]model.Value{"port": model.Number("100")}),
	}, nil)
	same := catalog([]model.Resource{
		resource("Package", "nginx", map[string]model.Value{"port": model.Number("100")}),
	}, nil)
	other := catalog([]model.Resource{
		resource("Package", "nginx", map[string]model.Value{"port": model.Number("101")}),
	}, nil)

	if nd, _ := run(t, target(nil, nil), before, same, nil); nd.HasDifference {
		t.Errorf("equal canonical numbers reported as a difference: %+v", nd.ResourceChanges)
	}
	nd, diags := run(t, target(nil, nil), before, other, nil)
	if !nd.HasDifference {
		t.Error("distinct numbers not reported as a difference")
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics (a model.Number must be fingerprintable): %+v", diags)
	}
	if nd.ResourceChanges[0].Fingerprint == "" {
		t.Error("numeric change has no fingerprint")
	}
}

// --- pass 2: exclusions ---

func TestDiff_ExcludedDifferenceDoesNotSetHasDifference(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/var/cache/x", map[string]model.Value{"mode": "0644"}),
	}, nil)
	after := catalog([]model.Resource{
		resource("File", "/var/cache/x", map[string]model.Value{"mode": "0600"}),
	}, nil)

	rules := []config.ExclusionRule{{Type: "File", Title: "/var/cache/*"}}
	nd, _ := run(t, target(rules, nil), before, after, nil)

	if nd.HasDifference {
		t.Error("an excluded difference must not set HasDifference")
	}
	if len(nd.ResourceChanges) != 0 {
		t.Errorf("excluded change survived: %+v", nd.ResourceChanges)
	}
	if len(nd.Exclusions) != 1 {
		t.Fatalf("got %d exclusion outcomes, want 1: %+v", len(nd.Exclusions), nd.Exclusions)
	}
	got := nd.Exclusions[0]
	if got.Rule != (model.ExclusionRuleRef{Type: "File", Title: "/var/cache/*"}) {
		t.Errorf("rule ref = %+v", got.Rule)
	}
	if got.SuppressedParameters != 1 || got.SuppressedResources != 0 || got.SuppressedEdges != 0 {
		t.Errorf("counts = %+v, want 1 suppressed parameter only", got)
	}
}

func TestDiff_ExclusionTypeIsExactAndTitleGlobIsCaseSensitive(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/var/Cache/x", map[string]model.Value{"mode": "0644"}),
		resource("file", "/var/cache/y", map[string]model.Value{"mode": "0644"}),
	}, nil)
	after := catalog([]model.Resource{
		resource("File", "/var/Cache/x", map[string]model.Value{"mode": "0600"}),
		resource("file", "/var/cache/y", map[string]model.Value{"mode": "0600"}),
	}, nil)

	rules := []config.ExclusionRule{{Type: "File", Title: "/var/cache/*"}}
	nd, _ := run(t, target(rules, nil), before, after, nil)

	if !nd.HasDifference || len(nd.ResourceChanges) != 2 {
		t.Fatalf("neither a lower-case title nor a lower-case type may match: %+v", nd.ResourceChanges)
	}
	if len(nd.Exclusions) != 0 {
		t.Errorf("expected no applied exclusions, got %+v", nd.Exclusions)
	}
}

func TestDiff_ExcludedEndpointSuppressesEdgeAndAttributesRule(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/var/cache/x", nil),
		resource("Service", "nginx", nil),
	}, []model.Edge{{Source: "File[/var/cache/x]", Target: "Service[nginx]"}})
	after := catalog([]model.Resource{
		resource("File", "/var/cache/x", nil),
		resource("Service", "nginx", nil),
	}, nil)

	rules := []config.ExclusionRule{{Type: "File", Title: "/var/cache/*"}}
	nd, _ := run(t, target(rules, nil), before, after, nil)

	if nd.HasDifference {
		t.Errorf("edge attached to an excluded endpoint must be suppressed: %+v", nd)
	}
	if len(nd.EdgeChanges) != 0 {
		t.Errorf("edge change survived: %+v", nd.EdgeChanges)
	}
	if len(nd.Exclusions) != 1 || nd.Exclusions[0].SuppressedEdges != 1 {
		t.Fatalf("exclusion outcomes = %+v, want 1 suppressed edge", nd.Exclusions)
	}
	if nd.Exclusions[0].Rule.Title != "/var/cache/*" {
		t.Errorf("edge suppression attributed to %+v", nd.Exclusions[0].Rule)
	}
}

// An edge with both endpoints excluded by different rules is counted
// once, against the first matching rule in configured order.
func TestDiff_EdgeWithTwoExcludedEndpointsCountedOnce(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/var/cache/x", nil),
		resource("Exec", "refresh", nil),
	}, []model.Edge{{Source: "File[/var/cache/x]", Target: "Exec[refresh]"}})
	after := catalog([]model.Resource{
		resource("File", "/var/cache/x", nil),
		resource("Exec", "refresh", nil),
	}, nil)

	rules := []config.ExclusionRule{
		{Type: "Exec", Title: "refresh"},
		{Type: "File", Title: "/var/cache/*"},
	}
	nd, _ := run(t, target(rules, nil), before, after, nil)

	total := 0
	for _, o := range nd.Exclusions {
		total += o.SuppressedEdges
	}
	if total != 1 {
		t.Fatalf("suppressed edge counted %d times across %+v, want 1", total, nd.Exclusions)
	}
	if len(nd.Exclusions) != 1 || nd.Exclusions[0].Rule.Type != "Exec" {
		t.Errorf("expected the single suppressed edge attributed to the first matching rule (Exec), got %+v", nd.Exclusions)
	}
}

// An excluded resource that only exists in one catalog is still matched:
// exclusion evaluates every identity in either catalog.
func TestDiff_ExclusionSuppressesAddedAndRemovedResources(t *testing.T) {
	before := catalog([]model.Resource{resource("File", "/tmp/gone", nil)}, nil)
	after := catalog([]model.Resource{resource("File", "/tmp/new", nil)}, nil)

	rules := []config.ExclusionRule{{Type: "File", Title: "/tmp/*"}}
	nd, _ := run(t, target(rules, nil), before, after, nil)

	if nd.HasDifference {
		t.Errorf("expected all differences suppressed, got %+v", nd)
	}
	if len(nd.Exclusions) != 1 || nd.Exclusions[0].SuppressedResources != 2 {
		t.Fatalf("exclusion outcomes = %+v, want 2 suppressed resources", nd.Exclusions)
	}
}

// A configured rule that suppressed nothing is not reported.
func TestDiff_UnmatchedExclusionRuleIsNotReported(t *testing.T) {
	before := catalog([]model.Resource{resource("Package", "nginx", map[string]model.Value{"ensure": "1.0"})}, nil)
	after := catalog([]model.Resource{resource("Package", "nginx", map[string]model.Value{"ensure": "2.0"})}, nil)

	rules := []config.ExclusionRule{{Type: "File", Title: "/var/cache/*"}}
	nd, _ := run(t, target(rules, nil), before, after, nil)

	if len(nd.Exclusions) != 0 {
		t.Errorf("a rule that suppressed nothing was reported: %+v", nd.Exclusions)
	}
	if !nd.HasDifference {
		t.Error("expected the non-excluded difference to remain")
	}
}

// --- pass 3: redaction ---

// The ordering proof: redaction runs strictly after HasDifference is
// fixed, so masking a value never removes a difference.
func TestDiff_RedactedChangeStillCountsAsDifference(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Exec", "login", map[string]model.Value{"password": "old-secret"}),
	}, nil)
	after := catalog([]model.Resource{
		resource("Exec", "login", map[string]model.Value{"password": "new-secret"}),
	}, nil)

	selectors := []config.RedactionSelector{{Type: "Exec", Parameter: "password"}}
	nd, _ := run(t, target(nil, selectors), before, after, nil)

	if !nd.HasDifference {
		t.Fatal("a redacted change must still count as a difference")
	}
	change := findChange(t, nd, "Exec", "login", "password")
	if change.Before != model.RedactedValue || change.After != model.RedactedValue {
		t.Errorf("before/after = %v/%v, want both %q", change.Before, change.After, model.RedactedValue)
	}
	encoded, err := json.Marshal(nd)
	if err != nil {
		t.Fatalf("marshaling node diff: %v", err)
	}
	for _, secret := range []string{"old-secret", "new-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("serialized node diff leaks %q: %s", secret, encoded)
		}
	}
}

func TestDiff_RedactionSelectorIsExactAndCaseSensitive(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Exec", "login", map[string]model.Value{"Password": "old-secret"}),
	}, nil)
	after := catalog([]model.Resource{
		resource("Exec", "login", map[string]model.Value{"Password": "new-secret"}),
	}, nil)

	selectors := []config.RedactionSelector{{Type: "exec", Parameter: "password"}}
	nd, _ := run(t, target(nil, selectors), before, after, nil)

	change := findChange(t, nd, "Exec", "login", "Password")
	if change.Before != "old-secret" {
		t.Errorf("a case-mismatched selector must not match; before = %v", change.Before)
	}
}

func TestDiff_SensitiveWrapperRedactedRecursively(t *testing.T) {
	beforeValue := map[string]model.Value{
		"plain": "visible",
		"nested": []model.Value{
			"also-visible",
			map[string]model.Value{"deep": sensitive("payload-before")},
		},
	}
	afterValue := map[string]model.Value{
		"plain": "visible",
		"nested": []model.Value{
			"also-visible",
			map[string]model.Value{"deep": sensitive("payload-after")},
		},
	}
	before := catalog([]model.Resource{
		resource("Exec", "login", map[string]model.Value{"env": beforeValue}),
	}, nil)
	after := catalog([]model.Resource{
		resource("Exec", "login", map[string]model.Value{"env": afterValue}),
	}, nil)

	nd, _ := run(t, target(nil, nil), before, after, nil)
	if !nd.HasDifference {
		t.Fatal("a changed Sensitive payload is still a difference")
	}
	change := findChange(t, nd, "Exec", "login", "env")

	got, ok := change.Before.(map[string]model.Value)
	if !ok {
		t.Fatalf("before projection type = %T, want map", change.Before)
	}
	if got["plain"] != "visible" {
		t.Errorf("non-sensitive sibling was altered: %v", got["plain"])
	}
	nested, ok := got["nested"].([]model.Value)
	if !ok || len(nested) != 2 {
		t.Fatalf("nested projection = %v", got["nested"])
	}
	if nested[0] != "also-visible" {
		t.Errorf("non-sensitive array element was altered: %v", nested[0])
	}
	deepMap, ok := nested[1].(map[string]model.Value)
	if !ok {
		t.Fatalf("deep projection type = %T, want map", nested[1])
	}
	// The whole wrapper subtree is replaced, not just its payload: a
	// surviving __ptype/__pvalue object would still disclose the
	// payload's shape.
	if deepMap["deep"] != model.RedactedValue {
		t.Errorf("nested Sensitive value = %v, want %q", deepMap["deep"], model.RedactedValue)
	}

	encoded, err := json.Marshal(nd)
	if err != nil {
		t.Fatalf("marshaling node diff: %v", err)
	}
	for _, leak := range []string{"payload-before", "payload-after", "__pvalue", "__ptype"} {
		if strings.Contains(string(encoded), leak) {
			t.Errorf("serialized node diff leaks %q: %s", leak, encoded)
		}
	}
}

// Redaction must not mutate the caller's catalogs in place.
func TestDiff_RedactionDoesNotMutateInputCatalogs(t *testing.T) {
	beforeParams := map[string]model.Value{"env": sensitive("payload-before")}
	afterParams := map[string]model.Value{"env": sensitive("payload-after")}
	before := catalog([]model.Resource{resource("Exec", "login", beforeParams)}, nil)
	after := catalog([]model.Resource{resource("Exec", "login", afterParams)}, nil)

	run(t, target(nil, nil), before, after, nil)

	wrapper, ok := beforeParams["env"].(map[string]model.Value)
	if !ok {
		t.Fatalf("input parameter was replaced: %T", beforeParams["env"])
	}
	if wrapper["__pvalue"] != "payload-before" {
		t.Errorf("input catalog was mutated: %v", wrapper)
	}
}

// --- fingerprints (the anti-merge property) ---

// The whole reason Fingerprint exists: two targets whose sensitive value
// changed differently must not merge into one aggregate group, even
// though both projections read "<redacted>".
func TestDiff_DistinctSensitiveValuesProduceDistinctFingerprints(t *testing.T) {
	makeDiff := func(beforeSecret, afterSecret string) model.NodeDiff {
		before := catalog([]model.Resource{
			resource("Exec", "login", map[string]model.Value{"password": beforeSecret}),
		}, nil)
		after := catalog([]model.Resource{
			resource("Exec", "login", map[string]model.Value{"password": afterSecret}),
		}, nil)
		selectors := []config.RedactionSelector{{Type: "Exec", Parameter: "password"}}
		nd, _ := run(t, target(nil, selectors), before, after, nil)
		return nd
	}

	a := makeDiff("old", "new-a")
	b := makeDiff("old", "new-b")
	same := makeDiff("old", "new-a")

	changeA := findChange(t, a, "Exec", "login", "password")
	changeB := findChange(t, b, "Exec", "login", "password")
	changeSame := findChange(t, same, "Exec", "login", "password")

	if changeA.Before != model.RedactedValue || changeB.Before != model.RedactedValue {
		t.Fatal("precondition: both projections must be redacted")
	}
	if changeA.Fingerprint == "" {
		t.Fatal("no fingerprint computed")
	}
	if changeA.Fingerprint == changeB.Fingerprint {
		t.Error("distinct sensitive changes share a fingerprint and would merge into one aggregate group")
	}
	if changeA.Fingerprint != changeSame.Fingerprint {
		t.Error("identical changes must share a fingerprint or they would never group")
	}
}

// A fingerprint carries equality only: it must not be a recoverable
// encoding of the secret it digests.
func TestDiff_FingerprintDoesNotEmbedValuesAndIsNotSerialized(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Exec", "login", map[string]model.Value{"password": "old-secret"}),
	}, nil)
	after := catalog([]model.Resource{
		resource("Exec", "login", map[string]model.Value{"password": "new-secret"}),
	}, nil)
	selectors := []config.RedactionSelector{{Type: "Exec", Parameter: "password"}}

	nd, _ := run(t, target(nil, selectors), before, after, nil)
	change := findChange(t, nd, "Exec", "login", "password")

	if !strings.HasPrefix(change.Fingerprint, "sha256:") {
		t.Errorf("fingerprint = %q, want a sha256: digest", change.Fingerprint)
	}
	if strings.Contains(change.Fingerprint, "secret") {
		t.Errorf("fingerprint embeds the value: %q", change.Fingerprint)
	}
	encoded, err := json.Marshal(nd)
	if err != nil {
		t.Fatalf("marshaling node diff: %v", err)
	}
	if strings.Contains(string(encoded), change.Fingerprint) {
		t.Errorf("fingerprint reached the serialized report: %s", encoded)
	}
}

// Different change kinds/identities/parameters never collide, so a
// consumer may group on the fingerprint alone.
func TestDiff_FingerprintDistinguishesKindIdentityAndParameter(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Package", "a", map[string]model.Value{"ensure": "1.0", "other": "1.0"}),
		resource("Package", "b", map[string]model.Value{"ensure": "1.0"}),
	}, nil)
	after := catalog([]model.Resource{
		resource("Package", "a", map[string]model.Value{"ensure": "2.0", "other": "2.0"}),
		resource("Package", "b", map[string]model.Value{"ensure": "2.0"}),
	}, nil)

	nd, _ := run(t, target(nil, nil), before, after, nil)
	seen := map[string]string{}
	for _, c := range nd.ResourceChanges {
		label := c.Identity.String() + "." + c.Parameter
		if prior, dup := seen[c.Fingerprint]; dup {
			t.Errorf("%s and %s collide on fingerprint %s", prior, label, c.Fingerprint)
		}
		seen[c.Fingerprint] = label
	}
	if len(seen) != 3 {
		t.Fatalf("got %d distinct fingerprints, want 3: %+v", len(seen), seen)
	}
}

// --- File content evidence ---

func TestDiff_FileContentBearingParametersCollapseToOneEntry(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/etc/app.conf", map[string]model.Value{
			"content":        "before-bytes",
			"source":         "puppet:///modules/app/old.conf",
			"checksum":       "sha256",
			"checksum_value": "aaaa",
			"owner":          "root",
		}),
	}, nil)
	after := catalog([]model.Resource{
		resource("File", "/etc/app.conf", map[string]model.Value{
			"content":        "after-bytes",
			"source":         "puppet:///modules/app/new.conf",
			"checksum":       "md5",
			"checksum_value": "bbbb",
			"owner":          "puppet",
		}),
	}, nil)

	nd, diags := run(t, target(nil, nil), before, after, nil)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	var contentEntries, ownerEntries int
	for _, c := range nd.ResourceChanges {
		switch c.Parameter {
		case "content":
			contentEntries++
		case "owner":
			ownerEntries++
		default:
			t.Errorf("content-bearing parameter %q reported independently: %+v", c.Parameter, c)
		}
	}
	if contentEntries != 1 {
		t.Errorf("got %d content entries, want exactly 1", contentEntries)
	}
	if ownerEntries != 1 {
		t.Errorf("got %d owner entries, want 1", ownerEntries)
	}

	content := findChange(t, nd, "File", "/etc/app.conf", "content")
	if content.FileContent == nil {
		t.Fatal("content entry carries no evidence")
	}
	if content.FileContent.State != model.FileContentChanged {
		t.Errorf("state = %s, want changed", content.FileContent.State)
	}
	if content.FileContent.EvidenceSource != model.FileContentEvidenceInline {
		t.Errorf("evidence source = %s, want inline_content", content.FileContent.EvidenceSource)
	}
	// The content entry carries evidence only, never the bytes.
	if content.Before != nil || content.After != nil {
		t.Errorf("content entry carries a value projection: %v/%v", content.Before, content.After)
	}
	encoded, _ := json.Marshal(nd)
	for _, leak := range []string{"before-bytes", "after-bytes"} {
		if strings.Contains(string(encoded), leak) {
			t.Errorf("serialized node diff leaks managed content %q: %s", leak, encoded)
		}
	}
}

// An evidence-verified non-difference in a content-bearing parameter is
// not reported at all: identical bytes reached through a changed
// `source` reference are noise, not a change.
func TestDiff_FileContentUnchangedIsNotReported(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/etc/app.conf", map[string]model.Value{
			"content": "same-bytes",
			"source":  "puppet:///modules/app/old.conf",
		}),
	}, nil)
	after := catalog([]model.Resource{
		resource("File", "/etc/app.conf", map[string]model.Value{
			"content": "same-bytes",
			"source":  "puppet:///modules/app/new.conf",
		}),
	}, nil)

	nd, diags := run(t, target(nil, nil), before, after, nil)
	if nd.HasDifference {
		t.Errorf("verified-identical content reported as a difference: %+v", nd.ResourceChanges)
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
}

func TestDiff_FileContentIndeterminateIsReportedWithDiagnostic(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/etc/app.conf", map[string]model.Value{
			"source": "puppet:///modules/app/old.conf",
		}),
	}, nil)
	after := catalog([]model.Resource{
		resource("File", "/etc/app.conf", map[string]model.Value{
			"source": "puppet:///modules/app/new.conf",
		}),
	}, nil)

	retriever := stubRetriever{err: errors.New("retrieval refused")}
	nd, diags := run(t, target(nil, nil), before, after, retriever)

	if !nd.HasDifference {
		t.Fatal("an unresolved content comparison must never be clean")
	}
	content := findChange(t, nd, "File", "/etc/app.conf", "content")
	if content.FileContent == nil || content.FileContent.State != model.FileContentIndeterminate {
		t.Errorf("state = %+v, want content_indeterminate", content.FileContent)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(diags), diags)
	}
	if diags[0].Operation != model.OperationVerifyContent {
		t.Errorf("diagnostic operation = %s, want verify_content", diags[0].Operation)
	}
	if diags[0].Certname != testCertname {
		t.Errorf("diagnostic certname = %q, want %q", diags[0].Certname, testCertname)
	}
}

func TestDiff_FileContentRedactionPreservesStateAndClearsDigests(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/etc/app.conf", map[string]model.Value{"content": "before-bytes"}),
	}, nil)
	after := catalog([]model.Resource{
		resource("File", "/etc/app.conf", map[string]model.Value{"content": "after-bytes"}),
	}, nil)

	selectors := []config.RedactionSelector{{Type: "File", Parameter: "content"}}
	nd, _ := run(t, target(nil, selectors), before, after, nil)

	if !nd.HasDifference {
		t.Fatal("a redacted File content change is still a difference")
	}
	content := findChange(t, nd, "File", "/etc/app.conf", "content")
	ev := content.FileContent
	if ev == nil {
		t.Fatal("content entry carries no evidence")
	}
	if ev.State != model.FileContentChanged {
		t.Errorf("state = %s, want the classification preserved (changed)", ev.State)
	}
	if !ev.Redacted {
		t.Error("evidence not marked redacted")
	}
	if ev.Algorithm != "" {
		t.Errorf("algorithm = %q, want cleared", ev.Algorithm)
	}
	if ev.BeforeDigest != model.RedactedValue || ev.AfterDigest != model.RedactedValue {
		t.Errorf("digests = %q/%q, want both %q", ev.BeforeDigest, ev.AfterDigest, model.RedactedValue)
	}
}

// Redacting the digests must not merge two distinct File content
// changes: the fingerprint is taken over the unredacted evidence.
func TestDiff_RedactedFileContentChangesKeepDistinctFingerprints(t *testing.T) {
	makeDiff := func(afterBytes string) model.NodeDiff {
		before := catalog([]model.Resource{
			resource("File", "/etc/app.conf", map[string]model.Value{"content": "before-bytes"}),
		}, nil)
		after := catalog([]model.Resource{
			resource("File", "/etc/app.conf", map[string]model.Value{"content": afterBytes}),
		}, nil)
		selectors := []config.RedactionSelector{{Type: "File", Parameter: "content"}}
		nd, _ := run(t, target(nil, selectors), before, after, nil)
		return nd
	}

	a := findChange(t, makeDiff("after-a"), "File", "/etc/app.conf", "content")
	b := findChange(t, makeDiff("after-b"), "File", "/etc/app.conf", "content")
	same := findChange(t, makeDiff("after-a"), "File", "/etc/app.conf", "content")

	if a.FileContent.BeforeDigest != model.RedactedValue {
		t.Fatal("precondition: digests must be redacted")
	}
	if a.Fingerprint == b.Fingerprint {
		t.Error("distinct redacted File content changes share a fingerprint and would merge into one aggregate group")
	}
	if a.Fingerprint != same.Fingerprint {
		t.Error("identical File content changes must share a fingerprint")
	}
}

// A non-File resource with a `content` parameter is an ordinary
// parameter comparison; the File-content collapsing is type-scoped.
func TestDiff_NonFileContentParameterIsAnOrdinaryChange(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Concat_fragment", "app", map[string]model.Value{"content": "a"}),
	}, nil)
	after := catalog([]model.Resource{
		resource("Concat_fragment", "app", map[string]model.Value{"content": "b"}),
	}, nil)

	nd, _ := run(t, target(nil, nil), before, after, nil)
	change := findChange(t, nd, "Concat_fragment", "app", "content")
	if change.FileContent != nil {
		t.Errorf("non-File resource got File-content evidence: %+v", change.FileContent)
	}
	if change.Before != "a" || change.After != "b" {
		t.Errorf("before/after = %v/%v, want a/b", change.Before, change.After)
	}
}

// --- determinism ---

func TestDiff_IsByteIdenticalAcrossRuns(t *testing.T) {
	before := catalog([]model.Resource{
		resource("Service", "z", map[string]model.Value{"ensure": "stopped"}),
		resource("File", "/etc/b", map[string]model.Value{"content": "b1", "owner": "root"}),
		resource("File", "/etc/a", map[string]model.Value{"mode": "0644"}),
		resource("Package", "gone", nil),
	}, []model.Edge{
		{Source: "Service[z]", Target: "File[/etc/a]"},
		{Source: "File[/etc/a]", Target: "File[/etc/b]"},
	})
	after := catalog([]model.Resource{
		resource("Service", "z", map[string]model.Value{"ensure": "running"}),
		resource("File", "/etc/b", map[string]model.Value{"content": "b2", "owner": "puppet"}),
		resource("File", "/etc/a", map[string]model.Value{"mode": "0600"}),
		resource("Package", "added", nil),
	}, []model.Edge{
		{Source: "Service[z]", Target: "File[/etc/b]"},
	})

	tgt := target(
		[]config.ExclusionRule{{Type: "File", Title: "/etc/a"}},
		[]config.RedactionSelector{{Type: "Service", Parameter: "ensure"}},
	)

	var first string
	for i := 0; i < 25; i++ {
		nd, _ := run(t, tgt, before, after, nil)
		encoded, err := json.Marshal(nd)
		if err != nil {
			t.Fatalf("marshaling node diff: %v", err)
		}
		if i == 0 {
			first = string(encoded)
			continue
		}
		if string(encoded) != first {
			t.Fatalf("run %d differs:\n first: %s\n  this: %s", i, first, encoded)
		}
	}
}

func TestDiff_EmptyCatalogsProduceCleanResult(t *testing.T) {
	nd, diags := run(t, target(nil, nil), catalog(nil, nil), catalog(nil, nil), nil)
	if nd.HasDifference || len(nd.ResourceChanges) != 0 || len(nd.EdgeChanges) != 0 || len(nd.Exclusions) != 0 {
		t.Errorf("expected a clean empty diff, got %+v", nd)
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
	if nd.Certname != testCertname {
		t.Errorf("certname = %q, want %q", nd.Certname, testCertname)
	}
}

// Exclusion suppresses differences, never diagnostics. A File whose
// content resolution failed is resolved in pass 1, before pass 2 can
// know it is excluded, so the verify_content diagnostic survives even
// though the change itself does not. That deliberately keeps the run
// from being reported as clean: an unreported content-verification
// failure is exactly what a clean outcome must never hide, and an
// exclusion rule is a statement about which differences are interesting,
// not a licence to suppress a failure to look.
func TestDiff_ExclusionSuppressesDifferencesButNotDiagnostics(t *testing.T) {
	before := catalog([]model.Resource{
		resource("File", "/var/cache/x", map[string]model.Value{
			"source": "puppet:///modules/app/old.conf",
		}),
	}, nil)
	after := catalog([]model.Resource{
		resource("File", "/var/cache/x", map[string]model.Value{
			"source": "puppet:///modules/app/new.conf",
		}),
	}, nil)

	rules := []config.ExclusionRule{{Type: "File", Title: "/var/cache/*"}}
	retriever := stubRetriever{err: errors.New("retrieval refused")}
	nd, diags := run(t, target(rules, nil), before, after, retriever)

	if nd.HasDifference {
		t.Errorf("excluded difference must not set HasDifference: %+v", nd.ResourceChanges)
	}
	if len(nd.ResourceChanges) != 0 {
		t.Errorf("excluded change survived: %+v", nd.ResourceChanges)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want the content-verification failure preserved: %+v", len(diags), diags)
	}
	if diags[0].Operation != model.OperationVerifyContent || diags[0].Severity != model.SeverityError {
		t.Errorf("diagnostic = %+v, want an error-severity verify_content entry", diags[0])
	}
}
