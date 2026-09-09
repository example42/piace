package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/example42/piace/internal/inference"
	"github.com/example42/piace/internal/safemeta"
	"github.com/example42/piace/internal/transport"
)

// debugFlags holds the two observation options every subcommand accepts.
//
// They are deliberately separate because they sit on opposite sides of
// the rule that PIACE never logs private keys, certificate private
// material, request authorization headers, or unredacted sensitive
// catalog parameter values:
//
//   - --debug prints safe metadata only: method, URL, status, timing,
//     body sizes, content type, and the response body's top-level JSON
//     *member names*. That is enough to diagnose a wire-shape mismatch
//     (a v4 response whose only top-level key is "catalog", say) and
//     contains no catalog values, so it is safe for a CI log.
//   - --debug-dump-dir writes the raw request and response bodies to
//     0600 files in an operator-named directory. Those bodies can carry
//     Puppet Sensitive values and unredacted catalog parameters, so they
//     never go to stdout or stderr: this is an operator-requested dump
//     to a restricted path, not logging, and the command says so out
//     loud when it is enabled.
type debugFlags struct {
	debug   bool
	dumpDir string
}

// register adds both options to fs. Every subcommand calls this so the
// observation seam is uniform across compare and both captures.
func (d *debugFlags) register(fs interface {
	BoolVar(*bool, string, bool, string)
	StringVar(*string, string, string, string)
}) {
	fs.BoolVar(&d.debug, "debug", false,
		"print one line per service request to stderr: method, URL, status, duration, body sizes, and the response's top-level JSON keys (no body content)")
	fs.StringVar(&d.dumpDir, "debug-dump-dir", "",
		"write raw request/response bodies to 0600 files in this directory; bodies may contain sensitive catalog values and are never printed to stdout/stderr")
}

// enabled reports whether any observation was requested.
func (d debugFlags) enabled() bool { return d.debug || d.dumpDir != "" }

// transportOptions builds the transport.Option list every service client
// in this invocation is constructed with. It returns nil when neither
// option was requested, so the default code path is untouched.
//
// label is the subcommand name, used to prefix the stderr lines and to
// name the dump directory's files.
func (d debugFlags) transportOptions(label string, stderr io.Writer) ([]transport.Option, error) {
	if !d.enabled() {
		return nil, nil
	}
	sink := &debugSink{label: label, stderr: stderr, printMetadata: d.debug, dumpDir: d.dumpDir}
	if d.dumpDir != "" {
		// 0700: the dump directory holds unredacted request and response bodies,
		// so it is created no more readable than the 0600 files inside it. An
		// existing directory's mode is left alone, that being the operator's
		// choice rather than this command's to override.
		if err := os.MkdirAll(d.dumpDir, 0o700); err != nil {
			return nil, fmt.Errorf("creating --debug-dump-dir: %w", err)
		}
		fmt.Fprintf(stderr, "piace %s: writing raw request/response bodies to %s; they may contain sensitive catalog values\n", label, d.dumpDir)
	}
	opts := []transport.Option{transport.WithObserver(sink.observe)}
	if d.dumpDir != "" {
		opts = append(opts, transport.WithBodyCapture(true))
	}
	return opts, nil
}

// inferenceOptions is transportOptions' counterpart for `explain`'s one
// service. internal/inference deliberately does not import
// internal/transport (see that package's Client doc), so its observation
// seam is separate; this bridges the two so one debugSink renders both.
func (d debugFlags) inferenceOptions(label string, stderr io.Writer) ([]inference.Option, error) {
	if !d.enabled() {
		return nil, nil
	}
	sink := &debugSink{label: label, stderr: stderr, printMetadata: d.debug, dumpDir: d.dumpDir}
	if d.dumpDir != "" {
		if err := os.MkdirAll(d.dumpDir, 0o700); err != nil {
			return nil, fmt.Errorf("creating --debug-dump-dir: %w", err)
		}
		fmt.Fprintf(stderr, "piace %s: writing raw request/response bodies to %s; the request body is the catalog-derived payload and a failed response names the account behind the token\n", label, d.dumpDir)
	}
	opts := []inference.Option{inference.WithObserver(sink.observeInference)}
	if d.dumpDir != "" {
		opts = append(opts, inference.WithBodyCapture(true))
	}
	return opts, nil
}

// debugSink renders transport.Event values. One sink is shared by every
// client in an invocation so the dump-file sequence numbers reflect the
// real request order across both services.
type debugSink struct {
	label         string
	stderr        io.Writer
	printMetadata bool
	dumpDir       string

	mu  sync.Mutex
	seq int
}

// observe implements transport.Observer. It is called synchronously from
// transport.Client.Do.
func (s *debugSink) observe(ev transport.Event) {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	if s.printMetadata {
		fmt.Fprintf(s.stderr, "piace %s: debug #%03d %s\n", s.label, seq, describeEvent(ev))
	}
	if s.dumpDir == "" {
		return
	}
	base := fmt.Sprintf("%03d-%s-%s", seq, strings.ToLower(ev.Method), slugPath(ev.URL))
	s.dump(base+".request", ev.RequestBody)
	s.dump(base+".response", ev.ResponseBody)
}

