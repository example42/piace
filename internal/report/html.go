package report

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// HTML renders r as the static review artifact: a single self-contained
// document that opens over `file://` with no HTTP server, CDN, network
// access, or sibling assets.
//
// Self-containment is structural, not a review promise: the template is
// a package constant with one inlined <style> block, no <script>, no
// <img>, no <link>, no `url(...)`, and no URL of any kind. There is
// nothing in the document that could issue a request, which also means
// no webfont, so the type system is built from system font stacks and
// every disclosure marker is drawn in CSS rather than set in a glyph a
// reader's machine may not have.
//
// Expand/collapse is <details>, so the page stays interactive with no
// JavaScript and the document has no script context for an untrusted
// value to escape into.
//
// Unlike the text report, HTML takes no display Options: it shows
// everything the result document holds, edge changes, an estimate's PQL,
// request options, and the full certname list, and uses disclosure
// rather than omission to keep the page readable. Every list of rows is
// closed and its summary carries its count, so a reader scanning for the
// outcome reads an index of the run rather than paging through it, and a
// reader who wants a section opens it. What never goes behind a
// disclosure is a failure: the per-target error banners, the v3 warning,
// the run diagnostics and the outcome badges stay in the scanning path,
// because a mark a reader has to go looking for is not a visible mark.
// Warning-severity target diagnostics collapse into a counted Notices
// chip; their count stays in the scanning path. content_indeterminate
// File parameter rows are omitted from Resource changes for the same
// reason those notices exist (see displayedResourceChange). See doc.go.
//
// Everything variable is interpolated through html/template, whose
// contextual escaping is what makes an attacker-shaped resource title or
// diagnostic message inert.
func HTML(r model.Result, a *assess.Assessment) ([]byte, error) {
	jsonData, err := JSON(r)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	if err := htmlTemplate.Execute(&b, buildHTMLView(r, a, string(jsonData))); err != nil {
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
	Compiler string
	PuppetDB string
	ExitCode int
	Reasons  []string
	Tally    []htmlTally
	Targets  []htmlTarget
	// Aggregate holds the resource-level groups; EdgeAggregate holds the
	// edge groups, which the page discloses separately rather than
	// interleaving. Both are shown: edge changes are a distinct aggregate
	// kind, and this format keeps all of it.
	Aggregate     []htmlGroup
	EdgeAggregate []htmlEdgeGroup
	// EstimateLabel and EstimateNote carry the mandatory wording into the
	// document; see doc.go.
	EstimateLabel   string
	EstimateNote    string
	Estimates       []htmlEstimate
	Diagnostics     []htmlDiagnostic
	CanonicalJSON   string
	TotalTargets    int
	TotalGroups     int
	TotalEdgeGroups int
	TotalEstimates  int
	// TotalEstimateFailures rides on the estimate list's closed summary.
	// The list is a disclosure like every other list of rows on the page,
	// and a failed estimate inside a closed one would be invisible; the
	// count keeps it in the scanning path without lifting the failed
	// entries out of their place in the list.
	TotalEstimateFailures int
	// Assessment is the advisory change assessment, nil when the report was
	// rendered without one. Nil renders nothing at all, not an empty section
	// and not a stray newline, because a `piace compare` report has to stay
	// byte-identical to one rendered before the assessment existed.
	Assessment *htmlAssessment
}

// htmlAssessment is the display-ready projection of an
// assess.Assessment. It is a separate view type rather than the
// assessment itself for the same reason htmlView exists: the template
// holds layout, and every decision about what a reader sees is made
// here.
//
// Nothing in it is ever template.HTML. Summary, Rationale and
// ReviewFocus are model-generated free text arriving from outside the
// building, and are exactly as untrusted as a resource title from a
// catalog. html/template's contextual escaping is what makes them inert.
type htmlAssessment struct {
	Label     string
	Note      string
	Risk      string
	RiskClass string
	// Stamp is the provenance line: which model, which endpoint
	// authority, when, and the checksum of the result document the
	// assessment was derived from. It is the assessment's counterpart to
	// the masthead's run stamp, and it is what lets a reader tell two
	// assessments of the same report apart.
	Stamp []string
	// Truncation and InputPartial are stated on the page, never left to
	// the artifact: a section that assessed a fifth of the groups, or was
	// built on a run that failed to compare half its targets, reads as a
	// complete review unless it says otherwise.
	Truncation   string
	InputPartial string
	// Diagnostics are the assessment's own failures. They are banners
	// outside every disclosure, like the run diagnostics above them, because
	// the case they exist for is an assessment that says "unknown" for
	// everything, which without a visible reason reads as a broken page
	// rather than a failed request.
	Diagnostics []htmlAssessmentDiagnostic
	Summary     string
	ReviewFocus []string
	Groups      []htmlAssessmentGroup
	TotalGroups int
}

type htmlAssessmentDiagnostic struct {
	Severity string
	Message  string
}

// htmlAssessmentGroup is one aggregate group's judgement, anchored to
// the group by the same identity the deterministic aggregate section
// above it shows, so a reader can tie an opinion to a difference.
type htmlAssessmentGroup struct {
	Identity    string
	Parameter   string
	Risk        string
	RiskClass   string
	Rationale   string
	Targets     string
	ReviewFocus []string
}

// htmlTally is one figure in the masthead's at-a-glance row. It is
// navigation, not new information: every number in it is the length of a
// section further down, shown once at the top so a reader knows the shape
// of a report before scrolling a thousand rows of it.
type htmlTally struct {
	Count int
	Label string
}

type htmlTarget struct {
	Certname     string
	Outcome      string
	OutcomeClass string
	Baseline     []mapEntry
	Facts        []mapEntry
	Candidate    []mapEntry
	Config       []mapEntry
	Exclude      []string
	Redact       []string
	V3Warning    string
	// BaselineV3Warning is the same warning about the baseline's own
	// capture: those are the bytes being compared, whatever API this
	// run used.
	BaselineV3Warning string
	Failures          []htmlDiagnostic
	// Warnings are warning-severity target diagnostics (verify_content and
	// similar). They render as a closed Notices chip with a count, not as
	// always-open banners: a real catalog can emit many of them, and the
	// scanning path only needs the count.
	Warnings []htmlDiagnostic
	Compared bool
	// Changes and EdgeChanges are the target's differences, split because
	// the page discloses them separately: a run's edge differences
	// routinely outnumber its resource differences and are usually
	// connected to them, so they get their own chip rather than padding a
	// list a
	// reader opens first. HasDifference stays authoritative for whether
	// the target changed at all, so a target whose only differences are
	// edges is never rendered as unchanged.
	HasDifference bool
	Changes       []htmlChange
	EdgeChanges   []htmlEdge
	Exclusions    []string
}

// htmlChange is one resource-level difference, split into the parts the
// page styles independently: a colored sign gutter, a monospace
// identity, a parameter name, and a before and after pair. Splitting
// happens here rather than in the template so the layout stays free of
// logic.
type htmlChange struct {
	// Sign is the gutter mark; Class is the CSS class keyed to it.
	Sign      string
	Class     string
	Kind      string
	Identity  string
	Parameter string
	Before    string
	After     string
	HasValues bool
	// Note carries File-content evidence in place of a value pair: the
	// comparison state, its evidence source, and the digest pair. Managed
	// content bytes are never available here to render.
	Note string
}

// htmlEdge is one dependency-graph edge difference. Direction is
// significant, so Source and Target are separate fields and the arrow
// between them is drawn by the page, never normalized away.
type htmlEdge struct {
	Sign   string
	Class  string
	Source string
	Target string
}

// htmlGroup and htmlEdgeGroup are aggregate groups: one difference plus
// the targets exhibiting it.
type htmlGroup struct {
	htmlChange
	Targets string
}

type htmlEdgeGroup struct {
	htmlEdge
	Targets string
}

// htmlEstimate is one impact estimate. Count is what the bounded query
// returned; Certnames, PQL and Request are the detail behind the
// disclosure, all of it present and none of it in the scanning path.
type htmlEstimate struct {
	Identity  string
	Status    string
	Count     string
	Certnames string
	NodeLabel string
	PQL       string
	Request   string
	Failed    bool
}

type htmlDiagnostic struct {
	Severity  string
	Operation string
	Message   string
}

func buildHTMLView(r model.Result, a *assess.Assessment, canonicalJSON string) htmlView {
	view := htmlView{
		ToolVersion:   r.Invocation.ToolVersion,
		TimestampUTC:  r.Invocation.TimestampUTC,
		Outcome:       string(r.Outcome),
		OutcomeClass:  outcomeClass(r.Outcome),
		ExitCode:      r.ExitCode,
		Reasons:       r.Reasons,
		EstimateLabel: ImpactEstimateLabel,
		EstimateNote:  ImpactEstimateNote,
		CanonicalJSON: canonicalJSON,
		TotalTargets:  len(r.Targets),
	}

	if s := r.Invocation.Services; s != nil {
		view.Compiler = s.Compiler
		view.PuppetDB = s.PuppetDB
	}

	var changes, edges int
	for _, t := range r.Targets {
		view.Targets = append(view.Targets, buildHTMLTarget(t))
		if t.NodeDiff != nil {
			changes += len(displayedResourceChanges(t.NodeDiff.ResourceChanges))
			edges += len(t.NodeDiff.EdgeChanges)
		}
	}

	for _, g := range r.Aggregate.Groups {
		targets := targetCountList(g.Certnames)
		if g.Key.Edge != nil {
			view.EdgeAggregate = append(view.EdgeAggregate, htmlEdgeGroup{
				htmlEdge: edgeKeyParts(g.Key), Targets: targets,
			})
			continue
		}
		if !displayedAggregateGroup(g) {
			continue
		}
		view.Aggregate = append(view.Aggregate, htmlGroup{
			htmlChange: groupKeyParts(g), Targets: targets,
		})
	}
	view.TotalGroups = len(view.Aggregate)
	view.TotalEdgeGroups = len(view.EdgeAggregate)

	for _, e := range r.ImpactEstimates {
		estimate := buildHTMLEstimate(e)
		if estimate.Failed {
			view.TotalEstimateFailures++
		}
		view.Estimates = append(view.Estimates, estimate)
	}
	view.TotalEstimates = len(view.Estimates)

	for _, d := range r.Diagnostics {
		view.Diagnostics = append(view.Diagnostics, htmlDiagnostic{
			Severity: string(d.Severity), Operation: string(d.Operation), Message: d.Message,
		})
	}

	view.Tally = buildHTMLTally(view, changes, edges)
	view.Assessment = buildHTMLAssessment(a)
	return view
}

// buildHTMLAssessment projects a change assessment for display, or
// returns nil when there is none.
func buildHTMLAssessment(a *assess.Assessment) *htmlAssessment {
	if a == nil {
		return nil
	}
	view := &htmlAssessment{
		Label:       AssessmentLabel,
		Note:        AssessmentNote,
		Risk:        string(a.Run.Risk),
		RiskClass:   riskClass(a.Run.Risk),
		Stamp:       assessmentStamp(*a),
		Summary:     a.Run.Summary,
		ReviewFocus: a.Run.ReviewFocus,
		TotalGroups: len(a.Groups),
	}
	if a.GroupsTruncated {
		view.Truncation = fmt.Sprintf(
			"Assessed %d of %d resource-change groups. The rest were ranked lower and never sent, so this section says nothing about them.",
			a.GroupsAssessed, a.GroupsTotal)
	}
	if a.InputPartial {
		view.InputPartial = "The result document this was built from is itself incomplete (the run recorded diagnostics above), so the assessment did not see the whole comparison."
	}
	for _, d := range a.Diagnostics {
		view.Diagnostics = append(view.Diagnostics, htmlAssessmentDiagnostic{
			Severity: string(d.Severity), Message: d.Message,
		})
	}
	for _, g := range a.Groups {
		view.Groups = append(view.Groups, htmlAssessmentGroup{
			Identity:    g.Identity,
			Parameter:   g.Parameter,
			Risk:        string(g.Risk),
			RiskClass:   riskClass(g.Risk),
			Rationale:   g.Rationale,
			Targets:     targetCountList(g.Certnames),
			ReviewFocus: g.ReviewFocus,
		})
	}
	return view
}

// assessmentStamp builds the provenance line. Empty fields are dropped
// rather than rendered as empty separators: an assessment produced
// without a checksum should show one fewer item, not a stray middle dot.
func assessmentStamp(a assess.Assessment) []string {
	var stamp []string
	for _, part := range []string{a.ModelID, a.EndpointAuthority, a.GeneratedAt, a.SourceReportChecksum} {
		if part != "" {
			stamp = append(stamp, part)
		}
	}
	return stamp
}

// riskClass maps a risk indication onto the badge classes the page
// already defines for outcomes, rather than introducing a second
// severity palette. Two reasons, and only one of them is aesthetic: a
// page with one visual language for severity is read faster, and a
// report rendered with no assessment must be byte-identical to one
// rendered before the assessment existed, which a new rule in the
// stylesheet, emitted unconditionally, would break.
func riskClass(r assess.Risk) string {
	switch r {
	case assess.RiskLow:
		return "clean"
	case assess.RiskMedium:
		return "allowed"
	case assess.RiskHigh:
		return "compile"
	default:
		return "operational"
	}
}

// buildHTMLTally assembles the masthead figures, omitting any that is
// zero. A row of zeroes tells a reader nothing and dilutes the figures
// that do matter; an absent figure is itself legible as "none".
func buildHTMLTally(view htmlView, changes, edges int) []htmlTally {
	groups := view.TotalGroups + view.TotalEdgeGroups
	tally := []htmlTally{{view.TotalTargets, plural(view.TotalTargets, "target", "targets")}}
	for _, entry := range []htmlTally{
		{changes, plural(changes, "resource change", "resource changes")},
		{edges, plural(edges, "edge change", "edge changes")},
		{groups, plural(groups, "aggregate group", "aggregate groups")},
		{view.TotalEstimates, plural(view.TotalEstimates, "impact estimate", "impact estimates")},
	} {
		if entry.Count > 0 {
			tally = append(tally, entry)
		}
	}
	return tally
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
		if t.Baseline.Capture != nil {
			out.BaselineV3Warning = t.Baseline.Capture.V3Warning
		}
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

	// Catalog retrieval failure and compilation failure have to be visibly
	// marked, so error diagnostics are split out of the general list into
	// their own banner rather than being one row among many.
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
		for _, c := range displayedResourceChanges(t.NodeDiff.ResourceChanges) {
			out.Changes = append(out.Changes, changeParts(c))
		}
		for _, c := range t.NodeDiff.EdgeChanges {
			out.EdgeChanges = append(out.EdgeChanges, edgeParts(c))
		}
		for _, e := range t.NodeDiff.Exclusions {
			out.Exclusions = append(out.Exclusions, exclusionSummaryFull(e))
		}
	}
	return out
}

