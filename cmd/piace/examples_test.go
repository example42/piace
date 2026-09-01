package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/config/resolve"
)

// The shipped examples are the first thing a new user copies, and the CI
// documentation tells every reader to copy examples/ci/services.yaml
// verbatim. Decoding is strict, so one mistyped key is a hard load error
// the first time somebody runs it and nothing else in this suite would
// have noticed. These tests load every example the way the command that
// owns it does.

func examplePath(t *testing.T, parts ...string) string {
	t.Helper()
	return filepath.Join(append([]string{"..", "..", "examples"}, parts...)...)
}

// setCredentialPaths points the *_env variables at real files, since the
// examples that use that form name variables rather than paths. The
// values only have to be absolute; nothing reads them at resolve time.
func setCredentialPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, v := range []struct{ name, file string }{
		{"PIACE_CA_BUNDLE", "ca.pem"},
		{"PIACE_CLIENT_CERT", "client.pem"},
		{"PIACE_PRIVATE_KEY", "client.key"},
	} {
		path := filepath.Join(dir, v.file)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		t.Setenv(v.name, path)
	}
}

func TestExamples_TargetAndServicesFilesLoad(t *testing.T) {
	services := []string{
		examplePath(t, "services.yaml"),
		examplePath(t, "ci", "services.yaml"),
	}
	targets := []string{
		examplePath(t, "targets-puppetdb-baseline.yaml"),
		examplePath(t, "targets-snapshot-baseline.yaml"),
		examplePath(t, "targets-v3-legacy.yaml"),
	}
	setCredentialPaths(t)

	for _, s := range services {
		for _, tg := range targets {
			t.Run(filepath.Base(filepath.Dir(s))+"/"+filepath.Base(s)+" + "+filepath.Base(tg), func(t *testing.T) {
				if _, err := resolve.Load(tg, s, resolve.Overrides{}); err != nil {
					t.Errorf("resolve.Load: %v", err)
				}
			})
		}
	}
}

// A comparison job is granted the mTLS material and not the inference
// token, so it has to load a merged services file with an unset
// token_env. That is the arrangement the CI documentation ships, and the
// one that would break if compare ever started resolving the inference
// section.
func TestExamples_CompareIgnoresTheInferenceSection(t *testing.T) {
	setCredentialPaths(t)
	t.Setenv("PIACE_INFERENCE_TOKEN", "")

	cfg, err := resolve.Load(
		examplePath(t, "targets-puppetdb-baseline.yaml"),
		examplePath(t, "ci", "services.yaml"),
		resolve.Overrides{},
	)
	if err != nil {
		t.Fatalf("resolve.Load with an unset inference token: %v", err)
	}
	if cfg.Services.Compiler.URL == nil || cfg.Services.PuppetDB.URL == nil {
		t.Error("both service endpoints should be resolved")
	}
}

// The assessment job is granted the token and not the mTLS material, so
// it has to load the same file with the credential variables unset.
func TestExamples_ExplainIgnoresTheServiceSections(t *testing.T) {
	// Each example picks its own variable name; an assessment job is
	// granted whichever one its services file happens to reference.
	t.Setenv("PIACE_INFERENCE_TOKEN", "token-value")
	t.Setenv("OPENAI_API_KEY", "token-value")
	for _, unset := range []string{"PIACE_CA_BUNDLE", "PIACE_CLIENT_CERT", "PIACE_PRIVATE_KEY"} {
		t.Setenv(unset, "")
	}

	for _, path := range []string{
		examplePath(t, "services.yaml"),
		examplePath(t, "ci", "services.yaml"),
		examplePath(t, "services-explain-only.yaml"),
	} {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			if _, err := resolve.LoadInferenceFile(path); err != nil {
				t.Errorf("resolve.LoadInferenceFile: %v", err)
			}
		})
	}
}

func TestExamples_ChangeContextLoads(t *testing.T) {
	if _, err := assess.LoadChangeContext(examplePath(t, "change-context.yaml")); err != nil {
		t.Errorf("assess.LoadChangeContext: %v", err)
	}
}
