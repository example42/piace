package impact

import (
	"strings"
	"testing"

	"github.com/example42/piace/internal/model"
)

func TestBuildPQL_MatchesTheSpecifiedTextVerbatim(t *testing.T) {
	got, err := BuildPQL(model.ResourceIdentity{Type: "Package", Title: "nginx"})
	if err != nil {
		t.Fatalf("BuildPQL: %v", err)
	}
	want := `resources[certname] { type = "Package" and title = "nginx" }`
	if got != want {
		t.Errorf("PQL =\n  %s\nwant\n  %s", got, want)
	}
}

// The escaping that matters: a title able to terminate the literal early
// would let the rest of it be parsed as query syntax.
func TestBuildPQL_EscapesQuotesAndBackslashes(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"double quote", `a"b`, `"a\"b"`},
		{"backslash", `a\b`, `"a\\b"`},
		{"trailing backslash", `a\`, `"a\\"`},
		{"quote injection attempt", `x" or title = "y`, `"x\" or title = \"y"`},
		{"newline", "a\nb", `"a\nb"`},
		{"carriage return", "a\rb", `"a\rb"`},
		{"tab", "a\tb", `"a\tb"`},
		{"non-ascii passes through", "café-01", `"café-01"`},
		{"empty", "", `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildPQL(model.ResourceIdentity{Type: "File", Title: tc.title})
			if err != nil {
				t.Fatalf("BuildPQL: %v", err)
			}
			want := `resources[certname] { type = "File" and title = ` + tc.want + ` }`
			if got != want {
				t.Errorf("PQL =\n  %s\nwant\n  %s", got, want)
			}
		})
	}
}

// A quote-injection attempt must not produce a query with an unbalanced
// or extra top-level literal delimiter.
func TestBuildPQL_InjectionAttemptKeepsExactlyFourDelimiters(t *testing.T) {
	got, err := BuildPQL(model.ResourceIdentity{Type: "File", Title: `x" or title = "y`})
	if err != nil {
		t.Fatalf("BuildPQL: %v", err)
	}
	unescaped := 0
	for i := 0; i < len(got); i++ {
		if got[i] != '"' {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && got[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			unescaped++
		}
	}
	if unescaped != 4 {
		t.Errorf("got %d unescaped quotes in %s, want exactly 4 (two literals)", unescaped, got)
	}
}

// A control character with no documented PQL escape is refused rather
// than guessed at.
func TestBuildPQL_RefusesUnencodableControlCharacters(t *testing.T) {
	for _, title := range []string{"a\x00b", "a\x01b", "a\x1fb", "a\bb"} {
		_, err := BuildPQL(model.ResourceIdentity{Type: "File", Title: title})
		if err == nil {
			t.Errorf("BuildPQL(%q) succeeded, want a refusal", title)
			continue
		}
		if strings.Contains(err.Error(), title) {
			t.Errorf("error text echoes the offending title: %v", err)
		}
		if !strings.Contains(err.Error(), "title") {
			t.Errorf("error should name the offending component: %v", err)
		}
	}
}

func TestBuildPQL_RefusesUnencodableType(t *testing.T) {
	_, err := BuildPQL(model.ResourceIdentity{Type: "Fi\x00le", Title: "/etc/motd"})
	if err == nil {
		t.Fatal("expected a refusal for an unencodable type")
	}
	if !strings.Contains(err.Error(), "type") {
		t.Errorf("error should name the offending component: %v", err)
	}
}
