package report

import (
	"bytes"
	"fmt"
	"html/template"

	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// HTML renders r as the static review artifact required by
// requirements.md 8.3-8.5: a single self-contained document that opens
// over `file://` with no HTTP server, CDN, network access, or sibling
// assets.
//
// Self-containment is structural, not a review promise: the template is a
// package constant with one inlined <style> block, no <script>, no <img>,
// no <link>, and no URL of any kind. There is nothing in the document
// that could issue a request. Expand/collapse uses <details>, so the page
// stays interactive with no JavaScript and the document has no script
// context for an untrusted value to escape into.
//
// Everything variable is interpolated through html/template, whose
// contextual escaping is what makes an attacker-shaped resource title or
// diagnostic message inert.
func HTML(r model.Result) ([]byte, error) {
	jsonData, err := JSON(r)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	if err := htmlTemplate.Execute(&b, buildHTMLView(r, string(jsonData))); err != nil {
		return nil, fmt.Errorf("rendering HTML report: %w", err)
	}
	return b.Bytes(), nil
}

// htmlView is the display-ready projection of a model.Result. It exists
// so the template contains layout only: every decision about what to show
// is made in Go, where it is testable, rather than in template logic.
type htmlView struct {
	ToolVersion  string
	TimestampUTC string
	Outcome      string
	OutcomeClass string
	// Compiler/PuppetDB are the run-level service authorities
	// (model.ServiceProvenance); empty when the document records none.
	Compiler  string
	PuppetDB  string
	ExitCode  int
	Reasons   []string
	Targets   []htmlTarget
	Aggregate []htmlGroup
	// EstimateLabel/EstimateNote carry requirement 9.3's wording into the
	// document; see doc.go.
	EstimateLabel  string
	EstimateNote   string
	Estimates      []htmlEstimate
	Diagnostics    []htmlDiagnostic
	CanonicalJSON  string
	TotalTargets   int
	TotalGroups    int
	TotalEstimates int
}

type htmlTarget struct {
	Certname      string
	Outcome       string
	OutcomeClass  string
	Baseline      []mapEntry
	Facts         []mapEntry
	Candidate     []mapEntry
	Config        []mapEntry
	Exclude       []string
	Redact        []string
	V3Warning     string
	Failures      []htmlDiagnostic
	Warnings      []htmlDiagnostic
	HasDifference bool
	Compared      bool
	Changes       []string
	Exclusions    []string
}

type htmlGroup struct {
	Label     string
	Before    string
	After     string
	HasValues bool
	Certnames []string
}

type htmlEstimate struct {
	Identity  string
	Status    string
	Summary   string
	PQL       string
	Request   string
	Certnames []string
	Failed    bool
}

type htmlDiagnostic struct {
	Severity  string
	Operation string
	Message   string
}

func buildHTMLView(r model.Result, canonicalJSON string) htmlView {
	view := htmlView{
		ToolVersion:    r.Invocation.ToolVersion,
		TimestampUTC:   r.Invocation.TimestampUTC,
		Outcome:        string(r.Outcome),
		OutcomeClass:   outcomeClass(r.Outcome),
		ExitCode:       r.ExitCode,
		Reasons:        r.Reasons,
		EstimateLabel:  ImpactEstimateLabel,
		EstimateNote:   ImpactEstimateNote,
		CanonicalJSON:  canonicalJSON,
		TotalTargets:   len(r.Targets),
		TotalGroups:    len(r.Aggregate.Groups),
		TotalEstimates: len(r.ImpactEstimates),
	}

	if s := r.Invocation.Services; s != nil {
		view.Compiler = s.Compiler
		view.PuppetDB = s.PuppetDB
	}

	for _, t := range r.Targets {
		view.Targets = append(view.Targets, buildHTMLTarget(t))
	}
	for _, g := range r.Aggregate.Groups {
		view.Aggregate = append(view.Aggregate, htmlGroup{
			Label:     aggregateKeyLabel(g.Key),
			Before:    formatValue(g.Before),
			After:     formatValue(g.After),
			HasValues: g.Key.Edge == nil && (g.Before != nil || g.After != nil),
			Certnames: g.Certnames,
		})
	}
	for _, e := range r.ImpactEstimates {
		request := fmt.Sprintf("path=%s limit=%d timeout=%s", e.Request.Path, e.Request.Limit, e.Timeout)
		if e.Request.OrderBy != "" {
			request += " order_by=" + e.Request.OrderBy
		}
		view.Estimates = append(view.Estimates, htmlEstimate{
			Identity:  e.Identity.String(),
			Status:    string(e.Status),
			Summary:   estimateSummary(e),
			PQL:       e.PQL,
			Request:   request,
			Certnames: e.Certnames,
			Failed:    e.Status != model.ImpactStatusCompleted,
		})
	}
	for _, d := range r.Diagnostics {
		view.Diagnostics = append(view.Diagnostics, htmlDiagnostic{
			Severity: string(d.Severity), Operation: string(d.Operation), Message: d.Message,
		})
	}
	return view
}

func buildHTMLTarget(t model.TargetResult) htmlTarget {
	out := htmlTarget{
		Certname:     t.Certname,
		Outcome:      string(t.Outcome),
		OutcomeClass: outcomeClass(t.Outcome),
		Compared:     t.NodeDiff != nil,
	}
	if t.Baseline != nil {
		out.Baseline = mapEntries(sourceProvenanceMap(*t.Baseline))
	}
	if t.Facts != nil {
		out.Facts = mapEntries(sourceProvenanceMap(*t.Facts))
	}
	if t.Candidate != nil {
		out.Candidate = mapEntries(candidateProvenanceMap(*t.Candidate))
		out.V3Warning = t.Candidate.V3Warning
	}
	if t.Config != nil {
		out.Config = configProvenanceEntries(*t.Config)
		for _, rule := range t.Config.Exclude {
			out.Exclude = append(out.Exclude, fmt.Sprintf("%s[%s]", rule.Type, rule.Title))
		}
		for _, sel := range t.Config.Redact {
			out.Redact = append(out.Redact, fmt.Sprintf("%s.%s", sel.Type, sel.Parameter))
		}
	}

	// requirements.md 8.5 requires catalog retrieval failure and
	// compilation failure to be visibly marked, so error diagnostics are
	// split out of the general list into their own banner rather than
	// being one row among many.
	for _, d := range t.Diagnostics {
		entry := htmlDiagnostic{Severity: string(d.Severity), Operation: string(d.Operation), Message: d.Message}
		if d.Severity == model.SeverityError {
			out.Failures = append(out.Failures, entry)
			continue
		}
		out.Warnings = append(out.Warnings, entry)
	}

	if t.NodeDiff != nil {
		out.HasDifference = t.NodeDiff.HasDifference
		for _, c := range t.NodeDiff.ResourceChanges {
			out.Changes = append(out.Changes, changeSummary(c))
		}
		for _, c := range t.NodeDiff.EdgeChanges {
			out.Changes = append(out.Changes, edgeSummary(c))
		}
		for _, e := range t.NodeDiff.Exclusions {
			out.Exclusions = append(out.Exclusions, exclusionSummary(e))
		}
	}
	return out
}

func sourceProvenanceMap(p model.SourceProvenance) map[string]any {
	m := map[string]any{"source": string(p.Kind), "certname": p.Certname}
	addNonEmpty(m, "environment", p.Environment)
	addNonEmpty(m, "producer_timestamp", p.ProducerTimestamp)
	addNonEmpty(m, "catalog_identity", p.CatalogIdentity)
	addNonEmpty(m, "producer", p.Producer)
	return m
}

func candidateProvenanceMap(p model.CandidateProvenance) map[string]any {
	m := map[string]any{
		"requested_api": string(p.RequestedAPI),
		"effective_api": string(p.EffectiveAPI),
		"environment":   p.Environment,
		"fact_source":   string(p.FactSource),
	}
	addNonEmpty(m, "factset_identity", p.FactsetIdentity)
	addNonEmpty(m, "trusted_facts_source", string(p.TrustedFactsSource))
	if p.FellBackFromV4 {
		m["fell_back_from_v4"] = true
	}
	return m
}

// configProvenanceEntries flattens the resolved configuration provenance
// into ordered display rows. The Exclude/Redact rule lists are rendered
// separately (they are lists, not scalars), so only the scalar sections
// are flattened here.
func configProvenanceEntries(c model.ConfigProvenance) []mapEntry {
	flat := map[string]any{"fail_on_diff": c.FailOnDiff}
	for prefix, section := range map[string]map[string]any{
		"candidate":       c.Candidate,
		"facts":           c.Facts,
		"baseline":        c.Baseline,
		"impact_estimate": c.ImpactEstimate,
	} {
		for _, k := range sortedKeys(section) {
			flat[prefix+"."+k] = section[k]
		}
	}
	return mapEntries(flat)
}

func addNonEmpty(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// outcomeClass maps an outcome to the CSS class that colors its badge.
// An unrecognized outcome gets the most severe styling, mirroring
// exitcode.ForOutcome's rule that an unknown classification is never
// presented as success.
func outcomeClass(o exitcode.Outcome) string {
	switch o {
	case exitcode.OutcomeClean:
		return "clean"
	case exitcode.OutcomeDifferencesAllowed:
		return "allowed"
	case exitcode.OutcomePolicyDisallowedDifference:
		return "policy"
	case exitcode.OutcomeCompilationFailure:
		return "compile"
	default:
		return "operational"
	}
}

var htmlTemplate = template.Must(template.New("piace").Parse(htmlSource))
