package normalize

import (
	"strings"
	"testing"
)

func TestSensitivityWireShapes(t *testing.T) {
	for _, shape := range []string{"compiler", "puppetdb"} {
		t.Run(shape, func(t *testing.T) {
			build := compilerShapedCatalog
			if shape == "puppetdb" {
				build = pdbShapedCatalog
			}
			raw := build("node", "production", `[{"type":"User","title":"app","sensitive_parameters":["password"],"parameters":{"password":"plaintext-secret"}}]`, `[]`)
			got, diag := Catalog(raw)
			if diag != nil {
				t.Fatal(diag)
			}
			if len(got.Resources[0].SensitiveParameters) != 1 || got.Resources[0].SensitiveParameters[0] != "password" {
				t.Fatal("sensitivity lost")
			}
			for _, invalid := range []string{`null`, `"plaintext-secret"`, `{}`, `[null]`, `[1]`, `[""]`, `["password","password"]`} {
				raw := build("node", "production", `[{"type":"User","title":"app","sensitive_parameters":`+invalid+`,"parameters":{"password":"plaintext-secret"}}]`, `[]`)
				_, diag := Catalog(raw)
				if diag == nil {
					t.Errorf("accepted malformed metadata %s", invalid)
					continue
				}
				if strings.Contains(diag.Message, "plaintext-secret") {
					t.Error("diagnostic disclosed metadata value")
				}
			}
		})
	}
}

func TestMalformedNestedSensitiveWrapperFails(t *testing.T) {
	raw := compilerShapedCatalog("node", "production", `[{"type":"User","title":"app","parameters":{"settings":[{"secret-key":{"__ptype":"Sensitive"}}]}}]`, `[]`)
	if _, diag := Catalog(raw); diag == nil || strings.Contains(diag.Message, "secret-key") {
		t.Fatalf("unsafe validation result: %v", diag)
	}
}
