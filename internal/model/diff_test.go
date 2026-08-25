package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestNodeDiff_JSONRoundTrip verifies the node diff schema (resource
// changes, edge changes, exclusion outcomes) marshals and unmarshals via
// encoding/json without field loss.
func TestNodeDiff_JSONRoundTrip(t *testing.T) {
	original := NodeDiff{
		Certname: "web-01.example.test",
		ResourceChanges: []ResourceChange{
			{
				Kind:     ChangeResourceAdded,
				Identity: ResourceIdentity{Type: "Notify", Title: "new"},
			},
			{
				Kind:      ChangeParameterChanged,
				Identity:  ResourceIdentity{Type: "File", Title: "/etc/motd"},
				Parameter: "content",
				Before:    "old",
				After:     "new",
				FileContent: &FileContentEvidence{
					State:          FileContentChanged,
					EvidenceSource: FileContentEvidenceInline,
					Algorithm:      "sha256",
					BeforeDigest:   "aaaa",
					AfterDigest:    "bbbb",
				},
			},
		},
		EdgeChanges: []EdgeChange{
			{Kind: ChangeEdgeAdded, Edge: Edge{Source: "Notify[new]", Target: "File[/etc/motd]"}},
		},
		Exclusions: []ExclusionOutcome{
			{
				Rule:                ExclusionRuleRef{Type: "File", Title: "/var/cache/*"},
				SuppressedResources: 1,
			},
		},
		HasDifference: true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded NodeDiff
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Certname != original.Certname {
		t.Errorf("Certname = %q", decoded.Certname)
	}
	if len(decoded.ResourceChanges) != 2 {
		t.Fatalf("ResourceChanges = %+v", decoded.ResourceChanges)
	}
	if decoded.ResourceChanges[1].FileContent == nil ||
		decoded.ResourceChanges[1].FileContent.State != FileContentChanged {
		t.Errorf("ResourceChanges[1].FileContent = %+v", decoded.ResourceChanges[1].FileContent)
	}
	if len(decoded.EdgeChanges) != 1 || decoded.EdgeChanges[0].Kind != ChangeEdgeAdded {
		t.Errorf("EdgeChanges = %+v", decoded.EdgeChanges)
	}
	if len(decoded.Exclusions) != 1 || decoded.Exclusions[0].SuppressedResources != 1 {
		t.Errorf("Exclusions = %+v", decoded.Exclusions)
	}
	if !decoded.HasDifference {
		t.Errorf("HasDifference = false, want true")
	}
}

// TestAggregateDiff_JSONRoundTrip verifies the aggregate diff schema
// (groups, certnames, node-change references) marshals and unmarshals via
// encoding/json without field loss.
func TestAggregateDiff_JSONRoundTrip(t *testing.T) {
	original := AggregateDiff{
		Groups: []AggregateGroup{
			{
				Key: AggregateChangeKey{
					Kind:      ChangeParameterChanged,
					Identity:  &ResourceIdentity{Type: "File", Title: "/etc/motd"},
					Parameter: "content",
				},
				Before:    "old",
				After:     "new",
				Certnames: []string{"web-01.example.test", "web-02.example.test"},
				NodeChangeRefs: []NodeChangeRef{
					{Certname: "web-01.example.test", Index: 1},
					{Certname: "web-02.example.test", Index: 0},
				},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded AggregateDiff
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.Groups) != 1 {
		t.Fatalf("Groups = %+v", decoded.Groups)
	}
	g := decoded.Groups[0]
	// AggregateChangeKey carries pointers (exactly one of Identity/Edge
	// is set), so compare pointed-to values rather than addresses.
	if !reflect.DeepEqual(g.Key, original.Groups[0].Key) {
		t.Errorf("Key = %+v, want %+v", g.Key, original.Groups[0].Key)
	}
	if len(g.Certnames) != 2 || len(g.NodeChangeRefs) != 2 {
		t.Errorf("Certnames/NodeChangeRefs mismatch: %+v", g)
	}
}
