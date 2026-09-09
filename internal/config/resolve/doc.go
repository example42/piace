// Package resolve decodes, validates, and resolves PIACE's `--targets` and
// `--services` YAML configuration into a complete per-target model, before
// any network I/O (compiler or PuppetDB calls) is attempted.
//
// It lives in a subpackage of internal/config rather than in package config
// itself because it builds internal/model.ConfigProvenance, and
// internal/model already imports internal/config; putting this logic in
// package config would create an import cycle (config -> model -> config).
//
// Scope boundary: this package parses and validates configuration shape
// and cross-field policy only. It never dials the compiler or PuppetDB,
// and it never checks that a TLS file (CA bundle, client certificate,
// private key) exists or is readable, which belongs to building the mTLS
// transports. It resolves and validates both deadline values a
// configuration can name, a target's impact_estimate.timeout and a
// service's timeout, but does not combine them: their precedence belongs
// to internal/transport, which applies them. See resolve.go's package
// comment for the assumptions this package documents.
//
// # One configuration, several commands
//
// Resolution is parameterized by the Command that is going to read the
// result (see requirements.go). Commands do not read the same fields, so
// each one's *presence* requirements differ: `capture facts` needs a
// certname and a fact destination, and used to be refused for want of a
// candidate environment it never looks at. Validity is not scoped: every
// value a file does contain is checked identically for every command, so
// this package's central promise survives intact, with one clause added.
// Nothing downstream re-checks a value; a field the running command does
// not need may be absent, and is then zero.
//
// The same parameter decides which service endpoints must be configured,
// which depends on the targets as well as the command: a comparison
// contacts PuppetDB only if some target's facts or baseline come from it
// or an impact estimate is enabled. Config.Required records the answer
// so a caller builds exactly those clients.
//
// ValidateComparisonTargets is a separate command policy: compare rejects a
// PuppetDB baseline whenever direct v3 or fallback can execute. Capture may use
// those configurations and reports the API's actual effects.
package resolve
