package impact

import (
	"fmt"
	"strings"

	"github.com/example42/piace/internal/model"
)

// errUnencodableLiteral reports a resource identity component that
// cannot be expressed as a PQL double-quoted string literal. Its text
// never contains the offending bytes; the caller adds the identity.
type errUnencodableLiteral struct {
	component string
}

func (e *errUnencodableLiteral) Error() string {
	return "impact: " + e.component + " contains a control character with no documented PQL string escape"
}

// BuildPQL renders the exact impact query for one resource identity:
//
//	resources[certname] { type = <quoted-type> and title = <quoted-title> }
//
// It is the single PQL string-literal encoder: no other code in this
// package composes query text.
//
// It returns an error rather than a best-effort query when either
// component cannot be safely encoded; see doc.go.
func BuildPQL(identity model.ResourceIdentity) (string, error) {
	quotedType, err := quotePQLString(identity.Type)
	if err != nil {
		return "", &errUnencodableLiteral{component: "resource type"}
	}
	quotedTitle, err := quotePQLString(identity.Title)
	if err != nil {
		return "", &errUnencodableLiteral{component: "resource title"}
	}
	return fmt.Sprintf("resources[certname] { type = %s and title = %s }", quotedType, quotedTitle), nil
}

// quotePQLString renders s as a PQL double-quoted string literal.
//
// PuppetDB's PQL reference documents double-quoted strings as supporting
// escape sequences (and single-quoted strings as supporting none), so
// this is the only literal form PIACE emits. Backslash and double quote
// must be escaped for the literal to terminate correctly; newline,
// carriage return, and tab use their documented C-style escapes.
//
// Any other control character (U+0000 to U+001F) has no documented
// escape form, the reference establishing no \uXXXX syntax, so it is
// refused rather than emitted raw, passed through, or silently dropped.
// Every other rune, non-ASCII text included, is emitted literally: PQL
// queries are sent UTF-8 encoded and URL-escaped by net/url, so a
// multibyte rune needs no further treatment here.
func quotePQLString(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				return "", fmt.Errorf("impact: control character U+%04X has no documented PQL escape", r)
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}
