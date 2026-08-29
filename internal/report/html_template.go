package report

// htmlSource is the complete HTML document template. It is a package
// constant, never read from disk and never composed from user input, so
// design.md section 11's exclusion of "user-controlled template
// execution" holds by construction.
//
// It contains one inlined <style> block and no <script>, <link>, <img>,
// <a>, `url(...)`, or URL of any kind: requirements.md 8.3's "no HTTP
// server, CDN, network access, or sibling assets" is a property of the
// document itself rather than of how it is served. That rules out
// webfonts and image assets too, so the type system is system font stacks
// with declared fallbacks, and the one piece of iconography — the
// disclosure triangle — is drawn with CSS borders rather than set in a
// glyph that may be missing on a reader's machine.
//
// # The scanning path is one screen; everything else is one click
//
// Disclosure sections use <details>, which needs no JavaScript. They are
// what lets this format keep everything the result document holds while
// staying readable, and the page applies them by one rule: a list of rows
// is always closed, and its <summary> states how many rows it holds, so
// the collapsed page is a complete index of the run rather than a
// truncation of it. A real run reaches four figures of rows (773 resource
// changes, 838 edge changes, 549 aggregate groups, 220 estimates is an
// ordinary shape); opened by default, any one of those sections buries
// every section after it.
//
// The one thing that never goes behind a disclosure is a failure. The
// per-target error banners, the v3 trusted-fact warning, the run
// diagnostics and every outcome badge sit outside <details> at the level
// they belong to, because requirements.md 8.5 requires catalog retrieval
// failure and compilation failure to be visibly marked and a mark a
// reader has to go looking for is not one. Same for requirement 9.3's
// label and note above the estimates.
//
// A target's disclosures are laid out as a wrapping row of chips that
// expand to full width when opened (.disclosures), which is what keeps a
// four-target run to roughly a screen: closed, each target is its
// certname, its outcome, and a chip per section with a count on it.
//
// Because the bulk is now closed, the print rules force disclosure
// content visible: a shared, printed or ticket-pasted artifact that
// dropped every row it had collapsed would be a different document from
// the one on screen. Engines hide <details> content two ways, so both
// are overridden.
//
// The template is layout only. Every decision about what a section shows
// — how a change splits into sign, identity and values; which aggregate
// groups are edges; which figures the masthead tallies — is made in
// html.go, where it is testable in Go rather than in template
// conditionals.
const htmlSource = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PIACE report — {{.Outcome}}</title>
<style>
:root {
  color-scheme: light;

  --paper: #f5f4f0;
  --surface: #ffffff;
  --sunk: #fbfaf7;
  --line: #e2ded5;
  --line-soft: #eeebe4;
  --ink: #1c1a16;
  --muted: #6d675d;
  --faint: #98928a;

  --clean-fg: #1d6a3c;       --clean-bg: #e8f2ea;
  --allowed-fg: #8a6510;     --allowed-bg: #f9f0da;
  --policy-fg: #98511b;      --policy-bg: #fbeadb;
  --compile-fg: #9c2320;     --compile-bg: #fbeae7;
  --operational-fg: #7d1a17; --operational-bg: #faeae7;

  --add: #1d6a3c;
  --remove: #9c2320;
  --change: #2f4a78;

  --serif: ui-serif, Georgia, "Iowan Old Style", "Palatino Linotype", Palatino, serif;
  --sans: ui-sans-serif, system-ui, -apple-system, "Segoe UI", "Helvetica Neue", sans-serif;
  --mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;

  --radius: 8px;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  padding: 1.75rem 1.25rem 4rem;
  background: var(--paper);
  color: var(--ink);
  font-family: var(--sans);
  font-size: 15.5px;
  line-height: 1.5;
  -webkit-text-size-adjust: 100%;
}

main { max-width: 76rem; margin: 0 auto; }

/* ---- masthead ------------------------------------------------------ */