func buildHTMLEstimate(e model.ImpactEstimate) htmlEstimate {
	request := fmt.Sprintf("path=%s limit=%d timeout=%s", e.Request.Path, e.Request.Limit, e.Timeout)
	if e.Request.OrderBy != "" {
		request += " order_by=" + e.Request.OrderBy
	}
	// A failed estimate carries its status in a badge, so the line beside
	// it is the reason alone: "failed  failed: puppetdb returned 503"
	// says the same word twice. The text report has no badge, which is
	// why estimateCount keeps the prefix there.
	count := estimateCount(e)
	if e.Status != model.ImpactStatusCompleted {
		count = e.FailureReason
		if count == "" {
			count = "no reason reported"
		}
	}

	return htmlEstimate{
		Identity:  e.Identity.String(),
		Status:    string(e.Status),
		Count:     count,
		Certnames: strings.Join(e.Certnames, ", "),
		NodeLabel: plural(len(e.Certnames), "node", "nodes"),
		PQL:       e.PQL,
		Request:   request,
		Failed:    e.Status != model.ImpactStatusCompleted,
	}
}

// changeParts splits one resource-level change into its display parts.
// It calls the same formatValue and fileContentSummary the text report
// does, so a value is spelled identically in both formats however
// differently the two lay it out (see doc.go).
func changeParts(c model.ResourceChange) htmlChange {
	out := htmlChange{Identity: c.Identity.String(), Parameter: c.Parameter, Kind: string(c.Kind)}
	switch c.Kind {
	case model.ChangeResourceAdded:
		out.Sign, out.Class = "+", "add"
	case model.ChangeResourceRemoved:
		out.Sign, out.Class = "-", "remove"
	case model.ChangeParameterChanged:
		out.Sign, out.Class = "~", "change"
		if c.FileContent != nil {
			out.Note = fileContentSummary(*c.FileContent)
			return out
		}
		out.Before, out.After, out.HasValues = formatValue(c.Before), formatValue(c.After), true
	default:
		out.Sign, out.Class = "?", "change"
	}
	if c.Before != nil || c.After != nil {
		out.Before, out.After, out.HasValues = formatValue(c.Before), formatValue(c.After), true
	}
	if c.FileContent != nil {
		out.Note = fileContentSummary(*c.FileContent)
	}
	return out
}

