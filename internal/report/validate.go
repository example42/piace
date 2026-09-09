package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/model"
)

// Validate checks that a decoded result document describes one complete,
// internally consistent comparison, and refuses it otherwise.
//
// Decoding a document and reading a supported schema_version establishes
// only that its syntax is right and that this binary knows the shape. It
// does not establish that the document says anything coherent:
// `{"schema_version":3}` decodes cleanly and describes no comparison at
// all, an outcome of "clean" can sit above a target reporting
// differences, and an aggregate group can name a target the document
// does not contain. Every consumer downstream of a stored document, the
// renderers and above all `explain`, which transmits what it reads to an
// inference service, would then be reasoning over, or disclosing, a
// document nobody produced.
//
// What it checks, in order:
//
//   - required invocation metadata, and a parseable timestamp;
//   - per target: a certname, unique across the document, a known
//     outcome, and a node diff whose certname and difference flag agree
//     with the target that carries it;
//   - per change: a known kind, an identity or edge appropriate to that
//     kind, and a parameter name only where one belongs;
//   - per aggregate group: a well-formed key, references that point at
//     changes that exist and are of the group's own kind, and a certname
//     list that matches those references;
//   - outcome consistency: the document's own outcome and exit code must
//     be what its targets and diagnostics reduce to;
//   - disclosure: no published value may carry a Sensitive wrapper or a
//     File content-bearing parameter's value.
//
// Partial comparisons stay valid. A target whose retrieval or
// compilation failed carries no node diff, and that absence is expected
// rather than suspicious; what is required is that the failure was
// actually recorded, so a missing structure cannot be presented as a
// failure that nothing reports.
//
// Every problem found is reported together: a document being rewritten
// by hand, which is the case this exists for, is better served by one
// list than by a sequence of single complaints.
func Validate(r model.Result) error {
	v := &validation{}

	if r.Invocation.ToolVersion == "" {
		v.addf("invocation.tool_version is missing")
	}
	if _, err := time.Parse(time.RFC3339, r.Invocation.TimestampUTC); err != nil {
		v.addf("invocation.timestamp_utc %q is not an RFC 3339 timestamp", r.Invocation.TimestampUTC)
	}
	if len(r.Targets) == 0 {
		v.addf("the document records no targets; a comparison always reports the targets it selected")
	}

	seen := make(map[string]bool, len(r.Targets))
	for i, target := range r.Targets {
		where := fmt.Sprintf("targets[%d]", i)
		switch {
		case target.Certname == "":
			v.addf("%s: certname is missing", where)
		case seen[target.Certname]:
			v.addf("%s: duplicate certname %q", where, target.Certname)
		}
		seen[target.Certname] = true
		v.target(where, target)
	}

	v.aggregate(r)
	v.outcome(r)
	v.disclosure(r)

	return v.err()
}

// validation accumulates every problem found in one document.
type validation struct{ problems []string }

func (v *validation) addf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func (v *validation) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	sort.Strings(v.problems)
	return fmt.Errorf("the result document is not a consistent comparison:\n  %s", strings.Join(v.problems, "\n  "))
}

// target checks one target's outcome, its node diff's agreement with it,
// and every change the diff carries.
func (v *validation) target(where string, t model.TargetResult) {
	if !knownOutcome(t.Outcome) {
		v.addf("%s: unknown outcome %q", where, t.Outcome)
	}
	if t.Baseline != nil {
		v.baselineCapture(where+".baseline.capture", t.Baseline)
	}

	switch t.Outcome {
	case exitcode.OutcomeCompilationFailure, exitcode.OutcomeOperationalError:
		// A failed target legitimately has no node diff. What it cannot
		// have is no record of the failure: an absent structure is not a
		// reported failure, and treating it as one would let a document
		// that simply omits a target's results pass as a document that
		// explains why they are missing.
		if !hasErrorDiagnostic(t.Diagnostics) {
			v.addf("%s: outcome %q with no error diagnostic recording it", where, t.Outcome)
		}
		return
	}

	if t.NodeDiff == nil {
		v.addf("%s: outcome %q without a node diff", where, t.Outcome)
		return
	}
	if t.NodeDiff.Certname != t.Certname {
		v.addf("%s: node diff certname %q does not match the target %q", where, t.NodeDiff.Certname, t.Certname)
	}
	changes := len(t.NodeDiff.ResourceChanges) + len(t.NodeDiff.EdgeChanges)
	if t.NodeDiff.HasDifference != (changes > 0) {
		v.addf("%s: has_difference is %v with %d recorded changes", where, t.NodeDiff.HasDifference, changes)
	}
	if t.Outcome == exitcode.OutcomeClean && t.NodeDiff.HasDifference {
		v.addf("%s: outcome %q with recorded differences", where, exitcode.OutcomeClean)
	}
	if (t.Outcome == exitcode.OutcomeDifferencesAllowed || t.Outcome == exitcode.OutcomePolicyDisallowedDifference) &&
		!t.NodeDiff.HasDifference {
		v.addf("%s: outcome %q with no recorded difference", where, t.Outcome)
	}

	for j, change := range t.NodeDiff.ResourceChanges {
		v.resourceChange(fmt.Sprintf("%s.resource_changes[%d]", where, j), change)
	}
	for j, change := range t.NodeDiff.EdgeChanges {
		at := fmt.Sprintf("%s.edge_changes[%d]", where, j)
		if change.Kind != model.ChangeEdgeAdded && change.Kind != model.ChangeEdgeRemoved {
			v.addf("%s: kind %q is not an edge change", at, change.Kind)
		}
		if change.Edge.Source == "" || change.Edge.Target == "" {
			v.addf("%s: edge has an incomplete endpoint", at)
		}
	}
}

