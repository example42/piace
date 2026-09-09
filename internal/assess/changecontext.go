// Package assess builds a change assessment request from a stored result
// document and interprets the response.
//
// This package owns the disclosure boundary for the change-assessment
// feature: it decides what may leave the process. internal/inference only
// knows how to send what this package hands it, and deliberately knows
// nothing about catalogs. See CONTEXT.md for why the assessment stays
// out of the result document.
package assess

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/example42/piace/internal/limits"
	"gopkg.in/yaml.v3"
)

// ChangeContextFileVersion is the only supported `version` value for a
// change context file.
const ChangeContextFileVersion = 1

// Caps on caller-supplied free text and list lengths. A change context is
// written by whoever opened the pull request, so every field it carries is
// bounded before it can reach an inference request. Over-cap content is
// truncated and the truncation recorded (see ChangeContext.Truncated);
// it never fails the command, because a long pull-request description is
// not a reason to fail a pipeline.
const (
	MaxTitleBytes         = 200
	MaxDescriptionBytes   = 4000
	MaxCommitSubjectBytes = 200
	MaxCommits            = 100
	MaxChangedPaths       = 500
)

// Commit is one commit named by a change context: its identity, its
// subject, and its author. There is deliberately no body field. Subjects
// carry the signal; bodies carry pasted logs and stack traces, and a
// `body` key is therefore an unknown field that LoadChangeContext refuses
// rather than forwards.
type Commit struct {
	SHA     string `yaml:"sha" json:"sha,omitempty"`
	Subject string `yaml:"subject" json:"subject,omitempty"`
	Author  string `yaml:"author" json:"author,omitempty"`
}

// ChangeContext is the caller-supplied description of the repository
// change under test. `explain` reads it and never invokes git, so a
// repository under any VCS can describe its change; `piace
// change-context` writes one from a git checkout.
//
// Every free-text field is untrusted data written by whoever opened the
// change, and is fenced and labelled as such when it reaches a request.
type ChangeContext struct {
	// Present distinguishes "no change context was supplied" from one
	// that was supplied and happens to be empty.
	Present      bool     `json:"present"`
	BaseRef      string   `json:"base_ref,omitempty"`
	HeadRef      string   `json:"head_ref,omitempty"`
	Commits      []Commit `json:"commits,omitempty"`
	ChangedPaths []string `json:"changed_paths,omitempty"`
	Title        string   `json:"title,omitempty"`
	Description  string   `json:"description,omitempty"`
	// Truncated names each field a cap shortened, in a fixed order, so a
	// reader is never shown a silently abbreviated change.
	Truncated []string `json:"truncated,omitempty"`
}

// changeContextFile is the on-disk document. It is separate from
// ChangeContext so the wire shape can reject unknown fields without the
// resolved type carrying yaml tags it does not need.
type changeContextFile struct {
	Version *int       `yaml:"version"`
	Change  changeWire `yaml:"change"`
}

// omitempty is for the encoder's benefit only: a decoder ignores it, and
// a generated document that carries an empty title should not spell it.
type changeWire struct {
	BaseRef      string   `yaml:"base_ref,omitempty"`
	HeadRef      string   `yaml:"head_ref,omitempty"`
	Commits      []Commit `yaml:"commits,omitempty"`
	ChangedPaths []string `yaml:"changed_paths,omitempty"`
	Title        string   `yaml:"title,omitempty"`
	Description  string   `yaml:"description,omitempty"`
}

// LoadChangeContext reads and bounds a change context file. An empty path
// means none was supplied, which is not an error: a comparison with no
// repository change to describe is ordinary. A named path that cannot be
// read is an error, because a caller that named a file meant it.
//
// Decoding uses yaml.Decoder.KnownFields(true), matching
// internal/config/resolve's strict decode.
func LoadChangeContext(path string) (ChangeContext, error) {
	if path == "" {
		return ChangeContext{}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return ChangeContext{}, fmt.Errorf("opening change context file: %w", err)
	}
	defer f.Close()

	// Bounded before decoding, and one byte past the limit so "too large"
	// is decidable without reading the file: this document's free text is
	// disclosed to an inference service, and every cap below applies to
	// values that first have to be read into memory.
	raw, err := io.ReadAll(io.LimitReader(f, limits.ChangeContext+1))
	if err != nil {
		return ChangeContext{}, fmt.Errorf("reading change context file: %w", err)
	}
	if len(raw) > limits.ChangeContext {
		return ChangeContext{}, fmt.Errorf("change context file: exceeds the %d-byte limit", limits.ChangeContext)
	}

	var file changeContextFile
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		return ChangeContext{}, fmt.Errorf("decoding change context file: %w", err)
	}
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		if err == nil {
			return ChangeContext{}, fmt.Errorf("change context file: contains more than one YAML document; PIACE reads exactly one")
		}
		return ChangeContext{}, fmt.Errorf("decoding change context file: %w", err)
	}

	if file.Version == nil {
		return ChangeContext{}, fmt.Errorf("change context file: missing version, want %d", ChangeContextFileVersion)
	}
	if *file.Version != ChangeContextFileVersion {
		return ChangeContext{}, fmt.Errorf("change context file: unsupported version %d, want %d", *file.Version, ChangeContextFileVersion)
	}

	return resolveChangeContext(file.Change), nil
}

// resolveChangeContext applies every cap and records what it shortened.
// The order of Truncated entries is fixed rather than incidental, so two
// runs over the same input name the same fields in the same order.
func resolveChangeContext(w changeWire) ChangeContext {
	cc := ChangeContext{
		Present: true,
		BaseRef: w.BaseRef,
		HeadRef: w.HeadRef,
	}

	var truncated []string
	mark := func(field string) { truncated = append(truncated, field) }

	if title, cut := capString(w.Title, MaxTitleBytes); cut {
		cc.Title = title
		mark("title")
	} else {
		cc.Title = title
	}
	if desc, cut := capString(w.Description, MaxDescriptionBytes); cut {
		cc.Description = desc
		mark("description")
	} else {
		cc.Description = desc
	}

	commits := w.Commits
	if len(commits) > MaxCommits {
		commits = commits[:MaxCommits]
		mark("commits")
	}
	subjectCut := false
	for _, c := range commits {
		subject, cut := capString(c.Subject, MaxCommitSubjectBytes)
		subjectCut = subjectCut || cut
		cc.Commits = append(cc.Commits, Commit{SHA: c.SHA, Subject: subject, Author: c.Author})
	}
	if subjectCut {
		mark("commit subjects")
	}

	paths := w.ChangedPaths
	if len(paths) > MaxChangedPaths {
		paths = paths[:MaxChangedPaths]
		mark("changed_paths")
	}
	cc.ChangedPaths = append(cc.ChangedPaths, paths...)

	cc.Truncated = truncated
	return cc
}

// capString shortens s to at most max bytes without splitting a rune, and
// reports whether it shortened anything. Cutting mid-rune would put
// invalid UTF-8 into a JSON request body.
func capString(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}