.masthead {
  background: var(--surface);
  border: 1px solid var(--line);
  border-left: 4px solid var(--rule, var(--muted));
  border-radius: var(--radius);
  padding: 1.1rem 1.35rem 1.2rem;
}
.masthead.clean { --rule: var(--clean-fg); }
.masthead.allowed { --rule: var(--allowed-fg); }
.masthead.policy { --rule: var(--policy-fg); }
.masthead.compile { --rule: var(--compile-fg); }
.masthead.operational { --rule: var(--operational-fg); }

.masthead h1 {
  font-family: var(--serif);
  font-size: 1.35rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  margin: 0;
}
.masthead-top {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: .5rem .85rem;
}
.stamp {
  font-family: var(--mono);
  font-size: .75rem;
  color: var(--faint);
  margin: .35rem 0 0;
  overflow-wrap: anywhere;
}
.stamp span { white-space: nowrap; }

.tally {
  display: flex;
  flex-wrap: wrap;
  gap: .3rem 2rem;
  margin: .9rem 0 0;
  padding: .85rem 0 0;
  border-top: 1px solid var(--line-soft);
}
.tally-item { display: flex; align-items: baseline; gap: .35rem; }
.tally-n {
  font-family: var(--serif);
  font-size: 1.25rem;
  font-weight: 600;
  letter-spacing: -0.015em;
  font-variant-numeric: tabular-nums;
}
.tally-l { font-size: .78rem; color: var(--muted); }

.reasons { list-style: none; margin: .85rem 0 0; padding: 0; }
.reasons li {
  position: relative;
  padding-left: 1.1rem;
  font-size: .85rem;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.reasons li + li { margin-top: .2rem; }
.reasons li::before {
  content: "";
  position: absolute;
  left: 0; top: .68em;
  width: .55rem; height: 1px;
  background: var(--faint);
}

/* ---- badges -------------------------------------------------------- */

.badge {
  display: inline-block;
  padding: .1rem .5rem .16rem;
  border-radius: 999px;
  font-size: .72rem;
  font-weight: 600;
  letter-spacing: .01em;
  white-space: nowrap;
}
.badge.clean { color: var(--clean-fg); background: var(--clean-bg); }
.badge.allowed { color: var(--allowed-fg); background: var(--allowed-bg); }
.badge.policy { color: var(--policy-fg); background: var(--policy-bg); }
.badge.compile { color: var(--compile-fg); background: var(--compile-bg); }
.badge.operational { color: var(--operational-fg); background: var(--operational-bg); }

/* ---- sections ------------------------------------------------------ */

h2 {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 1rem;
  font-family: var(--serif);
  font-size: 1rem;
  font-weight: 600;
  letter-spacing: -0.005em;
  margin: 1.9rem 0 .6rem;
  padding: .5rem 0 .35rem;
  background: var(--paper);
  border-bottom: 1px solid var(--line);
}
h2 .count {
  font-family: var(--mono);
  font-size: .75rem;
  font-weight: 400;
  color: var(--faint);
  font-variant-numeric: tabular-nums;
}

.empty { color: var(--faint); font-size: .85rem; margin: .35rem 0; }

/* ---- cards --------------------------------------------------------- */

.card {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: var(--radius);
  padding: .85rem 1rem .9rem;
  margin: 0 0 .55rem;
}
.card-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: .4rem .65rem;
}
.card-head h3 {
  font-family: var(--mono);
  font-size: .9rem;
  font-weight: 600;
  margin: 0;
  overflow-wrap: anywhere;
}

/* ---- banners ------------------------------------------------------- */

.banner {
  border-radius: 6px;
  padding: .5rem .7rem;
  margin: .55rem 0 0;
  font-size: .84rem;
  overflow-wrap: anywhere;
}
.banner strong { font-weight: 600; }
.banner-warn { color: var(--allowed-fg); background: var(--allowed-bg); }
.banner-error { color: var(--compile-fg); background: var(--compile-bg); }
.banner-note {
  color: var(--muted);
  background: var(--sunk);
  border: 1px solid var(--line-soft);
}

/* ---- disclosure ---------------------------------------------------- */