// dump writes one body to a 0600 file. A dump failure is reported to
// stderr but never fails the run: observation must not change a
// comparison's or capture's outcome.
//
// Unlink first, then create exclusively, rather than os.WriteFile,
// because these bodies are the unredacted ones. os.WriteFile opens an
// existing path for writing: it follows a symlink already sitting there
// and leaves whatever mode that file already had, applying 0600 only to
// a file it creates itself. A dump directory is often /tmp or a CI
// workspace, and neither is somewhere to take that on trust with raw
// catalog values.
//
// os.Remove unlinks a symlink rather than its target, so the create that
// follows always makes a fresh 0600 regular file. Removing first rather
// than only creating exclusively keeps a second run against the same
// dump directory working the way the first one did; the sequence numbers
// restart at 001 each run, so O_EXCL alone would refuse every file.
func (s *debugSink) dump(base string, body []byte) {
	if len(body) == 0 {
		return
	}
	name := base + ".bin"
	if json.Valid(body) {
		name = base + ".json"
	}
	path := filepath.Join(s.dumpDir, name)
	// A path that does not exist is the ordinary case; any other failure
	// surfaces from the create below, with the message that names it.
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintf(s.stderr, "piace %s: debug: writing %s: %v\n", s.label, path, err)
		return
	}
	if _, err := f.Write(body); err != nil {
		fmt.Fprintf(s.stderr, "piace %s: debug: writing %s: %v\n", s.label, path, err)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(s.stderr, "piace %s: debug: writing %s: %v\n", s.label, path, err)
	}
}

// observeInference is observe's counterpart for inference.Event. One
// debugSink is built per explain run and every inference request in that
// run goes through it, so a run whose first reply was unusable and was
// retried numbers both requests #001 and #002.
func (s *debugSink) observeInference(ev inference.Event) {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	if s.printMetadata {
		fmt.Fprintf(s.stderr, "piace %s: debug #%03d %s\n", s.label, seq, describeInferenceEvent(ev))
	}
	if s.dumpDir == "" {
		return
	}
	base := fmt.Sprintf("%03d-%s-%s", seq, strings.ToLower(ev.Method), slugPath(ev.URL))
	s.dump(base+".request", ev.RequestBody)
	s.dump(base+".response", ev.ResponseBody)
}

// describeInferenceEvent renders one inference.Event as a single safe
// line, in the same form as describeEvent. No response body value
// reaches it: TopLevelKeys carries member names only.
func describeInferenceEvent(ev inference.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", ev.Method, ev.URL)
	if ev.Err != nil && ev.StatusCode == 0 {
		fmt.Fprintf(&b, " -> no response after %s: %s", ev.Duration, transport.SafeMessage(ev.Err))
		return b.String()
	}
	fmt.Fprintf(&b, " -> %d in %s (request %d B, response %d B", ev.StatusCode, ev.Duration, ev.RequestBodyBytes, ev.ResponseBodyBytes)
	if ev.ContentType != "" {
		fmt.Fprintf(&b, ", content-type %s", safemeta.Text(ev.ContentType))
	}
	fmt.Fprintf(&b, ", body %s", ev.Shape)
	if ev.Shape == inference.ShapeObject {
		keys := safeDebugKeys(ev.TopLevelKeys)
		if ev.KeysTruncated {
			keys += ",..."
		}
		fmt.Fprintf(&b, ", top-level keys: %s", keys)
	}
	if ev.Err != nil {
		fmt.Fprintf(&b, ", body read error: %s", transport.SafeMessage(ev.Err))
	}
	b.WriteString(")")
	return b.String()
}

// describeEvent renders one Event as a single safe line. Every field it
// prints is metadata; no body content reaches it, since
// transport.Event's TopLevelKeys carries member names only (see
// internal/transport/debug.go).
func describeEvent(ev transport.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", ev.Method, ev.URL)
	if ev.Err != nil {
		fmt.Fprintf(&b, " -> no response after %s: %s", ev.Duration, transport.SafeMessage(ev.Err))
		return b.String()
	}
	fmt.Fprintf(&b, " -> %d in %s", ev.StatusCode, ev.Duration)
	if ev.RequestBodyBytes >= 0 {
		fmt.Fprintf(&b, " (request %d B", ev.RequestBodyBytes)
	} else {
		b.WriteString(" (request unknown size")
	}
	fmt.Fprintf(&b, ", response %d B", ev.ResponseBodyBytes)
	if ev.ContentType != "" {
		fmt.Fprintf(&b, ", content-type %s", safemeta.Text(ev.ContentType))
	}
	fmt.Fprintf(&b, ", body %s", ev.Shape)
	if ev.Shape == transport.ShapeObject {
		keys := safeDebugKeys(ev.TopLevelKeys)
		if ev.KeysTruncated {
			keys += ",..."
		}
		fmt.Fprintf(&b, ", top-level keys: %s", keys)
	}
	b.WriteString(")")
	return b.String()
}

// slugPath reduces a request URL to a short filesystem-safe fragment for
// a dump file name: its path only, with separators and any other
// non-alphanumeric character collapsed to "-". The query string is
// dropped so a PuppetDB query never lands in a file name.
func slugPath(rawURL string) string {
	path := rawURL
	if i := strings.Index(path, "://"); i >= 0 {
		path = path[i+3:]
		if j := strings.Index(path, "/"); j >= 0 {
			path = path[j:]
		} else {
			path = "/"
		}
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	var b strings.Builder
	lastDash := true
	for _, r := range path {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "request"
	}
	if len(slug) > 80 {
		slug = slug[:80]
	}
	return slug
}

func safeDebugKeys(keys []string) string {
	safe := make([]string, 0, len(keys))
	for i, key := range keys {
		if i == 64 {
			break
		}
		safe = append(safe, safemeta.Text(key))
	}
	return strings.Join(safe, ",")
}
