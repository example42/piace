package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/example42/piace/internal/assess"
	"github.com/example42/piace/internal/exitcode"
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

	mergeBase, err := gitOutput("merge-base", baseRef, headRef)
	if err != nil {
		return cc, err
	}
	mergeBase = strings.TrimSpace(mergeBase)

	name, err := gitOutput("rev-parse", "--abbrev-ref", headRef)
	if err != nil {
		return cc, err
	}
	cc.HeadRef = strings.TrimSpace(name)

	// -z so records are NUL-separated and \x1f between fields: a commit
	// subject may legally contain a tab or a newline, and splitting on
	// either would invent commits that do not exist.
	log, err := gitOutput("log", "-z", "--format=%H%x1f%s%x1f%an", mergeBase+".."+headRef)
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
	paths, err := gitOutput("diff", "--name-only", "-z", mergeBase, headRef)
	if err != nil {
		return cc, err
	}
	cc.ChangedPaths = splitNUL(paths)

	return cc, nil
}

func splitNUL(s string) []string {
	var out []string
	for _, field := range strings.Split(s, "\x00") {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

// gitOutput runs one git command in the working directory and returns its
// standard output. git's own stderr is carried into the error, because
// "unknown revision or path not in the working tree" and "does not have
// any commits yet" are the two failures a caller actually hits and
// neither is guessable from an exit status.
func gitOutput(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// readRef reads a ref from exactly one of the two references the caller
// may give, the same rule the services file follows for a credential.
func readRef(flagName, value, env string) (string, error) {
	switch {
	case value != "" && value != "HEAD" && env != "":
		return "", fmt.Errorf("set --%s or --%s-env, not both", flagName, flagName)
	case env != "":
		v := os.Getenv(env)
		if v == "" {
			return "", fmt.Errorf("--%s-env: environment variable %s is unset or empty", flagName, env)
		}
		return v, nil
	default:
		return value, nil
	}
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
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading --%s-file: %w", flagName, err)
		}
		return string(raw), nil
	default:
		return "", nil
	}
}
