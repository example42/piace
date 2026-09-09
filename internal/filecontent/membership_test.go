package filecontent

import (
	"context"
	"testing"

	"github.com/example42/piace/internal/model"
)

func TestMembershipEvidenceResolvesOnlyExistingSide(t *testing.T) {
	for _, kind := range []model.ChangeKind{model.ChangeResourceAdded, model.ChangeResourceRemoved} {
		side := Side{Resource: model.Resource{Identity: model.ResourceIdentity{Type: "File", Title: "/app"}, Parameters: map[string]any{"content": "bytes"}}}
		e, d := ResolveMembershipEvidence(context.Background(), "node", kind, side, nil)
		if d != nil || e.Algorithm != "sha256" {
			t.Fatalf("resolution failed: %+v %+v", e, d)
		}
		if kind == model.ChangeResourceAdded {
			if e.State != model.FileContentAdded || e.Before != nil || e.BeforeDigest != "" || !e.After.Verified || e.AfterDigest != hashLocalContent("bytes") {
				t.Fatalf("incorrect addition: %+v", e)
			}
		} else if e.State != model.FileContentRemoved || e.After != nil || e.AfterDigest != "" || !e.Before.Verified || e.BeforeDigest != hashLocalContent("bytes") {
			t.Fatalf("incorrect removal: %+v", e)
		}
	}
}

func TestMembershipEvidenceDisclosesMissingOrUnsupportedEvidence(t *testing.T) {
	for _, params := range []map[string]any{
		{"source": "puppet:///modules/app/file"},
		{"source": "puppet:///modules/app/dir", "recurse": true},
		{"checksum_value": "invalid-secret"},
	} {
		side := Side{Resource: model.Resource{Identity: model.ResourceIdentity{Type: "File", Title: "/app"}, Parameters: params}, Context: model.ContentContext{Historical: true}}
		e, d := ResolveMembershipEvidence(context.Background(), "node", model.ChangeResourceRemoved, side, nil)
		if d == nil || d.Severity != model.SeverityError || e.State != model.FileContentIndeterminate || e.Before.Verified || e.BeforeDigest != "" || e.After != nil {
			t.Fatalf("unsupported evidence claimed verified: %+v %+v", e, d)
		}
	}
}
