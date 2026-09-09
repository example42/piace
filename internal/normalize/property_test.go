package normalize

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	"github.com/example42/piace/internal/model"
	"github.com/example42/piace/internal/puppetdb"
)

// randomToken generates a short random-ish but deterministic (given rng)
// alphanumeric string, mirroring internal/compiler/property_test.go's
// randomIdentifier helper. No PBT library is vendored in this module (see
// go.mod), so every property test in this package is hand-rolled with
// math/rand over a fixed seed.
func randomToken(rng *rand.Rand, prefix string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-/."
	n := 1 + rng.Intn(20)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return prefix + string(b)
}

// randomResourceIdentities generates n distinct random (type, title)
// pairs.
func randomResourceIdentities(rng *rand.Rand, n int) []model.ResourceIdentity {
	seen := make(map[model.ResourceIdentity]bool, n)
	out := make([]model.ResourceIdentity, 0, n)
	for len(out) < n {
		id := model.ResourceIdentity{Type: randomToken(rng, "Type"), Title: randomToken(rng, "title-")}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// buildCompilerResourceJSON builds one compiler-plain-array resource
// entry JSON fragment for identity, including a random scalar parameter
// so parameter canonicalization is also exercised.
func buildCompilerResourceJSON(id model.ResourceIdentity, paramValue string) string {
	tb, _ := json.Marshal(id.Type)
	lb, _ := json.Marshal(id.Title)
	return fmt.Sprintf(`{"type":%s,"title":%s,"aliases":[],"exported":false,"file":"/x.pp","line":1,"tags":["t"],"parameters":{"v":%s}}`,
		tb, lb, paramValue)
}

// TestProperty_ResourceIdentityRoundTrips is a property-based test: for
// N randomly generated resource identities fed through the compiler's
// plain-array shape, Catalog must return exactly those identities,
// case-sensitively, with no loss or corruption, sorted by (Type, Title).
// This locks the identity-construction contract across many random
// inputs: the same input always normalizes to the same sorted identity
// set.
func TestProperty_ResourceIdentityRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 100; iter++ {
		n := 1 + rng.Intn(8)
		ids := randomResourceIdentities(rng, n)

		resourceEntries := make([]string, len(ids))
		for i, id := range ids {
			resourceEntries[i] = buildCompilerResourceJSON(id, "1")
		}
		resourcesJSON := "[" + joinStrings(resourceEntries, ",") + "]"

		raw := compilerShapedCatalog("node.example.test", "production", resourcesJSON, `[]`)
		got, diag := Catalog(raw)
		if diag != nil {
			t.Fatalf("iteration %d: unexpected diagnostic: %+v (resources=%s)", iter, diag, resourcesJSON)
		}
		if len(got.Resources) != len(ids) {
			t.Fatalf("iteration %d: got %d resources, want %d", iter, len(got.Resources), len(ids))
		}

		gotIdentities := make(map[model.ResourceIdentity]bool, len(got.Resources))
		for _, r := range got.Resources {
			gotIdentities[r.Identity] = true
		}
		for _, id := range ids {
			if !gotIdentities[id] {
				t.Errorf("iteration %d: identity %s missing from normalized output", iter, id.String())
			}
		}

		// Verify sort order: every consecutive pair satisfies
		// resourceLess (or equal, which cannot happen since identities
		// are distinct by construction).
		for i := 1; i < len(got.Resources); i++ {
			if !resourceLess(got.Resources[i-1].Identity, got.Resources[i].Identity) {
				t.Errorf("iteration %d: Resources not sorted at index %d: %s then %s",
					iter, i, got.Resources[i-1].Identity.String(), got.Resources[i].Identity.String())
			}
		}
	}
}

// TestProperty_EdgeSortKeyIsOrderedPair is a property-based test: for N
// randomly generated edges (each a random pair of resource identities,
// direction significant), Catalog's returned edge list is always sorted
// by the ordered pair (Source, Target) and preserves every edge exactly
// once: a graph edge key is the ordered pair (source identity, target
// identity), and direction is significant.
func TestProperty_EdgeSortKeyIsOrderedPair(t *testing.T) {
	rng := rand.New(rand.NewSource(43))

	for iter := 0; iter < 100; iter++ {
		poolSize := 2 + rng.Intn(6)
		pool := randomResourceIdentities(rng, poolSize)

		// maxPairs bounds how many distinct ordered (source, target) pairs the
		// pool can produce, self-loops included, so edgeCount below never
		// requests more unique pairs than exist. Otherwise the seenPairs retry
		// loop below would spin forever once every possible pair had already
		// been generated.
		maxPairs := poolSize * poolSize
		edgeCount := 1 + rng.Intn(8)
		if edgeCount > maxPairs {
			edgeCount = maxPairs
		}
		type pair struct{ s, tt model.ResourceIdentity }
		seenPairs := make(map[pair]bool)
		var pairs []pair
		for len(pairs) < edgeCount {
			s := pool[rng.Intn(len(pool))]
			tt := pool[rng.Intn(len(pool))]
			p := pair{s, tt}
			if seenPairs[p] {
				continue // avoid ambiguity from duplicate edges in this property
			}
			seenPairs[p] = true
			pairs = append(pairs, p)
		}

		// Build a resources list covering every identity in pool (edges
		// need not reference declared resources per the wire formats, but
		// build them anyway to keep the fixture realistic).
		resourceEntries := make([]string, len(pool))
		for i, id := range pool {
			resourceEntries[i] = buildCompilerResourceJSON(id, "1")
		}
		resourcesJSON := "[" + joinStrings(resourceEntries, ",") + "]"

		edgeEntries := make([]string, len(pairs))
		for i, p := range pairs {
			st, _ := json.Marshal(p.s.Type)
			stt, _ := json.Marshal(p.s.Title)
			tt2, _ := json.Marshal(p.tt.Type)
			ttt, _ := json.Marshal(p.tt.Title)
			edgeEntries[i] = fmt.Sprintf(`{"source":{"type":%s,"title":%s},"target":{"type":%s,"title":%s},"relationship":"contains"}`,
				st, stt, tt2, ttt)
		}
		edgesJSON := "[" + joinStrings(edgeEntries, ",") + "]"

		raw := compilerShapedCatalog("node.example.test", "production", resourcesJSON, edgesJSON)
		got, diag := Catalog(raw)
		if diag != nil {
			t.Fatalf("iteration %d: unexpected diagnostic: %+v", iter, diag)
		}
		if len(got.Edges) != len(pairs) {
			t.Fatalf("iteration %d: got %d edges, want %d", iter, len(got.Edges), len(pairs))
		}
		for i := 1; i < len(got.Edges); i++ {
			if !edgeLess(got.Edges[i-1], got.Edges[i]) {
				t.Errorf("iteration %d: Edges not sorted at index %d: %+v then %+v",
					iter, i, got.Edges[i-1], got.Edges[i])
			}
		}
		wantSet := make(map[model.Edge]bool, len(pairs))
		for _, p := range pairs {
			wantSet[model.Edge{Source: p.s.String(), Target: p.tt.String()}] = true
		}
		for _, e := range got.Edges {
			if !wantSet[e] {
				t.Errorf("iteration %d: unexpected edge %+v in normalized output", iter, e)
			}
		}
	}
}

// TestProperty_NumberCanonicalizationMatchesSnapshotAlgorithm is a
// property-based test: for N randomly generated differently-spelled
// encodings of the same numeric value (integer, decimal, exponent
// forms), the resulting model.Number is always identical regardless of
// spelling, reusing the one canonicalization algorithm already
// implemented for snapshot payload checksums.
func TestProperty_NumberCanonicalizationMatchesSnapshotAlgorithm(t *testing.T) {
	rng := rand.New(rand.NewSource(44))

	for iter := 0; iter < 100; iter++ {
		intPart := rng.Intn(100000)
		fracDigits := rng.Intn(4)
		var fracPart int
		if fracDigits > 0 {
			fracPart = rng.Intn(pow10(fracDigits))
		}

		// Spelling A: plain decimal, e.g. "123.045" (zero-padded frac).
		var plain string
		if fracDigits == 0 {
			plain = fmt.Sprintf("%d", intPart)
		} else {
			plain = fmt.Sprintf("%d.%0*d", intPart, fracDigits, fracPart)
		}
		// Spelling B: same value with a trailing zero appended to the
		// fractional part (or ".0" appended to an integer), which must
		// canonicalize identically.
		var padded string
		if fracDigits == 0 {
			padded = fmt.Sprintf("%d.0", intPart)
		} else {
			padded = fmt.Sprintf("%d.%0*d0", intPart, fracDigits, fracPart)
		}

		resourcesJSON := "[" +
			buildCompilerResourceJSON(model.ResourceIdentity{Type: "File", Title: "a"}, plain) +
			"," +
			buildCompilerResourceJSON(model.ResourceIdentity{Type: "File", Title: "b"}, padded) +
			"]"

		raw := compilerShapedCatalog("node.example.test", "production", resourcesJSON, `[]`)
		got, diag := Catalog(raw)
		if diag != nil {
			t.Fatalf("iteration %d: unexpected diagnostic: %+v (plain=%q padded=%q)", iter, diag, plain, padded)
		}

		var va, vb model.Value
		for _, r := range got.Resources {
			switch r.Identity.Title {
			case "a":
				va = r.Parameters["v"]
			case "b":
				vb = r.Parameters["v"]
			}
		}
		if va != vb {
			t.Errorf("iteration %d: canonicalized %q -> %#v, %q -> %#v; want identical", iter, plain, va, padded, vb)
		}
	}
}

// TestProperty_MalformedShapesAlwaysProduceDiagnostic is a property-based
// test: for N randomly generated malformed "resources"/"edges" field
// values (never a recognized {href, data} object or plain array),
// Catalog always returns a non-nil model.OperationNormalize diagnostic
// and a zero NormalizedCatalog, never a partially-populated or silently
// empty result: an unknown required shape is a reported normalization
// error, never silently discarded.
func TestProperty_MalformedShapesAlwaysProduceDiagnostic(t *testing.T) {
	rng := rand.New(rand.NewSource(45))

	malformedGenerators := []func(*rand.Rand) string{
		func(r *rand.Rand) string { return `null` },
		func(r *rand.Rand) string { return `true` },
		func(r *rand.Rand) string { return `false` },
		func(r *rand.Rand) string { return fmt.Sprintf("%d", r.Intn(1000)) },
		func(r *rand.Rand) string { return `"` + randomToken(r, "s") + `"` },
		func(r *rand.Rand) string { return `{}` },                      // object missing "data"
		func(r *rand.Rand) string { return `{"href":"/x"}` },           // object missing "data"
		func(r *rand.Rand) string { return `{"data":"not-an-array"}` }, // data present but wrong type
	}

	for iter := 0; iter < 100; iter++ {
		gen := malformedGenerators[rng.Intn(len(malformedGenerators))]
		badValue := gen(rng)

		targetField := rng.Intn(2) // 0 = resources malformed, 1 = edges malformed
		var raw puppetdb.Catalog
		if targetField == 0 {
			raw = puppetdb.Catalog{
				Certname:  "node.example.test",
				Resources: json.RawMessage(badValue),
				Edges:     json.RawMessage(`[]`),
			}
		} else {
			raw = puppetdb.Catalog{
				Certname:  "node.example.test",
				Resources: json.RawMessage(`[]`),
				Edges:     json.RawMessage(badValue),
			}
		}

		got, diag := Catalog(raw)
		if diag == nil {
			t.Fatalf("iteration %d: badValue=%q targetField=%d: expected a diagnostic, got none (result=%+v)",
				iter, badValue, targetField, got)
		}
		if diag.Operation != model.OperationNormalize {
			t.Errorf("iteration %d: Operation = %q, want %q", iter, diag.Operation, model.OperationNormalize)
		}
		if diag.Severity != model.SeverityError {
			t.Errorf("iteration %d: Severity = %q, want %q", iter, diag.Severity, model.SeverityError)
		}
		if len(got.Resources) != 0 || len(got.Edges) != 0 {
			t.Errorf("iteration %d: expected a zero NormalizedCatalog, got %+v", iter, got)
		}
	}
}

func pow10(n int) int {
	p := 1
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}

func joinStrings(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}