func (v *validation) resourceChange(where string, c model.ResourceChange) {
	switch c.Kind {
	case model.ChangeResourceAdded, model.ChangeResourceRemoved:
		if c.Parameter != "" {
			v.addf("%s: kind %q carries a parameter name", where, c.Kind)
		}
	case model.ChangeParameterChanged:
		if c.Parameter == "" {
			v.addf("%s: kind %q carries no parameter name", where, c.Kind)
		}
	default:
		v.addf("%s: kind %q is not a resource change", where, c.Kind)
	}
	if c.Identity.Type == "" || c.Identity.Title == "" {
		v.addf("%s: identity is incomplete", where)
	}
	if c.FileContent != nil {
		v.fileContent(where+".file_content", c.FileContent.State, c.FileContent.EvidenceSource,
			c.FileContent.Redacted, c.FileContent.Algorithm, c.FileContent.BeforeDigest, c.FileContent.AfterDigest)
	}
}

// fileContent checks content evidence for the combinations that cannot
// be true at once.
//
// Redaction here is not omission, and the difference is the point: a
// redacted side keeps its entry, carrying the marker in place of the
// digest and no algorithm, so a reader can tell "this side exists and
// its evidence is withheld" from "this side has no evidence". A document
// claiming redaction while carrying a real digest is claiming both.
func (v *validation) fileContent(where string, state model.FileContentState, source model.FileContentEvidenceSource,
	redacted bool, algorithm, before, after string) {
	if !knownFileContentState(state) {
		v.addf("%s: unknown state %q", where, state)
	}
	if source != "" && !knownEvidenceSource(source) {
		v.addf("%s: unknown evidence_source %q", where, source)
	}
	marker := func(digest string) bool { return digest == model.RedactedValue }
	if redacted {
		if algorithm != "" {
			v.addf("%s: is redacted and still names a digest algorithm", where)
		}
		for label, digest := range map[string]string{"before_digest": before, "after_digest": after} {
			if digest != "" && !marker(digest) {
				v.addf("%s: is redacted and still carries a %s", where, label)
			}
		}
		return
	}
	for label, digest := range map[string]string{"before_digest": before, "after_digest": after} {
		if marker(digest) {
			v.addf("%s: carries a redaction marker in %s without recording the redaction", where, label)
		}
	}
	if (before != "" || after != "") && algorithm == "" {
		v.addf("%s: carries a digest with no algorithm", where)
	}
}

