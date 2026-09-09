package filecontent

import (
	"context"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

// These cases come from the first live run of PIACE against an OpenVox
// 8.15.2 compiler and its PuppetDB, on 2026-09-09. Every `ensure =>
// absent` File in a real catalog, and the tp module alone contributed
// sixteen of them, was classified as an unsupported byte comparison. The
// resource was reported as changed when both catalogs held it
// identically, the accompanying diagnostic said "directory, recursive or
// non-file byte evidence is unsupported" about a plain file, and the
// error severity turned an otherwise clean comparison into exit 30.
//
// A catalog that says `ensure => absent` asks Puppet to remove the path.
// Puppet ignores content, source and checksum_value there, so there are
// no desired bytes: a determinate answer, not missing evidence.

func absentFile(params map[string]any) model.Resource {
	return model.Resource{
		Identity:   model.ResourceIdentity{Type: "File", Title: "/etc/tp/app/example"},
		Parameters: params,
	}
}

func TestUnmanagedContentIsNotAFailedComparison(t *testing.T) {
	// Both sides carry inline content that differs. Puppet applies
	// neither, because neither catalog manages bytes at this path.
	before := Side{Resource: absentFile(map[string]any{"ensure": "absent", "content": "one"})}
	after := Side{Resource: absentFile(map[string]any{"ensure": "absent", "content": "two"})}

	e, d := ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, nil)
	if d != nil {
		t.Errorf("an unmanaged File produced a diagnostic: %+v", d)
	}
	if e.State != model.FileContentNotManaged {
		t.Errorf("state = %q, want %q", e.State, model.FileContentNotManaged)
	}
	if e.BeforeDigest != "" || e.AfterDigest != "" {
		t.Errorf("unmanaged content carries digest evidence: %+v", e)
	}
}

func TestUnmanagedOnEitherSideAloneIsEnough(t *testing.T) {
	managed := Side{Resource: absentFile(map[string]any{"ensure": "file", "content": "one"})}
	for name, unmanaged := range map[string]Side{
		"absent": {Resource: absentFile(map[string]any{"ensure": "absent", "content": "two"})},
		"link":   {Resource: absentFile(map[string]any{"ensure": "link", "target": "/elsewhere"})},
		// Puppet's ensure values are not case sensitive.
		"mixed case": {Resource: absentFile(map[string]any{"ensure": "Absent", "content": "two"})},
	} {
		t.Run(name, func(t *testing.T) {
			for _, sides := range [][2]Side{{managed, unmanaged}, {unmanaged, managed}} {
				e, d := ResolveFileContentEvidence(context.Background(), "node", managed.Resource.Identity, sides[0], sides[1], nil)
				if d != nil {
					t.Errorf("diagnostic for a side that manages no bytes: %+v", d)
				}
				if e.State != model.FileContentNotManaged {
					t.Errorf("state = %q, want %q", e.State, model.FileContentNotManaged)
				}
			}
		})
	}
}

func TestUnmanagedMembershipChangeReportsNoContentEvidence(t *testing.T) {
	for _, kind := range []model.ChangeKind{model.ChangeResourceAdded, model.ChangeResourceRemoved} {
		side := Side{
			Resource: absentFile(map[string]any{"ensure": "absent", "content": "one"}),
			Context:  model.ContentContext{Historical: true},
		}
		e, d := ResolveMembershipEvidence(context.Background(), "node", kind, side, nil)
		if d != nil {
			t.Errorf("%s: diagnostic for a resource managing no bytes: %+v", kind, d)
		}
		if e.State != model.FileContentNotManaged {
			t.Errorf("%s: state = %q, want %q", kind, e.State, model.FileContentNotManaged)
		}
	}
}

// The narrower half of the same fix: `ensure => absent` must stop
// reaching nonByteComparable, but a directory, a recursive source and
// non-file static metadata must keep reaching it. Losing that would turn
// the false-clean case phase 2 exists to prevent back on.
func TestUnsupportedByteComparisonsStillAre(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"directory": {"ensure": "directory", "source": "puppet:///modules/app/dir"},
		"recursive": {"ensure": "file", "recurse": true, "source": "puppet:///modules/app/dir"},
	} {
		t.Run(name, func(t *testing.T) {
			side := Side{Resource: absentFile(params), Context: model.ContentContext{Historical: true}}
			e, d := ResolveMembershipEvidence(context.Background(), "node", model.ChangeResourceRemoved, side, nil)
			if d == nil || e.State != model.FileContentIndeterminate {
				t.Fatalf("unsupported byte evidence was not reported: %+v %+v", e, d)
			}
		})
	}
}

// Both sides produced valid evidence, in different algorithms. Nothing
// failed, so the diagnostic is a warning, and nothing is comparable, so
// the state is indeterminate. Before this was decided explicitly it fell
// through evidenceSeverity's nil-skipping loop and got the same answer
// by accident.
func TestMismatchedDigestAlgorithmsAreAWarning(t *testing.T) {
	md5 := "d41d8cd98f00b204e9800998ecf8427e"
	before := Side{Resource: model.Resource{
		Identity:   model.ResourceIdentity{Type: "File", Title: "/etc/motd"},
		Parameters: map[string]any{"checksum": "md5", "checksum_value": md5},
	}}
	after := Side{Resource: model.Resource{
		Identity:   model.ResourceIdentity{Type: "File", Title: "/etc/motd"},
		Parameters: map[string]any{"content": "bytes"},
	}}

	e, d := ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, nil)
	if e.State != model.FileContentIndeterminate {
		t.Errorf("state = %q, want %q", e.State, model.FileContentIndeterminate)
	}
	if d == nil || d.Severity != model.SeverityWarning {
		t.Fatalf("want a warning-severity diagnostic, got %+v", d)
	}
	if !strings.Contains(d.Message, "different algorithms") {
		t.Errorf("the reason does not say why: %q", d.Message)
	}
	if e.BeforeDigest != "" || e.AfterDigest != "" || e.Algorithm != "" {
		t.Errorf("incomparable digests were published: %+v", e)
	}
}
