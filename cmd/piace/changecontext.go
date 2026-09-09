package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/exitcode"
	"github.com/example42/piace/internal/limits"
)

type changeContextFlags struct {
	baseRef    string
	baseRefEnv string
	headRef    string
	headRefEnv string

	titleEnv        string
	titleFile       string
	descriptionEnv  string
	descriptionFile string
}

// runChangeContext writes a change context file describing the repository
// change under test, for `piace explain --change` to read.
//
// This is the one subcommand that invokes git, and it is optional.
// `compare` and `explain` still contact nothing but the compiler,
// PuppetDB and the inference service, and `explain --change` still reads
// a file the caller produced by whatever means, so a repository under a
// different VCS, or a CI system with no checkout at all, describes its
// change exactly as before.
//
// It exists because the alternative is every adopter hand-writing the
// same YAML in shell, and the free-text part of that is the dangerous
// part. There is deliberately no `--title` or `--description` flag: a
// pull request title is written by whoever opened the pull request, and a
// CI system that substitutes one into script text before a shell sees it
// (GitHub's `${{ }}`, Azure's `$( )`) turns a title of `$(curl ...)` into
// arbitrary code execution on a runner that holds the catalog-reader
// identity. Naming the variable instead of passing its value keeps
// attacker-controlled text off the command line entirely, which is also
// why the refs take an `-env` form: a git branch name may legally contain
// a semicolon, a dollar sign and a backtick.
func runChangeContext(args []string, stdout, stderr *os.File) exitcode.Code {
	fs := flag.NewFlagSet("change-context", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f changeContextFlags
	fs.StringVar(&f.baseRef, "base-ref", "", "the ref the change branched from (required, or --base-ref-env)")
	fs.StringVar(&f.baseRefEnv, "base-ref-env", "", "name of an environment variable holding the base ref")
	fs.StringVar(&f.headRef, "head-ref", "HEAD", "the ref under test")
	fs.StringVar(&f.headRefEnv, "head-ref-env", "", "name of an environment variable holding the head ref")
	fs.StringVar(&f.titleEnv, "title-env", "", "name of an environment variable holding the change title")
	fs.StringVar(&f.titleFile, "title-file", "", "path to a file holding the change title")
	fs.StringVar(&f.descriptionEnv, "description-env", "", "name of an environment variable holding the change description")
	fs.StringVar(&f.descriptionFile, "description-file", "", "path to a file holding the change description")
	if err := fs.Parse(args); err != nil {
		return exitcode.OperationalError
	}

	baseRef, err := readRef("base-ref", f.baseRef, f.baseRefEnv)
	if err != nil {
		fmt.Fprintf(stderr, "piace change-context: %s\n", err)
		return exitcode.OperationalError
	}
	if baseRef == "" {
		fmt.Fprintln(stderr, "piace change-context: --base-ref or --base-ref-env is required")
		return exitcode.OperationalError
	}
	headRef, err := readRef("head-ref", f.headRef, f.headRefEnv)
	if err != nil {
		fmt.Fprintf(stderr, "piace change-context: %s\n", err)
		return exitcode.OperationalError
	}

	title, err := readCallerText("title", f.titleEnv, f.titleFile)
	if err != nil {
		fmt.Fprintf(stderr, "piace change-context: %s\n", err)
		return exitcode.OperationalError
	}
	description, err := readCallerText("description", f.descriptionEnv, f.descriptionFile)
	if err != nil {
		fmt.Fprintf(stderr, "piace change-context: %s\n", err)
		return exitcode.OperationalError
	}

	cc, err := changeContextFromGit(baseRef, headRef)
	if err != nil {
		fmt.Fprintf(stderr, "piace change-context: %s\n", err)
		return exitcode.OperationalError
	}
	cc.Title = strings.TrimSpace(title)
	cc.Description = strings.TrimRight(description, "\n")

	if err := assess.EncodeChangeContext(stdout, cc); err != nil {
		fmt.Fprintf(stderr, "piace change-context: %s\n", err)
		return exitcode.OperationalError
	}
	return exitcode.Success
}

// changeContextFromGit reads the change between the merge base of baseRef
// and headRef and headRef itself.
//
// Commit subjects are collected; commit bodies are not, and there is no
// flag to ask for them. A body is unbounded free text written by whoever
// pushed, and it is the part of a repository most likely to carry a
// customer name, a ticket paste or a credential someone meant to delete.
// The reader refuses a `body` key outright, so this holds at both ends.
func changeContextFromGit(baseRef, headRef string) (assess.ChangeContext, error) {
	cc := assess.ChangeContext{Present: true, BaseRef: baseRef}

	mergeBase, err := gitOutput("merge-base", endOfOptions, baseRef, headRef)
	if err != nil {
		return cc, err
	}
	mergeBase = strings.TrimSpace(mergeBase)

	// The one call with no endOfOptions marker: `git rev-parse` echoes
	// arguments it cannot interpret, and the marker itself is one of them,
	// so passing it here puts a literal "--end-of-options" line above the
	// branch name in the output this reads. validateRef, which refuses a
	// leading "-" before any git command runs, is the control that covers
	// this call.
	name, err := gitOutput("rev-parse", "--abbrev-ref", headRef)
	if err != nil {
		return cc, err
	}
	cc.HeadRef = strings.TrimSpace(name)

	// -z so records are NUL-separated and \x1f between fields: a commit
	// subject may legally contain a tab or a newline, and splitting on
	// either would invent commits that do not exist.
	log, err := gitOutput("log", "-z", "--format=%H%x1f%s%x1f%an", endOfOptions, mergeBase+".."+headRef)
	if err != nil {
		return cc, err
	}
	for _, record := range splitNUL(log) {
		fields := strings.Split(record, "\x1f")
		if len(fields) != 3 {
			return cc, fmt.Errorf("git log returned an unreadable record %q", record)
		}
		cc.Commits = append(cc.Commits, assess.Commit{SHA: fields[0], Subject: fields[1], Author: fields[2]})
	}

	// -z again, for the same reason: a path may contain a newline, and
	// without it git would quote and escape such a path instead.
	paths, err := gitOutput("diff", "--name-only", "-z", endOfOptions, mergeBase, headRef)
	if err != nil {
		return cc, err
	}
	cc.ChangedPaths = splitNUL(paths)

	return cc, nil
}

// endOfOptions is passed to the git invocations above that accept it,
// immediately before the first ref. It tells git's revision parser that nothing
// after it is an option, however it is spelled.
//
// The refs here are attacker-supplied in the case this command exists
// for: on a fork pull request the base and head refs are branch names
// whoever opened the change chose. A ref of `--output=<path>` reaching
// `git diff` as a positional argument is an arbitrary file write on the
// runner that holds the catalog-reader identity. As it happens the
// `merge-base` call runs first and rejects an unknown option, so that
// particular value dead-ends before `diff` is reached, but a chain that
// depends on the argument order of the first of four commands is not a
// control. validateRef refuses a leading "-" outright and this refuses
// what a future reordering might otherwise let through.
//
// It requires git 2.24 (November 2019). A `piace change-context` run
// against anything older fails with git's own "unknown option" text
// rather than silently dropping the guard, which is the right way round:
// `explain --change` reads a file produced by any means, so a site on an
// older git writes the same YAML without this subcommand.
const endOfOptions = "--end-of-options"

func splitNUL(s string) []string {
	var out []string
	for _, field := range strings.Split(s, "\x00") {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

// gitTimeout bounds one git invocation. Every command this file runs
// reads local history and should answer immediately; a repository large
// enough, or a filesystem slow enough, to need longer than this is one
// where `explain --change` should be given a file written some other
// way rather than left waiting inside a CI job.
const gitTimeout = 30 * time.Second

// gitOutput runs one git command in the working directory and returns its
// standard output. git's own stderr is carried into the error, because
// "unknown revision or path not in the working tree" and "does not have
// any commits yet" are the two failures a caller actually hits and
// neither is guessable from an exit status.
//
// Both the time it may take and the output it may produce are bounded.
// `git log` in a repository with a hundred thousand commits between two
// refs produces megabytes of subjects, and the change context that
// results is disclosed to an inference service; collecting all of it
// first and deciding afterwards is the wrong order.
func gitOutput(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	var stdout, stderr boundedBuffer
	stdout.max, stderr.max = limits.ChangeContext, 8*1024
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.overflowed {
		return "", fmt.Errorf("git %s: produced more than the %d bytes a change context may carry",
			strings.Join(args, " "), limits.ChangeContext)
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: did not finish within %s", strings.Join(args, " "), gitTimeout)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// boundedBuffer collects at most max bytes and records that it stopped,
// so a caller reports truncation rather than discovering it as missing
// content. Writes past the limit are discarded and reported as written,
// which keeps the child process writing into a pipe that is being
// drained instead of blocking it.
type boundedBuffer struct {
	buf        bytes.Buffer
	max        int
	overflowed bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) <= room {
			return b.buf.Write(p)
		}
		b.buf.Write(p[:room])
	}
	b.overflowed = true
	return len(p), nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }

// readRef reads a ref from exactly one of the two references the caller
// may give, the same rule the services file follows for a credential,
// and validates it before it can become a git argument.
func readRef(flagName, value, env string) (string, error) {
	switch {
	case value != "" && value != "HEAD" && env != "":
		return "", fmt.Errorf("set --%s or --%s-env, not both", flagName, flagName)
	case env != "":
		v := os.Getenv(env)
		if v == "" {
			return "", fmt.Errorf("--%s-env: environment variable %s is unset or empty", flagName, env)
		}
		return v, validateRef(flagName, v)
	default:
		return value, validateRef(flagName, value)
	}
}

// validateRef refuses the ref shapes that would be read as something
// other than a ref by the git commands changeContextFromGit runs.
//
// A leading "-" is the whole point: a ref is passed to git as a
// positional argument, and git's option parser does not care that the
// caller meant a branch name. Git itself forbids a ref name beginning
// with "-", so nothing legitimate is refused here.
//
// A NUL byte is refused because exec would fail on it anyway, with a
// message that says nothing about which flag was wrong. Everything else
// a branch name may legally contain, semicolons, dollar signs and
// backticks among it, is passed through untouched: this command never
// builds a shell string, which is why those characters are safe here and
// are exactly what makes a `--title` flag unsafe (see the doc comment on
// runChangeContext).
func validateRef(flagName, ref string) error {
	if ref == "" {
		return nil
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("--%s: a ref may not begin with \"-\"", flagName)
	}
	if strings.ContainsRune(ref, 0) {
		return fmt.Errorf("--%s: a ref may not contain a NUL byte", flagName)
	}
	return nil
}

// readCallerText reads one untrusted free-text field by reference. An
// unset variable or an empty file is not an error: a change with no
// description is ordinary, and refusing to describe a change because
// nobody wrote a description would be a strange way to fail a pipeline.
func readCallerText(flagName, env, file string) (string, error) {
	switch {
	case env != "" && file != "":
		return "", fmt.Errorf("set --%s-env or --%s-file, not both", flagName, flagName)
	case env != "":
		return os.Getenv(env), nil
	case file != "":
		raw, err := readBoundedLocalFile(file, limits.ChangeContext)
		if err != nil {
			return "", fmt.Errorf("reading --%s-file: %w", flagName, err)
		}
		return string(raw), nil
	default:
		return "", nil
	}
}

// readBoundedLocalFile reads at most max bytes from path, refusing a
// larger file rather than allocating it. A change-context title or
// description is a line or a paragraph; a path naming something else is
// a mistake worth reporting before its contents are disclosed to an
// inference service.
func readBoundedLocalFile(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, fmt.Errorf("%s exceeds the %d-byte limit", path, max)
	}
	return data, nil
}
