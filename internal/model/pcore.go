package model

import "strings"

// A catalog PIACE reads is in one of two fidelities, and which one it is
// decides how two of them can be compared.
//
// A compiler's catalog response carries Pcore rich data: a typed value
// appears as `{"__ptype": <type name>, "__pvalue": <value>}`. A catalog
// read back from PuppetDB does not. Puppet's PuppetDB terminus wraps its
// call to `Puppet::Resource::Catalog#to_data_hash` in
// `Puppet.override({:stringify_rich => true})`, which routes every
// parameter value through `Puppet::Pops::Serialization::
// ToStringifiedConverter` on the way to storage. That class documents
// itself as lossy: "The conversion is lossy - the result cannot be
// deserialized to produce the original data types. All rich values are
// transformed to strings." `Puppet::Resource#to_data_hash` adds that its
// output "should not be used when producing a catalog serialization".
//
// So a PuppetDB baseline holds a string where a compiled candidate holds
// a typed value, and comparing the two directly reports a change in
// every rich-typed parameter. Measured against a deployed OpenVox 8.15.2
// installation on 2026-09-09, comparing one node's production
// environment with itself, `Class[Psick::Puppet]`'s
// `facts_file_exclude_regex` compared `"/^(...)$/"` against
// `{"__ptype":"Regexp","__pvalue":"^(...)$"}`.
//
// Neither side is wrong. They are two fidelities of one value, and the
// only comparison available is at the poorer of the two.

const pcoreValueKey = "__pvalue"

// RegexpWrapperType is the `__ptype` value naming a Pcore regular
// expression. It is the one rich type this codebase has measured the
// stringification of; see StringifyRich.
const RegexpWrapperType = "Regexp"

// ProjectStringifiedRich rewrites every Pcore rich value in v, at any
// depth, into the string Puppet's ToStringifiedConverter would have
// produced for it, so a rich value can be compared against the
// stringified form a PuppetDB baseline holds.
//
// The second return value is the name of the first rich type met whose
// stringification this codebase has not measured, and is empty when
// there was none. A projection carrying one is not a valid comparison
// input: reading Puppet's Ruby is not the same as measuring what a
// deployment emits, and a guessed stringification that happened to
// match would be a false clean result.
func ProjectStringifiedRich(v Value) (Value, string) {
	switch v := v.(type) {
	case map[string]Value:
		if name, ok := richTypeName(v); ok {
			s, measured := StringifyRich(name, v[pcoreValueKey])
			if !measured {
				return nil, name
			}
			return s, ""
		}
		out := make(map[string]Value, len(v))
		for k, child := range v {
			projected, unmeasured := ProjectStringifiedRich(child)
			if unmeasured != "" {
				return nil, unmeasured
			}
			out[k] = projected
		}
		return out, ""
	case []Value:
		out := make([]Value, len(v))
		for i, child := range v {
			projected, unmeasured := ProjectStringifiedRich(child)
			if unmeasured != "" {
				return nil, unmeasured
			}
			out[i] = projected
		}
		return out, ""
	default:
		return v, ""
	}
}

// ContainsRichData reports whether v is, or contains at any depth, a
// Pcore rich value. A value that does not is identical in both
// fidelities and needs no projection.
func ContainsRichData(v Value) bool {
	switch v := v.(type) {
	case map[string]Value:
		if _, ok := richTypeName(v); ok {
			return true
		}
		for _, child := range v {
			if ContainsRichData(child) {
				return true
			}
		}
	case []Value:
		for _, child := range v {
			if ContainsRichData(child) {
				return true
			}
		}
	}
	return false
}

// richTypeName returns m's Pcore type name when m is a rich value.
func richTypeName(m map[string]Value) (string, bool) {
	ptype, ok := m[pcoreTypeKey]
	if !ok {
		return "", false
	}
	name, ok := ptype.(string)
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

// StringifyRich returns the string Puppet's ToStringifiedConverter
// produces for a rich value of the named type, and whether this codebase
// has measured that type.
//
// Only Regexp is measured. Its stringification is
// `PRegexpType.regexp_to_s_with_delimiters`, and the `__pvalue` a
// compiler emits is `PRegexpType.regexp_to_s` of the same regular
// expression, so the two differ by the delimiters and by the escaping of
// any forward slash the pattern contains. Measured on the deployed
// OpenVox 8.15.2 installation on 2026-09-09 by calling both methods
// directly:
//
//	pvalue                  stringified
//	^abc$                   /^abc$/
//	a.b\d+                  /a.b\d+/
//	(empty)                 //
//	a/b                     /a\/b/
//	a\/b                    /a\/b/
//	^/etc/puppetlabs/.*$    /^\/etc\/puppetlabs\/.*$/
//	//                      /\/\//
//	a\\/b                   /a\\\/b/
//	(?i-mx:abc)             /(?i-mx:abc)/
//	(?mx-i:x)               /(?mx-i:x)/
//	(?m-ix:\A\z)            /(?m-ix:\A\z)/
//
// One measured shape does not follow the rule, and is deliberately not
// modelled: a pattern carrying no explicit flags but a non-ASCII source
// sets Ruby's fixed-encoding option, which puts a flag group in both
// forms while the two build it in different orders.
// `append_flags_group` emits enabled then disabled flags in i, m, x
// order, and `Regexp#to_s` uses m, i, x, so `(?-imx:café)` stringifies
// as `/(?-mix:café)/`. Several explicit flag combinations diverge the
// same way. When that happens the two values simply do not compare
// equal, so the difference is reported rather than assumed away, which
// is the safe direction: a reported difference can be read, and a
// guessed equality cannot be caught.
//
// Every other rich type a catalog can carry (Timestamp, Timespan,
// Binary, SemVer, SemVerRange, Version, Deferred, Default, Type, object
// types) is unmeasured and reported as such.
func StringifyRich(typeName string, value Value) (Value, bool) {
	if typeName != RegexpWrapperType {
		return nil, false
	}
	pattern, ok := value.(string)
	if !ok {
		return nil, false
	}
	return "/" + escapeRegexpSlashes(pattern) + "/", true
}

// escapeRegexpSlashes escapes every forward slash in a regular
// expression source that is not already escaped, which is what Ruby's
// Regexp#inspect and Regexp#to_s do when they add the delimiters.
//
// "Already escaped" means preceded by an odd number of backslashes: in
// `a\\/b` the backslash is itself escaped, so the slash is not, and
// Puppet stringifies that pattern as `/a\\\/b/`.
func escapeRegexpSlashes(pattern string) string {
	var b strings.Builder
	b.Grow(len(pattern))
	backslashes := 0
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '/' && backslashes%2 == 0 {
			b.WriteByte('\\')
		}
		if c == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
		b.WriteByte(c)
	}
	return b.String()
}
