// Package report renders the shared result document (model.Result) into
// the three output formats internal/report owns: concise CI text, the versioned
// canonical JSON report, and a single self-contained `file://` HTML
// artifact.
//
// # One projection, three formats
//
// HTML, text, and JSON derive from the same redacted projection, which
// prevents format drift and secret exposure. That projection is
// model.Result itself. Redaction has already happened upstream, inside
// internal/diff's pass 3, strictly before serialization, and the
// unredacted evidence never leaves that package:
// model.ResourceChange.Fingerprint is `json:"-"` and the raw canonical
// values are gone by the time a Result exists. No renderer in this
// package can therefore disclose a sensitive value, because none of them
// has access to one. Nothing here re-derives, re-orders, or re-computes
// anything: the three functions differ only in encoding.
//
// The shared formatting helpers in render.go exist for the same reason.
// A value rendered one way in text and another way in HTML is format
// drift even when both are safe, so both formats call formatValue, and
// both label the same sections with the same constants.
//
// # Three formats, three amounts of detail
//
// The three formats show the same document at three levels of detail.
// This is display policy, not a second projection: nothing here filters
// or recomputes what a comparison found, and every format decides through
// the same shared helpers (formatValue, changeSummary/changeParts,
// estimateCount, targetCountList), so they cannot drift apart in what a
// value says.
//
//   - JSON is the complete record and takes no options at all.
//   - HTML is complete too, and uses disclosure rather than omission:
//     resource changes, edge changes, aggregate groups, an estimate's PQL,
//     request options and full certname list are all on the page, inside
//     closed <details>. Every list of rows is closed and every summary
//     carries the count of what it holds, so the page a reader lands on is
//     an index of the run: the outcome, the reasons, the tally, and one
//     line per target with a counted chip per section, and one click
//     reaches any of it. Nothing is capped, because a closed disclosure
//     already keeps a thousand certnames out of the reading path without
//     dropping a name. What stays outside every disclosure is everything
//     that has to be visibly marked (see below), plus the estimate label
//     and note.
//   - Text is the only format that omits, because a CI log is a linear
//     read with no way to skip a section and no way to expand one. It
//     drops edge changes (a run's edge differences routinely outnumber
//     its resource differences, being a consequence of them), an
//     estimate's PQL and request options (identical in shape on every
//     line of a section that can run to hundreds of entries), and an
//     estimate's certnames past Options.inlineCertnameCap unless
//     Options.ImpactNodes is set. The count is never elided, only names.
//
// # The change assessment is a fourth thing, and it is not part of that
//
// HTML and Text take a second input `piace explain` supplies and `piace
// compare` never does: an advisory change assessment (internal/assess).
// It is a parameter rather than a field of a model.Result on purpose,
// keeping it out of the result document, and a nil one renders nothing at all, not an empty
// section and not a stray newline, so a report rendered without one is
// byte-identical to what v0.1.0 produced. That identity is asserted
// against a checked-in golden captured before the parameter existed.
//
// Where the deterministic sections above it are a record, the assessment
// is an opinion, so the page marks it as one: AssessmentNote states in
// both formats that it is advisory, model-generated and not
// deterministic, and the section renders below every deterministic
// section, never above one. What stays outside the section's disclosure
// is the run risk indication and everything that qualifies it, meaning
// the model id, a truncated selection, a partial input, and the
// assessment's own diagnostics, for the same reason the outcome badges
// do: an assessment reading "unknown" with its reason folded away is a
// page that looks broken rather than one that failed.
//
// Text carries the run risk indication and review focus and stops there.
// Per-group rationale is model prose, one paragraph per group, and a CI
// log has no way to skip a section; it is in HTML and in the JSON
// artifact, both of which a reader can navigate. Nothing in an
// assessment is ever wrapped in template.HTML: it is text a remote
// service wrote, and it is untrusted in exactly the way a resource title
// from a catalog is.
//
// So edges identified and retained through aggregation as a distinct
// kind, suppressed-difference counts, complete node diffs, and the exact
// generated PQL query are discharged by the JSON report and, for
// everything but the JSON envelope itself, visibly by the HTML report as
// well. The HTML artifact also embeds the canonical JSON in its closing
// disclosure, so the page is a complete record twice over.
//
// One consequence has to be handled explicitly rather than by omission.
// model.NodeDiff.HasDifference is true for a target whose only
// differences are edges, and that target still drives the run's outcome
// and exit code. HTML renders those edges, so nothing is needed there;
// the text report prints a note instead of an empty change list, because
// a report that showed nothing would read as "no changes" on a run that
// exits non-zero, contradicting its own stated outcome and brushing
// 10.5. For the same reason every section header in both formats counts
// what it actually displays rather than what the document holds.
//
// # A light page, and nothing to fetch
//
// "No HTTP server, a CDN, network access, or sibling assets" is stronger
// than it first reads: it also rules out a webfont and an image file.
// The HTML report is therefore built from system font stacks with
// declared fallbacks, and its only piece of iconography, the disclosure
// triangle, is drawn with CSS borders rather than set in a glyph a
// reader's machine may not have. The page commits to a single light
// palette rather than following the reader's system theme: a review
// artifact gets shared, printed, and pasted into tickets, and one
// appearance is one thing to check.
//
// # The impact-estimate label
//
// The result has to be labelled a **potential impact estimate**, and
// must never state that selected nodes will change. internal/impact
// deliberately emits no such wording, since model.ImpactEstimate carries
// state rather than prose, so the obligation is discharged here, in
// every format, via ImpactEstimateLabel and ImpactEstimateNote. Both are
// package constants rather than per-format literals so the three formats
// cannot drift into saying different things, and neither ever describes
// a returned certname as a node that will change: the estimate says only
// that a node's latest stored catalog contains the resource.
//
// The compact per-estimate line depends on that section header for its
// meaning. "Class[Foo]: 9 nodes: ..." is not a claim about those nodes on
// its own, because ImpactEstimateNote stands immediately above it and
// says, once for the whole section, what a listed certname does and does
// not mean. Any format that ever prints an estimate line without that
// header would be stating exactly what an estimate must never claim.
//
// # HTML safety
//
// The artifact has to open over `file://` without an HTTP server, a CDN,
// network access, or sibling assets, and external HTML assets and
// user-controlled template execution are both excluded. The template
// here is a package constant with inlined CSS and no JavaScript at all:
// expand and collapse use <details>, which needs none, so there is no
// script context in the document and no script-context escaping to get
// wrong.
//
// Every value the page shows, resource titles, parameter values,
// diagnostic messages and PQL text alike, originates in Puppet code or a
// service response and is untrusted. All of it is interpolated through
// html/template, whose contextual escaping is the mechanism that makes a
// title containing `</script>` or `<img onerror=...>` inert. No renderer
// here concatenates HTML by hand.
package report

