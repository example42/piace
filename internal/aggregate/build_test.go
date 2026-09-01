package aggregate

import (
	"encoding/json"
	"testing"

	"github.com/example42/piace/internal/model"
)

func identity(resourceType, title string) model.ResourceIdentity {
	return model.ResourceIdentity{Type: resourceType, Title: title}
}

func paramChange(resourceType, title, parameter, fingerprint string, before, after any) model.ResourceChange {
	return model.ResourceChange{
		Kind:        model.ChangeParameterChanged,
		Identity:    identity(resourceType, title),
		Parameter:   parameter,
		Before:      before,
		After:       after,
		Fingerprint: fingerprint,
	}
}

func addedChange(resourceType, title, fingerprint string) model.ResourceChange {
	return model.ResourceChange{
		Kind:        model.ChangeResourceAdded,
		Identity:    identity(resourceType, title),
		Fingerprint: fingerprint,
	}
}

func edgeChange(kind model.ChangeKind, source, target string) model.EdgeChange {
	return model.EdgeChange{Kind: kind, Edge: model.Edge{Source: source, Target: target}}
}

func TestBuild_GroupsEquivalentChangesAcrossTargets(t *testing.T) {
	diffs := []model.NodeDiff{
		{
			Certname:        "web-01",
			ResourceChanges: []model.ResourceChange{paramChange("Package", "nginx", "ensure", "fp-a", "1.0", "2.0")},
			HasDifference:   true,
		},
		{
			Certname:        "web-02",
			ResourceChanges: []model.ResourceChange{paramChange("Package", "nginx", "ensure", "fp-a", "1.0", "2.0")},
			HasDifference:   true,
		},
	}

	got := Build(diffs)
	if len(got.Groups) != 1 {
		t.Fatalf("got %d groups, want 1: %+v", len(got.Groups), got.Groups)
	}
	g := got.Groups[0]
	if g.Key.Kind != model.ChangeParameterChanged || g.Key.Parameter != "ensure" {
		t.Errorf("key = %+v", g.Key)
	}
	if g.Key.Identity == nil || *g.Key.Identity != identity("Package", "nginx") {
		t.Errorf("identity = %v", g.Key.Identity)
	}
	if g.Key.Edge != nil {
		t.Errorf("a resource group must not carry an edge: %+v", g.Key.Edge)
	}
	if len(g.Certnames) != 2 || g.Certnames[0] != "web-01" || g.Certnames[1] != "web-02" {
		t.Errorf("certnames = %v, want sorted [web-01 web-02]", g.Certnames)
	}
	if len(g.NodeChangeRefs) != 2 {
		t.Fatalf("refs = %+v, want 2", g.NodeChangeRefs)
	}
	if g.Before != "1.0" || g.After != "2.0" {
		t.Errorf("before/after = %v/%v", g.Before, g.After)
	}
}

// Requirement 7.1: equivalence requires equal evidence. Two targets whose
// same parameter changed to different values are two groups.
func TestBuild_DistinctFingerprintsDoNotMerge(t *testing.T) {
	diffs := []model.NodeDiff{
		{Certname: "web-01", ResourceChanges: []model.ResourceChange{
			paramChange("Package", "nginx", "ensure", "fp-a", "1.0", "2.0")}},
		{Certname: "web-02", ResourceChanges: []model.ResourceChange{
			paramChange("Package", "nginx", "ensure", "fp-b", "1.0", "3.0")}},
	}

	got := Build(diffs)
	if len(got.Groups) != 2 {
		t.Fatalf("got %d groups, want 2 (different evidence must not merge): %+v", len(got.Groups), got.Groups)
	}
	for _, g := range got.Groups {
		if len(g.Certnames) != 1 {
			t.Errorf("group %+v should hold exactly one certname", g)
		}
	}
}

// The same anti-merge property with the values already redacted — the
// case the fingerprint exists for.
func TestBuild_DistinctRedactedChangesDoNotMerge(t *testing.T) {
	diffs := []model.NodeDiff{
		{Certname: "web-01", ResourceChanges: []model.ResourceChange{
			paramChange("Exec", "login", "password", "fp-a", model.RedactedValue, model.RedactedValue)}},
		{Certname: "web-02", ResourceChanges: []model.ResourceChange{
			paramChange("Exec", "login", "password", "fp-b", model.RedactedValue, model.RedactedValue)}},
	}

	got := Build(diffs)
	if len(got.Groups) != 2 {
		t.Fatalf("two distinct sensitive changes merged into %d group(s): %+v", len(got.Groups), got.Groups)
	}
}

