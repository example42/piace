// Package diff compares normalized catalogs in three passes: raw resource and
// edge comparison, exclusions, then publication through a redacted projection.
// HasDifference is fixed after exclusions and before publication. Exclusions
// suppress changes, including connected edges, but retain content diagnostics.
//
// Raw changes use a private type that refuses JSON serialization and formats
// without values. publishChanges explicitly constructs model.ResourceChange;
// renderers, aggregation and inference receive only that published type.
//
// Sensitivity comes from resource-level sensitive_parameters, recursive Pcore
// {"__ptype":"Sensitive","__pvalue":...} wrappers, or exact configured selectors.
// Resource metadata and selectors protect the entire parameter on both sides.
// A nested wrapper protects the corresponding subtree on both sides, including
// when introduced or removed. Incompatible container shapes cause the whole
// parameter to be masked. Normalization rejects malformed sensitivity metadata
// and Sensitive wrappers without __pvalue before comparison.
//
// Source-backed content is evaluated even when references are unchanged. Each
// catalog supplies its own explicit content context; historical sides cannot
// use live retrieval. Exclusions do not cancel requested evidence resolution.
// File content, source, checksum and checksum_value changes collapse into one
// content evidence entry. Wrapped inline values are unwrapped only for evidence
// resolution. Sensitivity or selectors on any content-bearing parameter suppress
// both digests and the algorithm, preserving the content state. File bytes never
// enter the published change. Added and removed resources currently carry only
// identity; parameter evidence for them is scheduled for phase 3 of 0.5.0.
//
// Fingerprints cover raw canonical evidence before publication, including File
// content parameters and resolved evidence. Distinct sensitive changes therefore
// remain distinct even when evidence is indeterminate. Fingerprints are internal
// grouping tokens (json:"-") and never enter serialized reports or inference.
//
// These wire assumptions derive from Puppet::Resource#to_data_hash and Puppet's
// Pcore serialization sources. Tests use synthetic fixtures in compiler-array
// and PuppetDB expanded-container shapes. They do not establish that every
// deployed PuppetDB version retains resource sensitivity metadata.
package diff
