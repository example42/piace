package compiler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/example42/piace/internal/model"
)

func TestInvalidFactIdentityFailsBeforeCompilerLookup(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input reached compiler") })
	adapter := newAdapter(t, fixture, srv)
	for _, api := range []string{"v3", "v4"} {
		for _, kind := range []string{"factset", "trusted", "malformed trusted"} {
			t.Run(api+"/"+kind, func(t *testing.T) {
				fs := factsetWithTrusted("node.example", "production", true)
				switch kind {
				case "factset":
					fs.Certname = "other"
				case "trusted":
					fs.Facts = json.RawMessage(`{"data":[{"name":"trusted","value":{"certname":"other","authenticated":"remote"}}]}`)
				case "malformed trusted":
					fs.Facts = json.RawMessage(`{"data":[{"name":"trusted","value":{"certname":"node.example","authenticated":true}}]}`)
				}
				target := v4Target("node.example", "production", false, true)
				if api == "v3" {
					target = v3Target("node.example", "production")
				}
				_, _, _, diag := adapter.RequestCandidate(context.Background(), target, fs)
				if diag == nil || diag.Operation != model.OperationRequestCandidateTransport {
					t.Fatalf("unexpected classification: %v", diag)
				}
			})
		}
	}
}