// internal/diff's contract: an empty fingerprint means "cannot group".
func TestBuild_UnfingerprintableChangesEachGetTheirOwnGroup(t *testing.T) {
	diffs := []model.NodeDiff{
		{Certname: "web-01", ResourceChanges: []model.ResourceChange{
			paramChange("Package", "nginx", "ensure", "", "1.0", "2.0")}},
		{Certname: "web-02", ResourceChanges: []model.ResourceChange{
			paramChange("Package", "nginx", "ensure", "", "1.0", "2.0")}},
	}

	got := Build(diffs)
	if len(got.Groups) != 2 {
		t.Fatalf("unfingerprintable changes merged into %d group(s): %+v", len(got.Groups), got.Groups)
	}
}

// Requirement 7.4: edge changes survive aggregation as a distinct kind.
func TestBuild_EdgeGroupsUseEdgeKeyAndGroupWithoutFingerprint(t *testing.T) {
	diffs := []model.NodeDiff{
		{Certname: "web-01", EdgeChanges: []model.EdgeChange{
			edgeChange(model.ChangeEdgeAdded, "Package[nginx]", "Service[nginx]")}},
		{Certname: "web-02", EdgeChanges: []model.EdgeChange{
			edgeChange(model.ChangeEdgeAdded, "Package[nginx]", "Service[nginx]")}},
	}

	got := Build(diffs)
	if len(got.Groups) != 1 {
		t.Fatalf("got %d groups, want 1: %+v", len(got.Groups), got.Groups)
	}
	g := got.Groups[0]
	if g.Key.Kind != model.ChangeEdgeAdded {
		t.Errorf("kind = %s", g.Key.Kind)
	}
	if g.Key.Edge == nil || g.Key.Edge.Source != "Package[nginx]" || g.Key.Edge.Target != "Service[nginx]" {
		t.Errorf("edge = %v", g.Key.Edge)
	}
	if g.Key.Identity != nil {
		t.Errorf("an edge group must not carry a resource identity: %+v", g.Key.Identity)
	}
	if g.Before != nil || g.After != nil {
		t.Errorf("an edge group carries no value projection: %v/%v", g.Before, g.After)
	}
	if len(g.Certnames) != 2 {
		t.Errorf("certnames = %v", g.Certnames)
	}
}

// Edge direction is significant, so the two orientations never merge.
func TestBuild_EdgeDirectionSeparatesGroups(t *testing.T) {
	diffs := []model.NodeDiff{
		{Certname: "web-01", EdgeChanges: []model.EdgeChange{
			edgeChange(model.ChangeEdgeAdded, "A[x]", "B[y]")}},
		{Certname: "web-02", EdgeChanges: []model.EdgeChange{
			edgeChange(model.ChangeEdgeAdded, "B[y]", "A[x]")}},
	}
	if got := Build(diffs); len(got.Groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(got.Groups), got.Groups)
	}
}

// An added and a removed edge with the same endpoints are distinct kinds.
func TestBuild_EdgeAddedAndRemovedAreDistinctGroups(t *testing.T) {
	diffs := []model.NodeDiff{
		{Certname: "web-01", EdgeChanges: []model.EdgeChange{
			edgeChange(model.ChangeEdgeAdded, "A[x]", "B[y]")}},
		{Certname: "web-02", EdgeChanges: []model.EdgeChange{
			edgeChange(model.ChangeEdgeRemoved, "A[x]", "B[y]")}},
	}
	if got := Build(diffs); len(got.Groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(got.Groups), got.Groups)
	}
}

