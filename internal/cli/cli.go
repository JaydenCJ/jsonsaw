// Package cli implements the jsonsaw command-line interface. Run takes
// argv plus the three standard streams and returns an exit code, so the
// whole surface is testable in-process without building a binary.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/JaydenCJ/jsonsaw/internal/join"
	"github.com/JaydenCJ/jsonsaw/internal/jpath"
	"github.com/JaydenCJ/jsonsaw/internal/stream"
	"github.com/JaydenCJ/jsonsaw/internal/token"
	"github.com/JaydenCJ/jsonsaw/internal/version"
)

// Exit codes. Documented in the README; scripts can rely on them.
const (
	ExitOK      = 0 // success
	ExitData    = 1 // invalid JSON, or the path does not resolve
	ExitUsage   = 2 // bad flags or arguments
	ExitRuntime = 3 // I/O failure (missing file, write error, …)
)

// Run dispatches argv (without the program name) and returns the exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return ExitOK
	}
	switch args[0] {
	case "split":
		return runSplit(args[1:], stdin, stdout, stderr)
	case "join":
		return runJoin(args[1:], stdin, stdout, stderr)
	case "count":
		return runCount(args[1:], stdin, stdout, stderr)
	case "paths":
		return runPaths(args[1:], stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "jsonsaw %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "jsonsaw: unknown command %q (try `jsonsaw help`)\n", args[0])
		return ExitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `jsonsaw — saw giant JSON arrays into JSONL and weld them back, in constant memory

Usage:
  jsonsaw split [--path P] [--skip N] [--limit N] [--out FILE|DIR] [--chunk N] [--prefix S] [--quiet] [FILE]
  jsonsaw join  [--path P] [--pretty] [--indent N] [--out FILE] [--quiet] [FILE ...]
  jsonsaw count [--path P] [FILE]
  jsonsaw paths [--depth N] [--format text|json] [FILE]
  jsonsaw version

Input is read from FILE, or from stdin when FILE is omitted or "-".

Paths address a value inside the document: dot-separated object keys plus
bracketed array indexes, e.g.  data.users   results[0].rows   payload."weird.key"

Exit codes: 0 ok · 1 invalid input or unresolvable path · 2 usage error · 3 I/O error
`)
}

// exitFor classifies an error from an engine into an exit code and prints
// it. Data problems (bad JSON, bad path) are 1; everything else — missing
// files, full disks — is 3.
func exitFor(err error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "jsonsaw: %v\n", err)
	var se *token.SyntaxError
	var pe *stream.PathError
	var je *join.SourceError
	if errors.As(err, &se) || errors.As(err, &pe) || errors.As(err, &je) {
		return ExitData
	}
	return ExitRuntime
}

// parsePath wraps jpath.Parse with the usage-error convention: a path the
// user mistyped is exit 2, reported before any input is read.
func parsePath(s string, stderr io.Writer) (jpath.Path, bool) {
	p, err := jpath.Parse(s)
	if err != nil {
		fmt.Fprintf(stderr, "jsonsaw: %v\n", err)
		return nil, false
	}
	return p, true
}