func edgeParts(c model.EdgeChange) htmlEdge {
	if c.Kind == model.ChangeEdgeRemoved {
		return htmlEdge{Sign: "-", Class: "remove", Source: c.Edge.Source, Target: c.Edge.Target}
	}
	return htmlEdge{Sign: "+", Class: "add", Source: c.Edge.Source, Target: c.Edge.Target}
}

// groupKeyParts splits an aggregate group's key and evidence the same way
// changeParts splits a node change, so a group row and a target's change
// row read as the same kind of line.
func groupKeyParts(g model.AggregateGroup) htmlChange {
	out := htmlChange{Kind: string(g.Key.Kind), Parameter: g.Key.Parameter}
	if g.Key.Identity != nil {
		out.Identity = g.Key.Identity.String()
	}
	switch g.Key.Kind {
	case model.ChangeResourceAdded:
		out.Sign, out.Class = "+", "add"
	case model.ChangeResourceRemoved:
		out.Sign, out.Class = "-", "remove"
	default:
		out.Sign, out.Class = "~", "change"
	}
	if g.Before != nil || g.After != nil {
		out.Before, out.After, out.HasValues = formatValue(g.Before), formatValue(g.After), true
	}
	if g.FileContent != nil {
		out.Note = aggregateContentSummary(*g.FileContent)
	}
	return out
}