// ImpactEstimateLabel is the exact visible label the impact-estimate
// section carries in every output format.
const ImpactEstimateLabel = "potential impact estimate"

// ImpactEstimateNote is the fixed explanatory sentence shown beside
// ImpactEstimateLabel in every format. It states what the estimate does
// and does not claim and uses CONTEXT.md's terminology (never "affected
// nodes" or "blast radius").
const ImpactEstimateNote = "Reports only that a node's latest stored catalog contains this exact resource type and title. It does not state that the node will change, and PIACE does not compile these nodes."

// AssessmentLabel is the visible heading of the change-assessment
// section. The section is named for what it is, an assessment of a
// change, and never for the outcome of the comparison, which the
// deterministic sections above it already state.
const AssessmentLabel = "Change assessment"

// AssessmentNote is the fixed sentence shown beside AssessmentLabel in
// every format that carries an assessment. Like ImpactEstimateNote it is
// a package constant rather than per-format prose, so no format can
// quietly describe a change assessment as something firmer than it is.
//
// It states the three things a reader has to know before reading a word
// of what a model said: that this is advisory, that it is generated, and
// that running the same command again may say something different. What
// it is not is a disclaimer for the section's benefit. A risk indication
// is an opinion about a change, and a page that presents it beside a
// deterministic outcome without saying which is which is misleading
// whatever the model got right.
//
// The last sentence is the one omission a reader could otherwise mistake
// for a judgement: the assessment covers resource-change groups only, and
// a run's dependency-graph edge groups (a consequence of those changes,
// with no value pair to reason about) are never sent. The deterministic
// sections above list every one of them.
const AssessmentNote = "Advisory and model-generated: not deterministic, not part of the result document, and never able to affect the outcome or exit code above. Two runs over the same report may say different things. It covers resource-change groups only: dependency-graph edge changes are left to the deterministic sections above."
