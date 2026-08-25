// Package report renders the shared result document (model.Result) into
// the three output formats task 11 owns: concise CI text, the versioned
// canonical JSON report, and a single self-contained `file://` HTML
// artifact.
//
// # One projection, three formats
//
// design.md section 9: "HTML, text, and JSON derive from the same
// redacted projection, preventing format drift or secret exposure." That
// projection is model.Result itself. Redaction has already happened
// upstream, inside internal/diff's pass 3, strictly before serialization
// (design.md section 7.3), and the unredacted evidence never leaves that
// package — model.ResourceChange.Fingerprint is `json:"-"` and the raw
// canonical values are gone by the time a Result exists. No renderer in
// this package can therefore disclose a sensitive value, because none of
// them has access to one. Nothing here re-derives, re-orders, or
// re-computes anything: the three functions differ only in encoding.
//
// The shared formatting helpers in render.go exist for the same reason.
// A value rendered one way in text and another way in HTML is format
// drift even when both are safe, so both formats call formatValue, and
// both label the same sections with the same constants.
//
// # Determinism
//
// design.md Property 1 requires byte-identical output for identical
// inputs. model.Result carries three map fields (ConfigProvenance's
// Candidate/Facts/Baseline/ImpactEstimate projections). Go's
// encoding/json sorts map keys, so the JSON path is safe automatically —
// but a `range` over a map in a text or HTML renderer is not, so every
// map here is iterated through sortedKeys. Everything else in a Result is
// already ordered by the package that produced it (targets by certname,
// aggregate groups by kind and identity, estimates by identity, certname
// samples locally sorted).
//
// # requirement 9.3's label
//
// requirements.md 9.3 requires the CLI to "label the result **potential
// impact estimate**, and SHALL NOT state that selected nodes will
// change". internal/impact deliberately emits no such wording —
// model.ImpactEstimate carries state, not prose — so the obligation is
// discharged here, in every format, via ImpactEstimateLabel and
// ImpactEstimateNote. Both are package constants rather than per-format
// literals so the three formats cannot drift into saying different
// things, and neither ever describes a returned certname as a node that
// will change: the estimate says only that a node's latest stored catalog
// contains the resource.
//
// # HTML safety
//
// requirements.md 8.3 requires an artifact that opens over `file://`
// "without an HTTP server, a CDN, network access, or sibling assets", and
// design.md section 11 excludes "external HTML assets" and
// "user-controlled template execution". The template here is a package
// constant with inlined CSS and no JavaScript at all: expand/collapse
// uses <details>, which needs none, so there is no script context in the
// document and no script-context escaping to get wrong.
//
// Every value the page shows — resource titles, parameter values,
// diagnostic messages, PQL text — originates in Puppet code or a service
// response and is untrusted. All of it is interpolated through
// html/template, whose contextual escaping is the mechanism that makes a
// title containing `</script>` or `<img onerror=...>` inert. No renderer
// here concatenates HTML by hand.
package report

// ImpactEstimateLabel is the exact visible label requirements.md 9.3
// requires on the impact-estimate section of every output format.
const ImpactEstimateLabel = "potential impact estimate"

// ImpactEstimateNote is the fixed explanatory sentence shown beside
// ImpactEstimateLabel in every format. It states what the estimate does
// and does not claim, per requirements.md 9.3/9.8 and design.md section
// 8, and uses CONTEXT.md's terminology (never "affected nodes" or "blast
// radius").
const ImpactEstimateNote = "Reports only that a node's latest stored catalog contains this exact resource type and title. It does not state that the node will change, and PIACE does not compile these nodes."
