package snapshot

import (
	"strings"

	"encoding/json"
	"github.com/example42/piace/internal/limits"
	"testing"
)

// TestCanonicalJSON_MapKeyOrderIndependence verifies that two payloads
// differing only in map key insertion or declaration order produce
// byte-identical canonical output: map keys sort lexicographically by
// UTF-8 bytes, and the result is deterministic.
func TestCanonicalJSON_MapKeyOrderIndependence(t *testing.T) {
	a := json.RawMessage(`{"b": 1, "a": 2, "c": 3}`)
	b := json.RawMessage(`{"c": 3, "a": 2, "b": 1}`)

	got1, err := canonicalOf(t, a)
	if err != nil {
		t.Fatalf("canonicalOf(a): %v", err)
	}
	got2, err := canonicalOf(t, b)
	if err != nil {
		t.Fatalf("canonicalOf(b): %v", err)
	}
	if string(got1) != string(got2) {
		t.Errorf("canonical outputs differ by key order:\n%s\n%s", got1, got2)
	}
	want := `{"a":2,"b":1,"c":3}`
	if string(got1) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got1, want)
	}
}

// TestCanonicalJSON_LargeIntegerPreservesExactDigits verifies a large
// integer (beyond float64's 53-bit mantissa precision) round-trips
// through canonicalization with every digit intact. Parsed numeric
// tokens normalize to their exact base-10 numeric value, which is
// exactly what encoding/json's default float64 decoding would destroy.
func TestCanonicalJSON_LargeIntegerPreservesExactDigits(t *testing.T) {
	// 2^63 - 1 plus a large offset: not exactly representable as float64.
	raw := json.RawMessage(`{"id": 9223372036854775807, "big": 123456789012345678901234567890}`)
	got, err := canonicalOf(t, raw)
	if err != nil {
		t.Fatalf("canonicalOf: %v", err)
	}
	want := `{"big":123456789012345678901234567890,"id":9223372036854775807}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

// TestCanonicalJSON_NumericSpellingNormalizes verifies differently spelled
// encodings of the same numeric value normalize to identical canonical
// digits (e.g. "1.50" and "1.5", "1e2" and "100", "-0" and "0").
func TestCanonicalJSON_NumericSpellingNormalizes(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`1.50`, `1.5`},
		{`1.5`, `1.5`},
		{`1e2`, `100`},
		{`100`, `100`},
		{`-0`, `0`},
		{`0.500`, `0.5`},
		{`-1.250`, `-1.25`},
		{`2.5e-2`, `0.025`},
	}
	for _, tc := range cases {
		got, err := canonicalNumberString(tc.raw)
		if err != nil {
			t.Fatalf("canonicalNumberString(%q): %v", tc.raw, err)
		}
		if got != tc.want {
			t.Errorf("canonicalNumberString(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestCanonicalJSON_NestedArraysAndObjects verifies arrays retain order
// while nested objects still get their keys sorted.
func TestCanonicalJSON_NestedArraysAndObjects(t *testing.T) {
	raw := json.RawMessage(`{"list": [3, 1, {"z": 1, "y": 2}], "z": "hi"}`)
	got, err := canonicalOf(t, raw)
	if err != nil {
		t.Fatalf("canonicalOf: %v", err)
	}
	want := `{"list":[3,1,{"y":2,"z":1}],"z":"hi"}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

// TestCanonicalJSON_StringEscaping verifies strings requiring escaping
// (quotes, backslashes, control characters, unicode) encode using standard
// JSON escaping.
func TestCanonicalJSON_StringEscaping(t *testing.T) {
	raw := json.RawMessage(`{"s": "line1\nline2\t\"quoted\"\\backslash"}`)
	got, err := canonicalOf(t, raw)
	if err != nil {
		t.Fatalf("canonicalOf: %v", err)
	}
	var roundTrip map[string]string
	if err := json.Unmarshal(got, &roundTrip); err != nil {
		t.Fatalf("canonical output is not valid JSON: %v (%s)", err, got)
	}
	if roundTrip["s"] != "line1\nline2\t\"quoted\"\\backslash" {
		t.Errorf("round-tripped string = %q", roundTrip["s"])
	}
}

// canonicalOf decodes raw with number precision preserved and returns its
// canonical encoding, mirroring what Checksum does internally.
func canonicalOf(t *testing.T, raw json.RawMessage) ([]byte, error) {
	t.Helper()
	v, err := decodeAny(raw)
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(v)
}

// TestCanonicalNumberString_BoundsExpansion is the finding this budget
// exists for: `1e-10000` is eight bytes of input that used to produce
// 10,002 bytes of exact decimal output, and the cost is paid during
// parsing, before anything downstream can decline it.
func TestCanonicalNumberString_BoundsExpansion(t *testing.T) {
	for _, token := range []string{"1e-10000", "1e10000", "1E+10000", "1e-99999999999999999999"} {
		if _, err := CanonicalNumberString(token); err == nil {
			t.Errorf("CanonicalNumberString(%q) = no error, want the exponent refused", token)
		}
	}

	long := strings.Repeat("9", limits.NumberDigits+1)
	if _, err := CanonicalNumberString(long); err == nil {
		t.Error("accepted a mantissa with more significant digits than the limit")
	}
}

// TestCanonicalNumberString_KeepsTheValuesPIACECompares: the budget must
// not cost exactness on anything a catalog actually contains, including
// the large integers and long decimals the round-trip tests rely on.
func TestCanonicalNumberString_KeepsTheValuesPIACECompares(t *testing.T) {
	cases := map[string]string{
		"0":                     "0",
		"-0":                    "0",
		"1.50":                  "1.5",
		"9007199254740993":      "9007199254740993",
		"0.1234567890123456789": "0.1234567890123456789",
		"1e3":                   "1000",
		"1e-3":                  "0.001",
		"1e1024":                "1" + strings.Repeat("0", 1024),
		"1e-1024":               "0." + strings.Repeat("0", 1023) + "1",
	}
	for token, want := range cases {
		got, err := CanonicalNumberString(token)
		if err != nil {
			t.Errorf("CanonicalNumberString(%q): %v", token, err)
			continue
		}
		if got != want {
			t.Errorf("CanonicalNumberString(%q) = %q, want %q", token, got, want)
		}
	}
}

// TestCanonicalJSON_BoundsNesting: recursion over a document shaped by
// whoever wrote it is otherwise bounded only by the stack.
func TestCanonicalJSON_BoundsNesting(t *testing.T) {
	var deep any = "leaf"
	for i := 0; i < limits.JSONNestingDepth+2; i++ {
		deep = []any{deep}
	}
	if _, err := CanonicalJSON(deep); err == nil {
		t.Fatal("accepted a value nested past the limit")
	}

	var shallow any = "leaf"
	for i := 0; i < 50; i++ {
		shallow = map[string]any{"child": shallow}
	}
	if _, err := CanonicalJSON(shallow); err != nil {
		t.Errorf("rejected ordinary nesting: %v", err)
	}
}

// BenchmarkCanonicalNumberString covers the shapes that motivated the
// budget alongside an ordinary value, so a regression in either
// direction is visible.
func BenchmarkCanonicalNumberString(b *testing.B) {
	for name, token := range map[string]string{
		"ordinary":         "8140",
		"long decimal":     "0.1234567890123456789",
		"largest allowed":  "1e1024",
		"smallest allowed": "1e-1024",
	} {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := CanonicalNumberString(token); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
