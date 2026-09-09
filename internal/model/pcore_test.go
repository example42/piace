package model

import (
	"reflect"
	"testing"
)

// The table is measured, not derived. Each row was produced on a
// deployed OpenVox 8.15.2 installation on 2026-09-09 by calling
// Puppet::Pops::Types::PRegexpType.regexp_to_s (which is what a
// compiler's catalog puts in __pvalue) and
// PRegexpType.regexp_to_s_with_delimiters (which is what
// ToStringifiedConverter, and so PuppetDB, stores) on the same regular
// expression.
func TestStringifyRich_Regexp(t *testing.T) {
	for _, tc := range []struct{ pvalue, want string }{
		{`^abc$`, `/^abc$/`},
		{`a.b\d+`, `/a.b\d+/`},
		{``, `//`},
		{`a/b`, `/a\/b/`},
		{`a\/b`, `/a\/b/`},
		{`^/etc/puppetlabs/.*$`, `/^\/etc\/puppetlabs\/.*$/`},
		{`//`, `/\/\//`},
		{`a\\/b`, `/a\\\/b/`},
		{`(?i-mx:abc)`, `/(?i-mx:abc)/`},
		{`(?mx-i:x)`, `/(?mx-i:x)/`},
		{`(?m-ix:\A\z)`, `/(?m-ix:\A\z)/`},
		// The pattern that produced the finding, from
		// Class[Psick::Puppet]'s facts_file_exclude_regex.
		{`^(.*uptime.*|system_uptime)$`, `/^(.*uptime.*|system_uptime)$/`},
	} {
		got, measured := StringifyRich(RegexpWrapperType, tc.pvalue)
		if !measured {
			t.Errorf("StringifyRich(%q) reported the type as unmeasured", tc.pvalue)
			continue
		}
		if got != tc.want {
			t.Errorf("StringifyRich(%q) = %q, want %q", tc.pvalue, got, tc.want)
		}
	}
}

// Reading Puppet's Ruby is not measuring a deployment. Every type but
// Regexp is reported as unmeasured so a difference in it is reported
// rather than assumed away.
func TestStringifyRich_UnmeasuredTypes(t *testing.T) {
	for _, name := range []string{"Timestamp", "Timespan", "Binary", "SemVer", "SemVerRange", "Version", "Deferred", "Default", "Sensitive", "Type"} {
		if _, measured := StringifyRich(name, "whatever"); measured {
			t.Errorf("%s was claimed as measured", name)
		}
	}
}

func TestProjectStringifiedRich_NestedValues(t *testing.T) {
	regexp := func(pattern string) Value {
		return map[string]Value{"__ptype": RegexpWrapperType, "__pvalue": pattern}
	}
	in := map[string]Value{
		"plain": "left alone",
		"list":  []Value{regexp("^a$"), "b"},
		"nested": map[string]Value{
			"deep": regexp("c/d"),
		},
	}
	want := map[string]Value{
		"plain": "left alone",
		"list":  []Value{`/^a$/`, "b"},
		"nested": map[string]Value{
			"deep": `/c\/d/`,
		},
	}
	got, unmeasured := ProjectStringifiedRich(in)
	if unmeasured != "" {
		t.Fatalf("unmeasured type %q in a Regexp-only value", unmeasured)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProjectStringifiedRich = %+v, want %+v", got, want)
	}
}

func TestProjectStringifiedRich_NamesTheUnmeasuredType(t *testing.T) {
	in := []Value{map[string]Value{"__ptype": "Timestamp", "__pvalue": "2026-09-09T00:00:00.000000000 UTC"}}
	if _, unmeasured := ProjectStringifiedRich(in); unmeasured != "Timestamp" {
		t.Errorf("unmeasured = %q, want %q", unmeasured, "Timestamp")
	}
}

func TestContainsRichData(t *testing.T) {
	regexp := map[string]Value{"__ptype": RegexpWrapperType, "__pvalue": "^a$"}
	for _, tc := range []struct {
		value Value
		want  bool
	}{
		{"plain", false},
		{map[string]Value{"a": "b"}, false},
		{[]Value{"a", "b"}, false},
		{regexp, true},
		{map[string]Value{"a": []Value{regexp}}, true},
		// A hash whose __ptype is not a string is not a rich value.
		{map[string]Value{"__ptype": Number("1")}, false},
	} {
		if got := ContainsRichData(tc.value); got != tc.want {
			t.Errorf("ContainsRichData(%+v) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