// Requirement 7.3 / NodeChangeRef.Index: the index is a position within
// the referenced target's own change slice.
func TestBuild_NodeChangeRefsIndexIntoTheRightSlice(t *testing.T) {
	diffs := []model.NodeDiff{
		{
			Certname: "web-01",
			ResourceChanges: []model.ResourceChange{
				addedChange("Notify", "other", "fp-other"),
				paramChange("Package", "nginx", "ensure", "fp-a", "1.0", "2.0"),
			},
			EdgeChanges: []model.EdgeChange{
				edgeChange(model.ChangeEdgeAdded, "A[x]", "B[y]"),
			},
		},
	}

	got := Build(diffs)
	for _, g := range got.Groups {
		ref := g.NodeChangeRefs[0]
		switch g.Key.Kind {
		case model.ChangeParameterChanged:
			if ref.Index != 1 {
				t.Errorf("parameter group indexes ResourceChanges[%d], want 1", ref.Index)
			}
		case model.ChangeResourceAdded:
			if ref.Index != 0 {
				t.Errorf("added group indexes ResourceChanges[%d], want 0", ref.Index)
			}
		case model.ChangeEdgeAdded:
			if ref.Index != 0 {
				t.Errorf("edge group indexes EdgeChanges[%d], want 0", ref.Index)
			}
		}
		if ref.Certname != "web-01" {
			t.Errorf("certname = %q", ref.Certname)
		}
	}
}

// Groups are sorted by kind and canonical identity.
func TestBuild_GroupsSortedByKindThenIdentity(t *testing.T) {
	diffs := []model.NodeDiff{{
		Certname: "web-01",
		ResourceChanges: []model.ResourceChange{
			paramChange("Package", "zzz", "ensure", "fp1", "a", "b"),
			addedChange("Service", "aaa", "fp2"),
			addedChange("File", "/etc/z", "fp3"),
			{Kind: model.ChangeResourceRemoved, Identity: identity("File", "/etc/a"), Fingerprint: "fp4"},
		},
		EdgeChanges: []model.EdgeChange{
			edgeChange(model.ChangeEdgeRemoved, "Z[z]", "Y[y]"),
			edgeChange(model.ChangeEdgeAdded, "B[b]", "C[c]"),
			edgeChange(model.ChangeEdgeAdded, "A[a]", "C[c]"),
		},
	}}

	got := Build(diffs)
	want := []string{
		"resource_added File[/etc/z]",
		"resource_added Service[aaa]",
		"resource_removed File[/etc/a]",
		"parameter_changed Package[zzz]",
		"edge_added A[a]->C[c]",
		"edge_added B[b]->C[c]",
		"edge_removed Z[z]->Y[y]",
	}
	if len(got.Groups) != len(want) {
		t.Fatalf("got %d groups, want %d: %+v", len(got.Groups), len(want), got.Groups)
	}
	for i, w := range want {
		var label string
		if g := got.Groups[i]; g.Key.Edge != nil {
			label = string(g.Key.Kind) + " " + g.Key.Edge.Source + "->" + g.Key.Edge.Target
		} else {
			label = string(got.Groups[i].Key.Kind) + " " + got.Groups[i].Key.Identity.String()
		}
		if label != w {
			t.Errorf("group %d = %q, want %q", i, label, w)
		}
	}
}

// The fingerprint tiebreaker: two groups with an identical public key
// must still have a defined relative order, or the report is not
// byte-identical across runs.
func TestBuild_IsByteIdenticalAcrossRuns(t *testing.T) {
	diffs := []model.NodeDiff{
		{Certname: "web-03", ResourceChanges: []model.ResourceChange{
			paramChange("Package", "nginx", "ensure", "fp-c", "1.0", "4.0"),
			addedChange("Notify", "x", "fp-n"),
		}, EdgeChanges: []model.EdgeChange{edgeChange(model.ChangeEdgeAdded, "A[a]", "B[b]")}},
		{Certname: "web-01", ResourceChanges: []model.ResourceChange{
			paramChange("Package", "nginx", "ensure", "fp-a", "1.0", "2.0"),
			paramChange("Package", "nginx", "ensure", "", "1.0", "9.0"),
			addedChange("Notify", "x", "fp-n"),
		}, EdgeChanges: []model.EdgeChange{edgeChange(model.ChangeEdgeAdded, "A[a]", "B[b]")}},
		{Certname: "web-02", ResourceChanges: []model.ResourceChange{
			paramChange("Package", "nginx", "ensure", "fp-b", "1.0", "3.0"),
			paramChange("Package", "nginx", "ensure", "", "1.0", "9.0"),
		}},
	}

	var first string
	for i := 0; i < 50; i++ {
		encoded, err := json.Marshal(Build(diffs))
		if err != nil {
			t.Fatalf("marshaling aggregate: %v", err)
		}
		if i == 0 {
			first = string(encoded)
			continue
		}
		if string(encoded) != first {
			t.Fatalf("run %d differs:\n first: %s\n  this: %s", i, first, encoded)
		}
	}
}

