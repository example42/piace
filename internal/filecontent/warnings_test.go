package filecontent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

func TestSuppressSourceContentWarningsPreservesEvidence(t *testing.T) {
	source := map[string]any{"source": "puppet:///modules/app/config"}
	for _, tc := range []struct {
		name           string
		before, after  map[string]any
		retrievalError error
		wantSeverity   model.DiagnosticSeverity
		suppress       bool
	}{
		{"historical source", source, source, nil, model.SeverityWarning, true},
		{"changed source", source, map[string]any{"source": "puppet:///modules/app/new"}, nil, model.SeverityWarning, true},
		{"source list", map[string]any{"source": []any{"first", "second"}}, source, nil, model.SeverityWarning, true},
		{"recursive source", map[string]any{"source": "puppet:///modules/app/dir", "recurse": true}, source, nil, model.SeverityWarning, true},
		{"local source", source, map[string]any{"source": "/local/file"}, errUnsupportedSourceScheme, model.SeverityWarning, true},
		{"no source", map[string]any{"content": "bytes", "ensure": "directory"}, map[string]any{"content": "bytes", "ensure": "directory"}, nil, model.SeverityWarning, false},
		{"different algorithms", map[string]any{"source": "source", "checksum": "md5", "checksum_value": strings.Repeat("a", 32)}, map[string]any{"source": "source", "content": "bytes"}, nil, model.SeverityWarning, false},
		{"invalid checksum", map[string]any{"source": "source", "checksum_value": "invalid"}, source, nil, model.SeverityError, false},
		{"retrieval failure", source, source, errors.New("retrieval failed"), model.SeverityError, false},
		{"unsafe source", source, source, errUnsafeSourcePath, model.SeverityError, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := evidenceSide("production", true, tc.before)
			after := evidenceSide("candidate", false, tc.after)
			r := &recordingRetriever{get: func(string, RetrievalContext) (DigestEvidence, error) {
				return DigestEvidence{Algorithm: "sha256", Digest: hashLocalContent("bytes")}, tc.retrievalError
			}}
			evidence, diagnostic := ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, r, Options{})
			if diagnostic == nil || diagnostic.Severity != tc.wantSeverity {
				t.Fatalf("default diagnostic = %+v", diagnostic)
			}
			calls := append([]string(nil), r.calls...)
			r.calls = nil
			quietEvidence, quietDiagnostic := ResolveFileContentEvidence(context.Background(), "node", before.Resource.Identity, before, after, r, Options{SuppressSourceContentWarnings: true})
			if !reflect.DeepEqual(evidence, quietEvidence) || !reflect.DeepEqual(calls, r.calls) {
				t.Fatalf("suppression changed evidence or retrievals: %+v / %+v, %v / %v", evidence, quietEvidence, calls, r.calls)
			}
			if tc.suppress {
				if quietDiagnostic != nil {
					t.Errorf("warning not suppressed: %+v", quietDiagnostic)
				}
			} else if !reflect.DeepEqual(diagnostic, quietDiagnostic) {
				t.Errorf("unrelated diagnostic changed: %+v / %+v", diagnostic, quietDiagnostic)
			}
		})
	}
}

func TestSuppressSourceMembershipWarningsPreservesEvidence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     model.ChangeKind
		params   map[string]any
		suppress bool
	}{
		{"historical removal", model.ChangeResourceRemoved, map[string]any{"source": "puppet:///modules/app/file"}, true},
		{"recursive addition", model.ChangeResourceAdded, map[string]any{"source": "puppet:///modules/app/dir", "recurse": true}, true},
		{"invalid checksum", model.ChangeResourceRemoved, map[string]any{"source": "source", "checksum_value": "invalid"}, false},
		{"non-source directory", model.ChangeResourceAdded, map[string]any{"content": "bytes", "ensure": "directory"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			side := evidenceSide("production", tc.kind == model.ChangeResourceRemoved, tc.params)
			evidence, diagnostic := ResolveMembershipEvidence(context.Background(), "node", tc.kind, side, nil, Options{})
			quietEvidence, quietDiagnostic := ResolveMembershipEvidence(context.Background(), "node", tc.kind, side, nil, Options{SuppressSourceContentWarnings: true})
			if diagnostic == nil || !reflect.DeepEqual(evidence, quietEvidence) {
				t.Fatalf("missing default warning or changed evidence: %+v / %+v", evidence, quietEvidence)
			}
			if tc.suppress {
				if quietDiagnostic != nil {
					t.Errorf("warning not suppressed: %+v", quietDiagnostic)
				}
			} else if !reflect.DeepEqual(diagnostic, quietDiagnostic) {
				t.Errorf("unrelated diagnostic changed: %+v / %+v", diagnostic, quietDiagnostic)
			}
		})
	}
}
