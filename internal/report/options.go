package report

// Options is the display policy the text and HTML renderers apply. It
// carries presentation choices only — never anything that could change
// what the comparison found — so the JSON report, which is the complete
// machine-readable record (requirements.md 8.2), ignores it entirely and
// takes no Options at all.
//
// Two formats therefore show less than the document contains, by design:
// a CI log and a review page are read top to bottom by a human, and a
// per-target list of several hundred dependency-graph edges or a repeated
// PQL string per estimate buries the resource changes a reviewer is
// actually looking for. Nothing shown is ever recomputed or re-derived
// for a format (see doc.go); the renderers only choose what to print.
type Options struct {
	// ImpactNodes prints every certname an impact estimate returned
	// instead of the capped inline sample. A bounded estimate may hold up
	// to its configured result_limit certnames — a thousand by default —
	// so the full list is opt-in per estimate section, not the default.
	ImpactNodes bool
}

// inlineCertnameCap is how many certnames an impact estimate prints
// inline when Options.ImpactNodes is not set. The remainder is reported
// as a "+N more" tail, so the count is never hidden — only the names are.
const inlineCertnameCap = 5