// aggregate checks that every group is well-formed and that its
// references point at changes the document actually contains, of the
// group's own kind. A reference that does not is a group describing
// something else's evidence.
func (v *validation) aggregate(r model.Result) {
	byCertname := make(map[string]model.TargetResult, len(r.Targets))
	for _, t := range r.Targets {
		byCertname[t.Certname] = t
	}

	for i, g := range r.Aggregate.Groups {
		where := fmt.Sprintf("aggregate.groups[%d]", i)
		edgeKind := g.Key.Kind == model.ChangeEdgeAdded || g.Key.Kind == model.ChangeEdgeRemoved
		resourceKind := g.Key.Kind == model.ChangeResourceAdded || g.Key.Kind == model.ChangeResourceRemoved ||
			g.Key.Kind == model.ChangeParameterChanged
		switch {
		case !edgeKind && !resourceKind:
			v.addf("%s: unknown kind %q", where, g.Key.Kind)
		case edgeKind && (g.Key.Edge == nil || g.Key.Identity != nil):
			v.addf("%s: an edge group must key on an edge and not on a resource identity", where)
		case resourceKind && (g.Key.Identity == nil || g.Key.Edge != nil):
			v.addf("%s: a resource group must key on a resource identity and not on an edge", where)
		}
		if g.Key.Parameter != "" && g.Key.Kind != model.ChangeParameterChanged {
			v.addf("%s: kind %q carries a parameter name", where, g.Key.Kind)
		}
		if len(g.NodeChangeRefs) == 0 {
			v.addf("%s: references no target change", where)
		}
		if g.FileContent != nil {
			v.fileContent(where+".file_content", g.FileContent.State, g.FileContent.EvidenceSource,
				g.FileContent.Redacted, "", "", "")
		}

		referenced := make(map[string]bool, len(g.NodeChangeRefs))
		for j, ref := range g.NodeChangeRefs {
			at := fmt.Sprintf("%s.node_change_refs[%d]", where, j)
			target, ok := byCertname[ref.Certname]
			if !ok {
				v.addf("%s: names target %q, which the document does not contain", at, ref.Certname)
				continue
			}
			referenced[ref.Certname] = true
			if target.NodeDiff == nil {
				v.addf("%s: names target %q, which recorded no node diff", at, ref.Certname)
				continue
			}
			kind, ok := referencedKind(*target.NodeDiff, g.Key.Kind, ref.Index)
			if !ok {
				v.addf("%s: index %d is out of range for target %q", at, ref.Index, ref.Certname)
				continue
			}
			if kind != g.Key.Kind {
				v.addf("%s: refers to a %q change from a %q group", at, kind, g.Key.Kind)
			}
		}

		for _, certname := range g.Certnames {
			if !referenced[certname] {
				v.addf("%s: lists certname %q with no matching change reference", where, certname)
			}
		}
		for certname := range referenced {
			if !contains(g.Certnames, certname) {
				v.addf("%s: references target %q without listing its certname", where, certname)
			}
		}
	}
}

// referencedKind returns the kind of the change a reference points at,
// choosing the slice the group's kind implies, and reports whether the
// index exists at all.
func referencedKind(diff model.NodeDiff, groupKind model.ChangeKind, index int) (model.ChangeKind, bool) {
	if index < 0 {
		return "", false
	}
	if groupKind == model.ChangeEdgeAdded || groupKind == model.ChangeEdgeRemoved {
		if index >= len(diff.EdgeChanges) {
			return "", false
		}
		return diff.EdgeChanges[index].Kind, true
	}
	if index >= len(diff.ResourceChanges) {
		return "", false
	}
	return diff.ResourceChanges[index].Kind, true
}

// outcome recomputes the document's outcome from its own targets and
// diagnostics. A document whose stated outcome is not what its contents
// reduce to is contradictory, and is rejected rather than quietly
// repaired: repairing it would mean deciding which half of the
// contradiction to believe, and a reader has no basis for that.
func (v *validation) outcome(r model.Result) {
	if !knownOutcome(r.Outcome) {
		v.addf("unknown outcome %q", r.Outcome)
		return
	}
	// The whole document is copied and its two computed fields recomputed,
	// rather than a Result assembled from the inputs Finalize reads today:
	// that set is Finalize's business, and a validator that duplicated it
	// would start rejecting valid documents, silently and with no compile
	// error, the day Finalize folded in one more field.
	recomputed := r
	recomputed.Outcome, recomputed.ExitCode = "", 0
	recomputed.Finalize()
	if recomputed.Outcome != r.Outcome {
		v.addf("outcome %q, but its targets and diagnostics reduce to %q", r.Outcome, recomputed.Outcome)
	}
	if r.ExitCode != int(exitcode.ForOutcome(r.Outcome)) {
		v.addf("exit_code %d does not match outcome %q (%d)", r.ExitCode, r.Outcome, exitcode.ForOutcome(r.Outcome))
	}
}

// disclosure refuses a document carrying evidence that redaction would
// have removed. A stored report is an input like any other: a forged
// Sensitive wrapper or a File `content` value written into a document by
// hand would otherwise reach a renderer, and reach an inference service
// through `explain`, on the strength of having arrived in a file rather
// than from a catalog.
func (v *validation) disclosure(r model.Result) {
	for i, t := range r.Targets {
		if t.NodeDiff == nil {
			continue
		}
		for j, c := range t.NodeDiff.ResourceChanges {
			where := fmt.Sprintf("targets[%d].node_diff.resource_changes[%d]", i, j)
			v.publishedValue(where+".before", c.Identity.Type, c.Parameter, c.Before)
			v.publishedValue(where+".after", c.Identity.Type, c.Parameter, c.After)
		}
	}
	for i, g := range r.Aggregate.Groups {
		where := fmt.Sprintf("aggregate.groups[%d]", i)
		resourceType := ""
		if g.Key.Identity != nil {
			resourceType = g.Key.Identity.Type
		}
		v.publishedValue(where+".before", resourceType, g.Key.Parameter, g.Before)
		v.publishedValue(where+".after", resourceType, g.Key.Parameter, g.After)
	}
}

