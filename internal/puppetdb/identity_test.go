package puppetdb

import (
	"context"
	"net/http"
	"testing"

	"github.com/example42/piace/internal/model"
)

func TestWrongResponseTargetRejected(t *testing.T) {
	fixture := newTLSFixture(t, "127.0.0.1")
	srv := newMTLSTestServer(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"certname":"different-node","environment":"production","facts":{"data":[]},"resources":[],"edges":[]}`))
	})
	adapter := newAdapter(t, fixture, srv)
	target := puppetdbTarget("requested-node", "production")
	if _, _, diag := adapter.Load(context.Background(), target); diag == nil || diag.Operation != model.OperationLoadFacts {
		t.Fatalf("factset accepted: %v", diag)
	}
	if _, _, diag := adapter.LoadBaseline(context.Background(), target); diag == nil || diag.Operation != model.OperationLoadBaseline {
		t.Fatalf("catalog accepted: %v", diag)
	}
}
