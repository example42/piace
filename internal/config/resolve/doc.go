// Package resolve decodes, validates, and resolves PIACE's `--targets` and
// `--services` YAML configuration into a complete per-target model, before
// any network I/O (compiler or PuppetDB calls) is attempted.
//
// It lives in a subpackage of internal/config rather than in package config
// itself because it builds internal/model.ConfigProvenance, and
// internal/model already imports internal/config; putting this logic in
// package config would create an import cycle (config -> model -> config).
//
// Design reference: design.md section 3 ("Target configuration resolution")
// and section 2.2 ("Service configuration"). Requirements: 3.3-3.5,
// 4.1-4.4, 6.1-6.2, 8.8, 9.1/9.5, 10.3.
//
// Scope boundary: this package parses and validates configuration shape and
// cross-field policy only. It never dials the compiler or PuppetDB, and it
// never checks that a TLS file (CA bundle, client certificate, private key)
// exists or is readable — that is task 3's concern (building the mTLS
// transports). Likewise, combining a target's impact-estimate timeout with
// a service-level request deadline is deferred: ServicesFile (see
// internal/config/services.go) does not yet model a service-level deadline,
// so this package resolves and validates only the target's own
// impact_estimate.timeout value. See resolve.go's package comment for the
// documented assumptions made where design.md and requirements.md leave a
// gap.
package resolve