details { margin: .5rem 0 0; }
summary {
  display: flex;
  align-items: center;
  gap: .45rem;
  list-style: none;
  cursor: pointer;
  padding: .28rem .55rem;
  border-radius: 6px;
  background: var(--sunk);
  border: 1px solid var(--line-soft);
  font-size: .78rem;
  font-weight: 600;
  color: var(--muted);
  user-select: none;
}
summary::-webkit-details-marker { display: none; }
summary::before {
  content: "";
  flex: none;
  width: 0; height: 0;
  border-left: 5px solid currentColor;
  border-top: 4px solid transparent;
  border-bottom: 4px solid transparent;
  transform-origin: 30% 50%;
  transition: transform .15s ease;
}
details[open] > summary::before { transform: rotate(90deg); }
summary .count {
  font-family: var(--mono);
  font-weight: 400;
  color: var(--faint);
  font-variant-numeric: tabular-nums;
}
.disclosed { padding: .6rem .1rem .1rem .7rem; }

/* A target's (or the aggregate section's) disclosures sit side by side
   as chips and take a full row of their own once opened, so the closed
   card is one line of section headings rather than four stacked bars. */
.disclosures {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: .4rem;
  margin-top: .6rem;
}
.disclosures > details { margin: 0; min-width: 0; flex: 0 1 auto; }
.disclosures > details[open] { flex: 1 0 100%; }

/* ---- change rows --------------------------------------------------- */

.rows { list-style: none; margin: 0; padding: 0; }
.rows > li {
  display: grid;
  grid-template-columns: .9rem minmax(0, 1fr);
  gap: 0 .5rem;
  padding: .12rem 0;
  font-size: .8rem;
  line-height: 1.45;
  border-top: 1px solid transparent;
}
.rows > li + li { border-top-color: var(--line-soft); }
.sign {
  font-family: var(--mono);
  font-weight: 700;
  text-align: center;
  user-select: none;
}
.sign.add { color: var(--add); }
.sign.remove { color: var(--remove); }
.sign.change { color: var(--change); }

.body { min-width: 0; }
.ident { font-family: var(--mono); overflow-wrap: anywhere; }
.param { font-family: var(--mono); color: var(--muted); }
.param::before { content: " · "; color: var(--faint); }
/* The before/after pair rides on the identity's own line and wraps only
   when it has to: one row per change instead of two. */
.vals {
  font-family: var(--mono);
  color: var(--muted);
  overflow-wrap: anywhere;
}
.vals::before { content: " · "; color: var(--faint); }
.vals .was { color: var(--remove); }
.vals .now { color: var(--add); }
.note {
  font-family: var(--mono);
  color: var(--muted);
  overflow-wrap: anywhere;
}
.note::before { content: " · "; color: var(--faint); }
.on {
  display: block;
  color: var(--faint);
  font-size: .76rem;
  overflow-wrap: anywhere;
}
.arrow { color: var(--faint); padding: 0 .25rem; }

/* ---- estimates ----------------------------------------------------- */

/* One estimate is one clickable line: the resource, its status, and its
   node count are the summary; the certnames, PQL and request options are
   behind it. */