// A group's certname list holds each contributing target once, but every
// contributing change still gets a reference (requirement 7.3).
func TestBuild_DuplicateChangeInOneTargetKeepsBothRefsButOneCertname(t *testing.T) {
	diffs := []model.NodeDiff{{
		Certname: "web-01",
		ResourceChanges: []model.ResourceChange{
			paramChange("Package", "nginx", "ensure", "fp-a", "1.0", "2.0"),
			paramChange("Package", "nginx", "ensure", "fp-a", "1.0", "2.0"),
		},
	}}

	got := Build(diffs)
	if len(got.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(got.Groups))
	}
	g := got.Groups[0]
	if len(g.Certnames) != 1 {
		t.Errorf("certnames = %v, want one entry", g.Certnames)
	}
	if len(g.NodeChangeRefs) != 2 {
		t.Errorf("refs = %+v, want both changes referenced", g.NodeChangeRefs)
	}
}

func TestBuild_NoDiffsProducesEmptyAggregate(t *testing.T) {
	if got := Build(nil); len(got.Groups) != 0 {
		t.Errorf("got %+v, want no groups", got.Groups)
	}
	clean := []model.NodeDiff{{Certname: "web-01"}, {Certname: "web-02"}}
	if got := Build(clean); len(got.Groups) != 0 {
		t.Errorf("got %+v, want no groups", got.Groups)
	}
}

// Group order must be a property of the changes themselves, not of the
// order targets happen to appear in. This is what the fingerprint sort
// tiebreaker is load-bearing for: two groups sharing a public key are
// otherwise emitted in first-encounter order, which target-file order
// determines. A byte-identical-across-runs test cannot catch that,
// because the group slice is built in deterministic encounter order —
// only reordering the input exposes it.
func TestBuild_GroupOrderIsIndependentOfTargetOrder(t *testing.T) {
	a := model.NodeDiff{Certname: "web-01", ResourceChanges: []model.ResourceChange{
		paramChange("Package", "nginx", "ensure", "fp-aaa", "1.0", "2.0"),
	}}
	b := model.NodeDiff{Certname: "web-02", ResourceChanges: []model.ResourceChange{
		paramChange("Package", "nginx", "ensure", "fp-bbb", "1.0", "3.0"),
	}}
	c := model.NodeDiff{Certname: "web-03", ResourceChanges: []model.ResourceChange{
		paramChange("Package", "nginx", "ensure", "fp-ccc", "1.0", "4.0"),
	}}

	forward, err := json.Marshal(Build([]model.NodeDiff{a, b, c}))
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	reversed, err := json.Marshal(Build([]model.NodeDiff{c, b, a}))
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	if string(forward) != string(reversed) {
		t.Errorf("group order depends on target order:\n forward: %s\nreversed: %s", forward, reversed)
	}
}

// The same invariance for groups that differ only by an absent
// fingerprint, where the per-change sequence tiebreaker orders them.
func TestBuild_UnfingerprintableGroupCountIsIndependentOfTargetOrder(t *testing.T) {
	a := model.NodeDiff{Certname: "web-01", ResourceChanges: []model.ResourceChange{
		paramChange("Package", "nginx", "ensure", "", "1.0", "2.0"),
	}}
	b := model.NodeDiff{Certname: "web-02", ResourceChanges: []model.ResourceChange{
		paramChange("Package", "nginx", "ensure", "", "1.0", "3.0"),
	}}

	forward := Build([]model.NodeDiff{a, b})
	reversed := Build([]model.NodeDiff{b, a})
	if len(forward.Groups) != 2 || len(reversed.Groups) != 2 {
		t.Fatalf("group counts = %d/%d, want 2 each", len(forward.Groups), len(reversed.Groups))
	}
}
