package report

// htmlSource is the complete HTML document template. It is a package
// constant, never read from disk and never composed from user input, so
// design.md section 11's exclusion of "user-controlled template
// execution" holds by construction.
//
// It contains one inlined <style> block and no <script>, <link>, <img>,
// or URL of any kind: requirements.md 8.3's "no HTTP server, CDN, network
// access, or sibling assets" is a property of the document itself rather
// than of how it is served. Disclosure sections use <details>, which
// needs no JavaScript.
const htmlSource = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PIACE report — {{.Outcome}}</title>
<style>
:root { color-scheme: light dark; }
body { font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
       margin: 0; padding: 1.5rem; line-height: 1.5; }
main { max-width: 68rem; margin: 0 auto; }
h1 { font-size: 1.4rem; margin: 0 0 .25rem 0; }
h2 { font-size: 1.15rem; margin: 2rem 0 .5rem 0; border-bottom: 1px solid currentColor; padding-bottom: .2rem; }
h3 { font-size: 1rem; margin: 0 0 .35rem 0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
code, pre, .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85rem; }
pre { overflow-x: auto; padding: .6rem; border: 1px solid rgba(128,128,128,.4); border-radius: .3rem; }
.badge { display: inline-block; padding: .1rem .55rem; border-radius: .8rem; font-size: .8rem;
         font-weight: 600; border: 1px solid; }
.clean { color: #14622f; border-color: #14622f; }
.allowed { color: #6b5300; border-color: #6b5300; }
.policy { color: #8a4b00; border-color: #8a4b00; }
.compile { color: #9b1c1c; border-color: #9b1c1c; }
.operational { color: #7a1010; border-color: #7a1010; }
.target { border: 1px solid rgba(128,128,128,.4); border-radius: .4rem; padding: .8rem 1rem; margin: .8rem 0; }
.banner { border-left: .3rem solid; padding: .5rem .75rem; margin: .5rem 0; border-radius: .2rem; }
.banner-warn { border-color: #6b5300; background: rgba(180,140,0,.10); }
.banner-error { border-color: #9b1c1c; background: rgba(155,28,28,.10); }
.banner-note { border-color: #3a5a99; background: rgba(58,90,153,.10); }
.kv { display: grid; grid-template-columns: max-content 1fr; gap: .1rem .8rem; font-size: .85rem; }
.kv dt { font-weight: 600; }
.kv dd { margin: 0; overflow-wrap: anywhere; }
ul.plain { list-style: none; padding-left: 0; margin: .3rem 0; }
ul.plain li { overflow-wrap: anywhere; }
.changes li { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85rem; }
.muted { opacity: .75; font-size: .85rem; }
details { margin: .4rem 0; }
summary { cursor: pointer; font-size: .9rem; }
</style>
</head>
<body>
<main>

<h1>PIACE report</h1>
<p><span class="badge {{.OutcomeClass}}">{{.Outcome}}</span>
   <span class="muted">exit code {{.ExitCode}} &middot; piace {{.ToolVersion}} &middot; {{.TimestampUTC}}</span></p>
{{if .Compiler}}
<p class="muted mono">compiler: {{.Compiler}} &middot; puppetdb: {{.PuppetDB}}</p>
{{end}}
{{if .Reasons}}
<ul class="plain">{{range .Reasons}}<li>&mdash; {{.}}</li>{{end}}</ul>
{{end}}

<h2>Targets ({{.TotalTargets}})</h2>
{{range .Targets}}
<section class="target">
  <h3>{{.Certname}} <span class="badge {{.OutcomeClass}}">{{.Outcome}}</span></h3>

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
  <p class="muted">No node diff was produced for this target.</p>
  {{else if not .HasDifference}}
  <p class="muted">No non-excluded differences.</p>
  {{else}}
  <ul class="plain changes">{{range .Changes}}<li>{{.}}</li>{{end}}</ul>
  {{end}}

  {{if .Exclusions}}
  <div class="banner banner-note"><strong>Excluded differences.</strong>
    <ul class="plain">{{range .Exclusions}}<li>{{.}}</li>{{end}}</ul>
  </div>
  {{end}}

  <details>
    <summary>Provenance and resolved configuration</summary>
    {{if .Baseline}}<p class="muted">baseline</p><dl class="kv">{{range .Baseline}}<dt>{{.Key}}</dt><dd class="mono">{{.Value}}</dd>{{end}}</dl>{{end}}
    {{if .Facts}}<p class="muted">facts</p><dl class="kv">{{range .Facts}}<dt>{{.Key}}</dt><dd class="mono">{{.Value}}</dd>{{end}}</dl>{{end}}
    {{if .Candidate}}<p class="muted">candidate</p><dl class="kv">{{range .Candidate}}<dt>{{.Key}}</dt><dd class="mono">{{.Value}}</dd>{{end}}</dl>{{end}}
    {{if .Config}}<p class="muted">configuration</p><dl class="kv">{{range .Config}}<dt>{{.Key}}</dt><dd class="mono">{{.Value}}</dd>{{end}}</dl>{{end}}
    {{if .Exclude}}<p class="muted">exclusion rules</p><ul class="plain mono">{{range .Exclude}}<li>{{.}}</li>{{end}}</ul>{{end}}
    {{if .Redact}}<p class="muted">redaction selectors</p><ul class="plain mono">{{range .Redact}}<li>{{.}}</li>{{end}}</ul>{{end}}
  </details>
</section>
{{end}}

<h2>Aggregate diff ({{.TotalGroups}})</h2>
{{if not .Aggregate}}<p class="muted">No grouped changes.</p>{{end}}
<ul class="plain">
{{range .Aggregate}}
  <li>
    <span class="mono">{{.Label}}</span>
    {{if .HasValues}}<div class="mono muted">{{.Before}} &rarr; {{.After}}</div>{{end}}
    <div class="muted">{{len .Certnames}} target(s): {{range $i, $c := .Certnames}}{{if $i}}, {{end}}{{$c}}{{end}}</div>
  </li>
{{end}}
</ul>

{{if .Estimates}}
<h2>{{.EstimateLabel}} ({{.TotalEstimates}})</h2>
<div class="banner banner-note">{{.EstimateNote}}</div>
<ul class="plain">
{{range .Estimates}}
  <li>
    <span class="mono">{{.Identity}}</span>
    <span class="badge {{if .Failed}}operational{{else}}clean{{end}}">{{.Status}}</span>
    <div class="muted">{{.Summary}}</div>
    <div class="mono muted">pql: {{.PQL}}</div>
    <div class="mono muted">request: {{.Request}}</div>
    {{if .Certnames}}
    <div class="muted">nodes whose latest stored catalog contains this resource:
      <span class="mono">{{range $i, $c := .Certnames}}{{if $i}}, {{end}}{{$c}}{{end}}</span></div>
    {{end}}
  </li>
{{end}}
</ul>
{{end}}

{{if .Diagnostics}}
<h2>Run diagnostics</h2>
{{range .Diagnostics}}
<div class="banner {{if eq .Severity "error"}}banner-error{{else}}banner-warn{{end}}"><strong>{{.Operation}}.</strong> {{.Message}}</div>
{{end}}
{{end}}

<h2>Result document</h2>
<details>
  <summary>Canonical JSON (schema-versioned, identical to the --json-out artifact)</summary>
  <pre>{{.CanonicalJSON}}</pre>
</details>

</main>
</body>
</html>
`