.est { margin: 0; }
.est > summary {
  background: none;
  border: none;
  border-radius: 0;
  border-top: 1px solid var(--line-soft);
  padding: .22rem .1rem;
  font-weight: 400;
  flex-wrap: wrap;
  gap: .3rem .6rem;
}
.est:first-child > summary { border-top: none; }
.est > summary .ident { font-size: .8rem; color: var(--ink); }
.est-count {
  font-size: .78rem;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.est .disclosed { padding: .35rem .1rem .55rem 1.05rem; }

/* ---- key/value grids ----------------------------------------------- */

.kv {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: .1rem .9rem;
  font-size: .8rem;
  margin: .15rem 0 .7rem;
}
.kv dt { color: var(--muted); }
.kv dd { margin: 0; font-family: var(--mono); overflow-wrap: anywhere; }
.sub {
  font-size: .72rem;
  font-weight: 600;
  letter-spacing: .04em;
  text-transform: uppercase;
  color: var(--faint);
  margin: .75rem 0 .1rem;
}
.sub:first-child { margin-top: .15rem; }
.plain {
  list-style: none;
  margin: .15rem 0 .7rem;
  padding: 0;
  font-family: var(--mono);
  font-size: .8rem;
}
.plain li { overflow-wrap: anywhere; }
.prose { font-size: .8rem; overflow-wrap: anywhere; margin: .1rem 0 .5rem; }
.prose.mono { font-family: var(--mono); }

pre {
  font-family: var(--mono);
  font-size: .75rem;
  line-height: 1.5;
  overflow-x: auto;
  max-height: 32rem;
  padding: .8rem;
  margin: .6rem 0 0;
  background: var(--sunk);
  border: 1px solid var(--line-soft);
  border-radius: 6px;
}

@media (max-width: 34rem) {
  body { padding: 1.25rem .75rem 3rem; }
  .kv { grid-template-columns: minmax(0, 1fr); gap: 0; }
  .kv dt { margin-top: .35rem; }
  .disclosures > details { flex: 1 0 100%; }
}

@media print {
  body { background: #fff; padding: 0; }
  h2 { position: static; }
  .card, .est { break-inside: avoid; }
  pre { max-height: none; }
  /* Everything the screen keeps one click away still prints: a closed
     disclosure must not silently drop rows from a shared or filed copy.
     Engines hide the content two ways, so both are overridden. The
     canonical JSON is the exception — it is the same document a second
     time, machine-readable, and half a megabyte of it on paper serves
     nobody; a reader who opens it before printing still gets it. */
  details:not(.record) > *:not(summary) { display: block !important; }
  details:not(.record)::details-content { content-visibility: visible !important; }
  details:not(.record) > summary::before { transform: rotate(90deg); }
  .disclosures > details { flex: 1 0 100%; }
}
</style>
</head>
<body>
<main>

<header class="masthead {{.OutcomeClass}}">
  <div class="masthead-top">
    <h1>PIACE report</h1>
    <span class="badge {{.OutcomeClass}}">{{.Outcome}}</span>
  </div>
  <p class="stamp"><span>exit {{.ExitCode}}</span> &middot; <span>piace {{.ToolVersion}}</span> &middot; <span>{{.TimestampUTC}}</span>{{if .Compiler}} &middot; <span>compiler {{.Compiler}}</span> &middot; <span>puppetdb {{.PuppetDB}}</span>{{end}}</p>
  {{if .Tally}}
  <div class="tally">
    {{range .Tally}}<div class="tally-item"><span class="tally-n">{{.Count}}</span><span class="tally-l">{{.Label}}</span></div>{{end}}
  </div>
  {{end}}
  {{if .Reasons}}
  <ul class="reasons">{{range .Reasons}}<li>{{.}}</li>{{end}}</ul>
  {{end}}
</header>

<h2>Targets <span class="count">{{.TotalTargets}}</span></h2>
{{range .Targets}}
<section class="card">
  <div class="card-head">
    <h3>{{.Certname}}</h3>
    <span class="badge {{.OutcomeClass}}">{{.Outcome}}</span>
  </div>

  {{if .V3Warning}}
  <div class="banner banner-warn"><strong>Trusted-fact compatibility warning.</strong> {{.V3Warning}}</div>
  {{end}}

  {{range .Failures}}
  <div class="banner banner-error"><strong>{{.Operation}} failed.</strong> {{.Message}}</div>
  {{end}}

  {{range .Warnings}}
  <div class="banner banner-warn"><strong>{{.Operation}}.</strong> {{.Message}}</div>
  {{end}}

  {{if not .Compared}}
  <p class="empty">No node diff was produced for this target.</p>
  {{else if not .HasDifference}}
  <p class="empty">No non-excluded differences.</p>
  {{end}}

  <div class="disclosures">
    {{if .Changes}}
    <details>
      <summary>Resource changes <span class="count">{{len .Changes}}</span></summary>
      <div class="disclosed">
        <ul class="rows">
          {{range .Changes}}
          <li>
            <span class="sign {{.Class}}">{{.Sign}}</span>
            <span class="body"><span class="ident">{{.Identity}}</span>{{if .Parameter}}<span class="param">{{.Parameter}}</span>{{end}}{{if .HasValues}}<span class="vals"><span class="was">{{.Before}}</span><span class="arrow">&rarr;</span><span class="now">{{.After}}</span></span>{{end}}{{if .Note}}<span class="note">{{.Note}}</span>{{end}}</span>
          </li>
          {{end}}
        </ul>
      </div>
    </details>
    {{end}}

    {{if .EdgeChanges}}
    <details>
      <summary>Dependency-graph edges <span class="count">{{len .EdgeChanges}}</span></summary>
      <div class="disclosed">
        <ul class="rows">
          {{range .EdgeChanges}}
          <li>
            <span class="sign {{.Class}}">{{.Sign}}</span>
            <span class="body"><span class="ident">{{.Source}}</span><span class="arrow">&rarr;</span><span class="ident">{{.Target}}</span></span>
          </li>
          {{end}}
        </ul>
      </div>
    </details>
    {{end}}

    {{if .Exclusions}}
    <details>
      <summary>Excluded differences <span class="count">{{len .Exclusions}}</span></summary>
      <div class="disclosed">
        <ul class="plain">{{range .Exclusions}}<li>{{.}}</li>{{end}}</ul>
      </div>
    </details>
    {{end}}

    <details>
      <summary>Provenance and resolved configuration</summary>
      <div class="disclosed">
        {{if .Baseline}}<p class="sub">baseline</p><dl class="kv">{{range .Baseline}}<dt>{{.Key}}</dt><dd>{{.Value}}</dd>{{end}}</dl>{{end}}
        {{if .Facts}}<p class="sub">facts</p><dl class="kv">{{range .Facts}}<dt>{{.Key}}</dt><dd>{{.Value}}</dd>{{end}}</dl>{{end}}
        {{if .Candidate}}<p class="sub">candidate</p><dl class="kv">{{range .Candidate}}<dt>{{.Key}}</dt><dd>{{.Value}}</dd>{{end}}</dl>{{end}}
        {{if .Config}}<p class="sub">configuration</p><dl class="kv">{{range .Config}}<dt>{{.Key}}</dt><dd>{{.Value}}</dd>{{end}}</dl>{{end}}
        {{if .Exclude}}<p class="sub">exclusion rules</p><ul class="plain">{{range .Exclude}}<li>{{.}}</li>{{end}}</ul>{{end}}
        {{if .Redact}}<p class="sub">redaction selectors</p><ul class="plain">{{range .Redact}}<li>{{.}}</li>{{end}}</ul>{{end}}
      </div>
    </details>
  </div>
</section>
{{end}}

<h2>Aggregate diff <span class="count">{{.TotalGroups}}</span></h2>
{{if or .Aggregate .EdgeAggregate}}
<section class="card">
  <div class="disclosures">
    {{if .Aggregate}}
    <details>
      <summary>Grouped resource changes <span class="count">{{.TotalGroups}}</span></summary>
      <div class="disclosed">
        <ul class="rows">
          {{range .Aggregate}}
          <li>
            <span class="sign {{.Class}}">{{.Sign}}</span>
            <span class="body"><span class="ident">{{.Identity}}</span>{{if .Parameter}}<span class="param">{{.Parameter}}</span>{{end}}{{if .HasValues}}<span class="vals"><span class="was">{{.Before}}</span><span class="arrow">&rarr;</span><span class="now">{{.After}}</span></span>{{end}}<span class="on">{{.Targets}}</span></span>
          </li>
          {{end}}
        </ul>
      </div>
    </details>
    {{end}}
    {{if .EdgeAggregate}}
    <details>
      <summary>Dependency-graph edge groups <span class="count">{{.TotalEdgeGroups}}</span></summary>
      <div class="disclosed">
        <ul class="rows">
          {{range .EdgeAggregate}}
          <li>
            <span class="sign {{.Class}}">{{.Sign}}</span>
            <span class="body"><span class="ident">{{.Source}}</span><span class="arrow">&rarr;</span><span class="ident">{{.Target}}</span><span class="on">{{.Targets}}</span></span>
          </li>
          {{end}}
        </ul>
      </div>
    </details>
    {{end}}
  </div>
</section>
{{else}}
<p class="empty">No grouped resource changes.</p>
{{end}}

{{if .Estimates}}
<h2>{{.EstimateLabel}} <span class="count">{{.TotalEstimates}}</span></h2>
<div class="banner banner-note">{{.EstimateNote}}</div>
<section class="card">
  <div class="disclosures">
    <details>
      <summary>Queried resources <span class="count">{{.TotalEstimates}}</span>{{if .TotalEstimateFailures}}<span class="badge operational">{{.TotalEstimateFailures}} failed</span>{{end}}</summary>
      <div class="disclosed">
        {{range .Estimates}}
        <details class="est">
          <summary><span class="ident">{{.Identity}}</span>{{if .Failed}}<span class="badge operational">{{.Status}}</span>{{end}}<span class="est-count">{{.Count}}</span></summary>
          <div class="disclosed">
            {{if .Certnames}}<p class="sub">{{.NodeLabel}} whose latest stored catalog contains this resource</p><p class="prose mono">{{.Certnames}}</p>{{end}}
            <p class="sub">pql</p><p class="prose mono">{{.PQL}}</p>
            <p class="sub">request</p><p class="prose mono">{{.Request}}</p>
          </div>
        </details>
        {{end}}
      </div>
    </details>
  </div>
</section>
{{end}}

{{if .Diagnostics}}
<h2>Run diagnostics <span class="count">{{len .Diagnostics}}</span></h2>
{{range .Diagnostics}}
<div class="banner {{if eq .Severity "error"}}banner-error{{else}}banner-warn{{end}}"><strong>{{.Operation}}.</strong> {{.Message}}</div>
{{end}}
{{end}}
{{- with .Assessment}}

<h2>{{.Label}} <span class="badge {{.RiskClass}}">{{.Risk}}</span></h2>
<div class="banner banner-note">{{.Note}}</div>
{{if .Stamp}}<p class="stamp">{{range $i, $s := .Stamp}}{{if $i}} &middot; {{end}}<span>{{$s}}</span>{{end}}</p>{{end}}
{{if .Truncation}}<div class="banner banner-warn">{{.Truncation}}</div>{{end}}
{{if .InputPartial}}<div class="banner banner-warn">{{.InputPartial}}</div>{{end}}
{{range .Diagnostics}}<div class="banner {{if eq .Severity "error"}}banner-error{{else}}banner-warn{{end}}">{{.Message}}</div>
{{end}}{{if .Summary}}<p class="prose">{{.Summary}}</p>{{end}}
{{if .ReviewFocus}}<p class="sub">review focus</p><ul class="plain">{{range .ReviewFocus}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .Groups}}
<section class="node">
  <div class="disclosures">
    <details>
      <summary>Group risk indications <span class="count">{{.TotalGroups}}</span></summary>
      <div class="disclosed">
        <ul class="rows">
          {{range .Groups}}
          <li>
            <span class="badge {{.RiskClass}}">{{.Risk}}</span>
            <span class="body"><span class="ident">{{.Identity}}</span>{{if .Parameter}}<span class="param">{{.Parameter}}</span>{{end}}<span class="on">{{.Targets}}</span>{{if .Rationale}}<span class="note">{{.Rationale}}</span>{{end}}{{range .ReviewFocus}}<span class="note">{{.}}</span>{{end}}</span>
          </li>
          {{end}}
        </ul>
      </div>
    </details>
  </div>
</section>
{{end}}
{{end}}

<h2>Result document</h2>
<details class="record">
  <summary>Canonical JSON &mdash; schema-versioned, identical to the --json-out artifact</summary>
  <div class="disclosed"><pre>{{.CanonicalJSON}}</pre></div>
</details>

</main>
</body>
</html>
`