// edgeKeyParts renders an edge-kind aggregate key. model.AggregateChangeKey
// leaves Identity nil for these, which is why the two key shapes become
// separate view types here rather than one type that would have to
// nil-check on every row.
func edgeKeyParts(key model.AggregateChangeKey) htmlEdge {
	out := htmlEdge{Sign: "+", Class: "add"}
	if key.Kind == model.ChangeEdgeRemoved {
		out.Sign, out.Class = "-", "remove"
	}
	if key.Edge != nil {
		out.Source, out.Target = key.Edge.Source, key.Edge.Target
	}
	return out
}

func sourceProvenanceMap(p model.SourceProvenance) map[string]any {
	m := map[string]any{"source": string(p.Kind), "certname": p.Certname}
	addNonEmpty(m, "environment", p.Environment)
	addNonEmpty(m, "producer_timestamp", p.ProducerTimestamp)
	addNonEmpty(m, "catalog_identity", p.CatalogIdentity)
	addNonEmpty(m, "producer", p.Producer)
	if c := p.Capture; c != nil {
		addNonEmpty(m, "captured_requested_api", string(c.RequestedAPI))
		addNonEmpty(m, "captured_effective_api", string(c.EffectiveAPI))
		addNonEmpty(m, "captured_trusted_facts_source", string(c.TrustedFactsSource))
		addNonEmpty(m, "captured_fact_source", string(c.FactSource))
		if c.FellBackFromV4 {
			m["captured_fell_back_from_v4"] = true
		}
	}
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// outcomeClass maps an outcome to the CSS class that colors its badge.
// An unrecognized outcome gets the most severe styling, mirroring
// exitcode.ForOutcome's rule that an unknown classification is never
// presented as success.
//
// Both success outcomes, clean and differences_allowed, share the green
// `clean` class: they share exit 0, and a yellow badge on a run that
// succeeded would read as a warning. Yellow (`allowed`) is reserved for
// the advisory medium-risk indication, which is not an outcome.
func outcomeClass(o exitcode.Outcome) string {
	switch o {
	case exitcode.OutcomeClean, exitcode.OutcomeDifferencesAllowed:
		return "clean"
	case exitcode.OutcomePolicyDisallowedDifference:
		return "policy"
	case exitcode.OutcomeCompilationFailure:
		return "compile"
	default:
		return "operational"
	}
}

var htmlTemplate = template.Must(template.New("piace").Parse(htmlSource))
