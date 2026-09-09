package filecontent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

type recordingRetriever struct {
	calls []string
	get   func(string, RetrievalContext) (DigestEvidence, error)
}

func (r *recordingRetriever) Digest(_ context.Context, ref string, rc RetrievalContext) (DigestEvidence, error) {
	r.calls = append(r.calls, rc.Environment+":"+ref)
	return r.get(ref, rc)
}

func evidenceSide(environment string, historical bool, params map[string]any) Side {
	return Side{Resource: model.Resource{Identity: model.ResourceIdentity{Type: "File", Title: "/app"}, Parameters: params},
		Context: model.ContentContext{Source: "compiler", Environment: environment, Historical: historical}}
}

func TestIndependentEnvironmentAndHistoricalEvidence(t *testing.T) {
	before := evidenceSide("production", false, map[string]any{"source": "puppet:///modules/app/config"})
	after := evidenceSide("candidate", false, before.Resource.Parameters)
	r := &recordingRetriever{get: func(_ string, rc RetrievalContext) (DigestEvidence, error) {
		return DigestEvidence{Algorithm: "sha256", Digest: hashLocalContent(rc.Environment)}, nil
	}}
	e, d := ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, r)
	if d != nil || e.State != model.FileContentChanged || len(r.calls) != 2 || r.calls[0] == r.calls[1] {
		t.Fatalf("independent environments lost: %+v, %+v, %v", e, d, r.calls)
	}
	before.Context.Historical = true
	r.calls = nil
	e, d = ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, r)
	if d == nil || e.State != model.FileContentIndeterminate || len(r.calls) != 1 || !strings.HasPrefix(r.calls[0], "candidate:") {
		t.Fatalf("historical source fetched from current environment: %+v, %+v, %v", e, d, r.calls)
	}
	before.Resource.CapturedContent = &model.ContentDigest{Algorithm: "sha256", Digest: hashLocalContent("production")}
	e, d = ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, r)
	if d != nil || e.State != model.FileContentChanged || e.Before.Source != model.FileContentEvidenceCaptured || !e.Before.Verified {
		t.Fatalf("captured evidence not used: %+v, %+v", e, d)
	}
}

func TestSourceFallbackOnlyOnVerifiedAbsence(t *testing.T) {
	for _, firstErr := range []error{ErrSourceNotFound, errors.New("401"), errors.New("403"), errors.New("timeout")} {
		r := &recordingRetriever{get: func(ref string, _ RetrievalContext) (DigestEvidence, error) {
			if ref == "first" {
				return DigestEvidence{}, firstErr
			}
			return DigestEvidence{Algorithm: "sha256", Digest: hashLocalContent("second")}, nil
		}}
		s := evidenceSide("candidate", false, map[string]any{"source": []any{"first", "second"}})
		d, _, err := ResolveSide(context.Background(), "node", s, r)
		if errors.Is(firstErr, ErrSourceNotFound) {
			if err != nil || d.Digest != hashLocalContent("second") || !reflect.DeepEqual(r.calls, []string{"candidate:first", "candidate:second"}) {
				t.Fatal("first existing source not selected")
			}
		} else if err == nil || len(r.calls) != 1 {
			t.Fatal("failure hidden by fallback")
		}
	}
}

func TestChecksumValidationAndEquivalentForms(t *testing.T) {
	for algorithm, size := range map[string]int{"md5": 32, "sha224": 56, "sha256": 64, "sha384": 96, "sha512": 128} {
		want := strings.Repeat("a", size)
		for _, value := range []string{want, strings.ToUpper(want), "{" + algorithm + "}" + want} {
			d, err := validateDigest(algorithm, value)
			if err != nil || d.Digest != want || d.Algorithm != algorithm {
				t.Fatalf("valid digest rejected: %s", algorithm)
			}
		}
	}
	for _, value := range []string{"audit-new-secret", "", strings.Repeat("g", 64), strings.Repeat("a", 63), "{md5}" + strings.Repeat("a", 64), "{sha256}", strings.Repeat("a", 64) + "\n"} {
		s := evidenceSide("candidate", false, map[string]any{"checksum": "sha256", "checksum_value": value})
		e, d := ResolveFileContentEvidence(context.Background(), "node", s.Resource.Identity, s, s, nil)
		if d == nil || e.State == model.FileContentUnchanged || e.BeforeDigest != "" || e.AfterDigest != "" {
			t.Fatal("invalid checksum published")
		}
		encoded, _ := json.Marshal(e)
		if strings.Contains(string(encoded), "audit-new-secret") || strings.Contains(d.Message, "audit-new-secret") {
			t.Fatal("invalid checksum leaked")
		}
	}
	for _, algorithm := range []string{"mtime", "ctime", "none", "sha256lite", "sha1"} {
		if _, err := validateDigest(algorithm, strings.Repeat("a", 64)); err == nil {
			t.Fatalf("unsupported algorithm %s", algorithm)
		}
	}
}

func TestStaticMetadataAndMixedEvidence(t *testing.T) {
	before := evidenceSide("production", true, map[string]any{"source": "puppet:///modules/app/config"})
	before.Resource.StaticContent = &model.StaticFileMetadata{Type: "file"}
	before.Resource.StaticContent.Checksum.Type = "sha256"
	before.Resource.StaticContent.Checksum.Value = "{sha256}" + hashLocalContent("inline")
	after := evidenceSide("candidate", false, map[string]any{"content": "inline"})
	e, d := ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, nil)
	if d != nil || e.State != model.FileContentUnchanged || e.Before.Source != model.FileContentEvidenceStaticMetadata || e.After.Source != model.FileContentEvidenceInline {
		t.Fatalf("static/inline mismatch: %+v %+v", e, d)
	}
	before.Resource.StaticContent.Checksum.Value = "audit-new-secret"
	e, d = ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, nil)
	if d == nil || e.State != model.FileContentReferenceChanged || e.BeforeDigest != "" {
		t.Fatal("bad static metadata accepted")
	}
}

func TestRecursiveMetadataNeverBecomesASingleFileDigest(t *testing.T) {
	for _, selection := range []string{"first", "all"} {
		s := evidenceSide("candidate", false, map[string]any{"source": []any{"first", "second"}, "recurse": true, "sourceselect": selection})
		s.Resource.RecursiveContent = true
		s.Resource.CapturedContent = &model.ContentDigest{Algorithm: "sha256", Digest: hashLocalContent("one file")}
		r := &recordingRetriever{get: func(string, RetrievalContext) (DigestEvidence, error) {
			t.Fatal("recursive content retrieved as one file")
			return DigestEvidence{}, nil
		}}
		e, d := ResolveFileContentEvidence(context.Background(), "node", s.Resource.Identity, s, s, r)
		if d == nil || e.State != model.FileContentIndeterminate || len(r.calls) != 0 {
			t.Fatal("recursive comparison appears verified")
		}
	}
}
