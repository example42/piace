package puppetdb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestFactsetStructureValidation(t *testing.T) {
	for _, raw := range []string{``, `null`, `{}`, `[]`, `{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[{}]}`, `{"data":[{"name":"os"}]}`, `{"data":[{"name":1,"value":1}]}`, `{"data":[{"name":"os","value":1},{"name":"os","value":2}]}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := FlattenFacts(json.RawMessage(raw)); err == nil {
				t.Fatal("accepted malformed collection")
			}
		})
	}
	for _, raw := range []string{`{"data":[]}`, `{"data":[{"name":"os","value":null}]}`} {
		if _, err := FlattenFacts(json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTrustedFactsValidation(t *testing.T) {
	for _, auth := range []string{`"remote"`, `"local"`} {
		raw := fmt.Sprintf(`{"certname":"node.example","authenticated":%s,"extensions":{},"hostname":"node","domain":"example","external":{}}`, auth)
		if err := ValidateTrustedFacts([]byte(raw), "node.example"); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"certname":"other","authenticated":"remote"}`,
		`{"certname":"node.example","authenticated":null}`, `{"certname":"node.example","authenticated":true}`,
		`{"certname":"node.example","authenticated":false}`, `{"certname":null,"authenticated":false}`,
		`{"certname":"node.example","authenticated":"false"}`, `{"certname":"node.example","authenticated":"secret"}`,
		`{"certname":"node.example","authenticated":"remote","extensions":[]}`,
		`{"certname":"node.example","authenticated":"remote","extensions":null}`,
		`{"certname":"node.example","authenticated":"remote","hostname":3}`,
		`{"certname":"node.example","authenticated":"remote","domain":"wrong"}`,
		`{"certname":"node.example","authenticated":"remote","external":false}`,
	} {
		err := ValidateTrustedFacts([]byte(raw), "node.example")
		if err == nil {
			t.Errorf("accepted %s", raw)
		} else if strings.Contains(err.Error(), "secret") {
			t.Error("disclosed invalid value")
		}
	}
	if err := ValidateTrustedFacts([]byte(`{"certname":"node","authenticated":"local","domain":null}`), "node"); err != nil {
		t.Fatal(err)
	}
}

func TestFactsetIdentityValidation(t *testing.T) {
	fs := Factset{Certname: "different-node", Facts: json.RawMessage(`{"data":[]}`)}
	if _, err := ValidateFactset(fs, "requested-node"); err == nil {
		t.Fatal("accepted wrong target")
	}
	fs.Certname = "requested-node"
	fs.Facts = json.RawMessage(`{"data":[{"name":"trusted","value":{"certname":"different-node","authenticated":"remote"}}]}`)
	if _, err := ValidateFactset(fs, "requested-node"); err == nil {
		t.Fatal("accepted inconsistent trusted identity")
	}
}