// publishedValue checks one published before/after projection.
//
// A resource-added or resource-removed entry publishes a parameter map
// rather than one value, so its File content-bearing entries are checked
// individually; parameter is empty for those entries, which is why the
// map case cannot be folded into the scalar one.
func (v *validation) publishedValue(where, resourceType, parameter string, value any) {
	if value == nil {
		return
	}
	if model.ContainsSensitive(value) {
		v.addf("%s: carries an unredacted Sensitive wrapper", where)
	}
	if resourceType != model.FileResourceType {
		return
	}
	if parameter != "" {
		if model.FileContentBearingParameter(parameter) && value != model.RedactedValue {
			v.addf("%s: publishes the value of the File %s parameter", where, parameter)
		}
		return
	}
	parameters, ok := value.(map[string]any)
	if !ok {
		return
	}
	for _, name := range model.FileContentBearingParameters() {
		if held, ok := parameters[name]; ok && held != model.RedactedValue {
			v.addf("%s: publishes the value of the File %s parameter", where, name)
		}
	}
}

// baselineCapture checks that a file baseline's capture provenance says
// something coherent about how it was obtained. The warning is derived
// from the effective API on the way in, so a document claiming a v3
// capture without it, or a v4 capture carrying it, is one whose trust
// semantics have been rewritten somewhere between the snapshot and the
// reader.
func (v *validation) baselineCapture(where string, p *model.SourceProvenance) {
	c := p.Capture
	if c == nil {
		return
	}
	if p.Kind != model.SourceKindFile {
		v.addf("%s: a %s baseline carries capture provenance, which only a snapshot has", where, p.Kind)
	}
	if !knownCatalogAPI(c.RequestedAPI) || !knownCatalogAPI(c.EffectiveAPI) {
		v.addf("%s: unknown catalog API pair (%q requested, %q effective)", where, c.RequestedAPI, c.EffectiveAPI)
		return
	}
	if c.FellBackFromV4 && (c.RequestedAPI != config.CatalogAPIv4 || c.EffectiveAPI != config.CatalogAPIv3) {
		v.addf("%s: records a v4-to-v3 fallback from %q to %q", where, c.RequestedAPI, c.EffectiveAPI)
	}
	if !c.FellBackFromV4 && c.RequestedAPI != c.EffectiveAPI {
		v.addf("%s: %q was requested and %q answered, with no fallback recorded", where, c.RequestedAPI, c.EffectiveAPI)
	}
	if c.EffectiveAPI == config.CatalogAPIv3 && c.V3Warning != model.V3TrustedFactWarning {
		v.addf("%s: a v3 capture without its trusted-fact warning", where)
	}
	if c.EffectiveAPI == config.CatalogAPIv4 && c.V3Warning != "" {
		v.addf("%s: a v4 capture carrying a v3 warning", where)
	}
	if c.EffectiveAPI == config.CatalogAPIv3 && c.TrustedFactsSource != "" {
		v.addf("%s: a v3 capture naming a trusted-fact source, which v3 has no request field for", where)
	}
}

func knownCatalogAPI(api config.CatalogAPI) bool {
	return api == config.CatalogAPIv3 || api == config.CatalogAPIv4
}

func hasErrorDiagnostic(diagnostics []model.Diagnostic) bool {
	for _, d := range diagnostics {
		if d.Severity == model.SeverityError {
			return true
		}
	}
	return false
}

func knownOutcome(o exitcode.Outcome) bool {
	for _, known := range exitcode.Precedence {
		if o == known {
			return true
		}
	}
	return false
}

func knownFileContentState(s model.FileContentState) bool {
	switch s {
	case model.FileContentAdded, model.FileContentRemoved, model.FileContentUnchanged,
		model.FileContentChanged, model.FileContentReferenceChanged, model.FileContentIndeterminate,
		model.FileContentNotManaged:
		return true
	}
	return false
}

func knownEvidenceSource(s model.FileContentEvidenceSource) bool {
	switch s {
	case model.FileContentEvidenceInline, model.FileContentEvidenceCompiledChecksum,
		model.FileContentEvidenceCompilerRetrieval, model.FileContentEvidenceStaticMetadata,
		model.FileContentEvidenceCaptured, model.FileContentEvidenceMixed:
		return true
	}
	return false
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
