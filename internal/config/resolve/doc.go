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
// transports. Combining a target's impact-estimate timeout with a
// service-level request deadline is deferred for the same kind of
// reason: ServicesFile (see internal/config/services.go) does not model
// a service-level deadline, so this package resolves and validates only
// the target's own impact_estimate.timeout value. See resolve.go's
// package comment for the assumptions this package documents.
package resolve
