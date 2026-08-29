package assess

import (
	"fmt"
	"sort"
	"strings"

	"github.com/example42/piace/internal/model"
)

// Pseudonyms is a stable per-run substitute for each certname a result
// document names, used only in an inference request body. It is held in
// memory for the length of one run and is never written to a change
// assessment or any report: the artifact carries real certnames, because
// it never leaves the machine that produced it.
//
// A disabled Pseudonyms is the identity mapping, so callers need no
// branch of their own.
type Pseudonyms struct {
	enabled bool
	forward map[string]string
	reverse map[string]string
}

// Of returns the pseudonym for certname, or certname itself when
// pseudonymization is disabled or the certname is unknown to the mapping.
// An unknown certname is returned unchanged rather than invented, so a
// caller can never silently emit a pseudonym that reverses to nothing.
func (p Pseudonyms) Of(certname string) string {
	if !p.enabled {
		return certname
	}
	if got, ok := p.forward[certname]; ok {
		return got
	}
	return certname
}

// certname reverses Of. It is unexported because reversal is this
// package's own job: Interpret does it across a whole response, and no
// caller outside assess ever holds a pseudonym to reverse.
func (p Pseudonyms) certname(pseudonym string) string {
	if !p.enabled {
		return pseudonym
	}
	if got, ok := p.reverse[pseudonym]; ok {
		return got
	}
	return pseudonym
}

// newPseudonyms assigns one pseudonym per certname the result document
// names anywhere — targets, aggregate groups, and impact estimates — in
// sorted certname order, so the assignment depends only on the document
// and not on map iteration or pipeline ordering.
func newPseudonyms(r model.Result, enabled bool) Pseudonyms {
	p := Pseudonyms{enabled: enabled}
	if !enabled {
		return p
	}

	seen := map[string]bool{}
	for _, t := range r.Targets {
		seen[t.Certname] = true
	}
	for _, g := range r.Aggregate.Groups {
		for _, c := range g.Certnames {
			seen[c] = true
		}
	}
	for _, e := range r.ImpactEstimates {
		for _, c := range e.Certnames {
			seen[c] = true
		}
	}

	names := make([]string, 0, len(seen))
	for c := range seen {
		names = append(names, c)
	}
	sort.Strings(names)

	p.forward = make(map[string]string, len(names))
	p.reverse = make(map[string]string, len(names))
	for i, c := range names {
		alias := fmt.Sprintf("node-%03d", i+1)
		p.forward[c] = alias
		p.reverse[alias] = c
	}
	return p
}

// Reveal replaces every pseudonym appearing in s with the certname it
// stands for. A change assessment carries real names — it never leaves the
// machine that produced it — so any pseudonym a model wrote into its prose
// has to be put back before the assessment is written or rendered.
//
// Longer aliases are substituted first: "node-100" is a prefix of
// "node-1000", and replacing the shorter one first would corrupt the
// longer.
func (p Pseudonyms) Reveal(s string) string {
	if !p.enabled || s == "" {
		return s
	}
	aliases := make([]string, 0, len(p.reverse))
	for alias := range p.reverse {
		aliases = append(aliases, alias)
	}
	sort.Slice(aliases, func(i, j int) bool {
		if len(aliases[i]) != len(aliases[j]) {
			return len(aliases[i]) > len(aliases[j])
		}
		return aliases[i] < aliases[j]
	})
	for _, alias := range aliases {
		s = strings.ReplaceAll(s, alias, p.reverse[alias])
	}
	return s
}

// revealAll applies Reveal across a list, preserving order and length.
func (p Pseudonyms) revealAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, p.Reveal(s))
	}
	return out
}
