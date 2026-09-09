package diff

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

func TestResourceSensitivityProtectsBothSides(t *testing.T) {
	for _, side := range []string{"before", "after", "both"} {
		t.Run(side, func(t *testing.T) {
			br := resource("User", "app", map[string]any{"password": "old-secret"})
			ar := resource("User", "app", map[string]any{"password": "new-secret"})
			if side != "after" {
				br.SensitiveParameters = []string{"password"}
			}
			if side != "before" {
				ar.SensitiveParameters = []string{"password"}
			}
			nd, ds := run(t, target(nil, nil), catalog([]model.Resource{br}, nil), catalog([]model.Resource{ar}, nil), nil)
			if !nd.HasDifference || len(ds) != 0 || len(nd.ResourceChanges) != 1 {
				t.Fatal("missing comparison")
			}
			change := nd.ResourceChanges[0]
			if change.Before != model.RedactedValue || change.After != model.RedactedValue {
				t.Fatal("values not both protected")
			}
			encoded, err := json.Marshal(nd)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), change.Fingerprint) {
				t.Fatal("serialized secret or fingerprint")
			}
		})
	}
}

func TestWrapperIntroductionAndRemovalProtectCorrespondingValues(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		before := map[string]any{"plain": "visible", "nested": []any{"old-secret"}}
		after := map[string]any{"plain": "visible", "nested": []any{sensitive("new-secret")}}
		if reverse {
			before, after = after, before
		}
		b, a := redactSensitivePair(before, after)
		for _, v := range []any{b, a} {
			encoded, _ := json.Marshal(v)
			if strings.Contains(string(encoded), "secret") || !strings.Contains(string(encoded), "visible") {
				t.Fatal("incorrect recursive projection")
			}
		}
	}
}

func TestSensitiveFileContentSuppressesDigests(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		br := resource("File", "/secret", map[string]any{"content": "old-secret"})
		ar := resource("File", "/secret", map[string]any{"content": "new-secret"})
		if wrapped {
			ar.Parameters["content"] = sensitive("new-secret")
		} else {
			ar.SensitiveParameters = []string{"content"}
		}
		nd, ds := run(t, target(nil, nil), catalog([]model.Resource{br}, nil), catalog([]model.Resource{ar}, nil), nil)
		if len(ds) != 0 || len(nd.ResourceChanges) != 1 {
			t.Fatalf("comparison failed: %v", ds)
		}
		evidence := nd.ResourceChanges[0].FileContent
		if evidence == nil || evidence.State != model.FileContentChanged || !evidence.Redacted || evidence.Algorithm != "" || evidence.BeforeDigest != model.RedactedValue || evidence.AfterDigest != model.RedactedValue {
			t.Fatal("unsafe content projection")
		}
	}
}

func TestRawEvidenceCannotBePublished(t *testing.T) {
	raw := rawResourceChange{Before: "secret"}
	if _, err := json.Marshal(raw); err == nil {
		t.Fatal("raw evidence serialized")
	}
	if strings.Contains(fmt.Sprintf("%+v", raw), "secret") {
		t.Fatal("raw evidence formatted")
	}
}

func TestSensitiveContainerShapeChangeIsMasked(t *testing.T) {
	before := map[string]any{"old-key": sensitive("secret")}
	after := map[string]any{"new-key": "secret"}
	b, a := redactSensitivePair(before, after)
	if b != model.RedactedValue || a != model.RedactedValue {
		t.Fatal("shape change exposed sensitive evidence")
	}
}

func TestDistinctSensitiveResourceAndFileChangesRemainDistinct(t *testing.T) {
	for _, resourceType := range []string{"User", "File"} {
		var diffs []model.NodeDiff
		for _, secret := range []string{"first-secret", "second-secret"} {
			param := "password"
			if resourceType == "File" {
				param = "source"
			}
			br := resource(resourceType, "app", map[string]any{param: "old-secret"})
			ar := resource(resourceType, "app", map[string]any{param: secret})
			ar.SensitiveParameters = []string{param}
			nd, _ := run(t, target(nil, nil), catalog([]model.Resource{br}, nil), catalog([]model.Resource{ar}, nil), nil)
			nd.Certname = secret[:6]
			diffs = append(diffs, nd)
		}
		if len(diffs[0].ResourceChanges) != 1 || len(diffs[1].ResourceChanges) != 1 {
			t.Fatal("missing change")
		}
		if diffs[0].ResourceChanges[0].Fingerprint == diffs[1].ResourceChanges[0].Fingerprint {
			t.Fatal("distinct sensitive changes share grouping fingerprint")
		}
	}
}
