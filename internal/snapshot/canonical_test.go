package snapshot

import (
	"encoding/json"
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
