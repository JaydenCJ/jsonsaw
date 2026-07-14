// Subcommand implementations: flag parsing, input/output plumbing, and the
// human-facing summary lines. All real work happens in the engine packages
// (split, join, inspect, stream); nothing here touches JSON directly.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/JaydenCJ/jsonsaw/internal/inspect"
	"github.com/JaydenCJ/jsonsaw/internal/join"
	"github.com/JaydenCJ/jsonsaw/internal/split"
	"github.com/JaydenCJ/jsonsaw/internal/stream"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

// newFlagSet builds a FlagSet that reports its own errors to stderr and
// never calls os.Exit.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("jsonsaw "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// openInput resolves the optional positional FILE argument: a path opens
// the file, "-" or nothing means stdin. The returned name labels errors.
func openInput(args []string, stdin io.Reader, stderr io.Writer) (io.ReadCloser, string, int) {
	switch {
	case len(args) > 1:
		fmt.Fprintf(stderr, "jsonsaw: expected at most one input file, got %d\n", len(args))
		return nil, "", ExitUsage
	case len(args) == 0 || args[0] == "-":
		return io.NopCloser(stdin), "stdin", ExitOK
	default:
		f, err := os.Open(args[0])
		if err != nil {
			fmt.Fprintf(stderr, "jsonsaw: %v\n", err)
			return nil, "", ExitRuntime
		}
		return f, args[0], ExitOK
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// plural renders a count with its unit, so summaries never say
// "1 elements". Every unit jsonsaw uses pluralizes with a plain "s".
func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func runSplit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("split", stderr)
	pathStr := fs.String("path", "", "path to the array to split (default: the root value)")
	skip := fs.Int("skip", 0, "skip the first N elements")
	limit := fs.Int("limit", -1, "stop after N elements (default: all)")
	out := fs.String("out", "", "output file, or output directory with --chunk (default: stdout)")
	chunk := fs.Int("chunk", 0, "elements per part file; requires --out DIR")
	prefix := fs.String("prefix", "part-", "part file name prefix with --chunk")
	quiet := fs.Bool("quiet", false, "suppress the summary line on stderr")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if *skip < 0 {
		fmt.Fprintln(stderr, "jsonsaw: --skip must be >= 0")
		return ExitUsage
	}
	if *limit < -1 {
		fmt.Fprintln(stderr, "jsonsaw: --limit must be >= 0")
		return ExitUsage
	}
	if *chunk < 0 {
		fmt.Fprintln(stderr, "jsonsaw: --chunk must be >= 1")
		return ExitUsage
	}
	if *chunk > 0 && *out == "" {
		fmt.Fprintln(stderr, "jsonsaw: --chunk requires --out DIR")
		return ExitUsage
	}
	path, ok := parsePath(*pathStr, stderr)
	if !ok {
		return ExitUsage
	}

	in, _, code := openInput(fs.Args(), stdin, stderr)
	if code != ExitOK {
		return code
	}
	defer in.Close()

	newPart := func(index int) (io.WriteCloser, error) {
		switch {
		case *chunk > 0:
			if index == 0 {
				if err := os.MkdirAll(*out, 0o755); err != nil {
					return nil, err
				}
			}
			return os.Create(filepath.Join(*out, fmt.Sprintf("%s%05d.jsonl", *prefix, index)))
		case *out != "":
			return os.Create(*out)
		default:
			return nopWriteCloser{stdout}, nil
		}
	}

	stats, err := split.Run(in, split.Options{Path: path, Skip: *skip, Limit: *limit, Chunk: *chunk}, newPart)
	if err != nil {
		return exitFor(err, stderr)
	}
	if !*quiet {
		if *chunk > 0 {
			fmt.Fprintf(stderr, "jsonsaw split: %s written across %s\n", plural(stats.Elements, "element"), plural(stats.Parts, "part file"))
		} else {
			fmt.Fprintf(stderr, "jsonsaw split: %s written\n", plural(stats.Elements, "element"))
		}
	}
	return ExitOK
}

func runJoin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("join", stderr)
	pathStr := fs.String("path", "", "wrap the array under these object keys, e.g. data.users")
	pretty := fs.Bool("pretty", false, "pretty-print the output")
	indent := fs.Int("indent", 2, "spaces per indent level with --pretty")
	out := fs.String("out", "", "output file (default: stdout)")
	quiet := fs.Bool("quiet", false, "suppress the summary line on stderr")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if *indent < 1 || *indent > 16 {
		fmt.Fprintln(stderr, "jsonsaw: --indent must be between 1 and 16")
		return ExitUsage
	}
	path, ok := parsePath(*pathStr, stderr)
	if !ok {
		return ExitUsage
	}
	for _, seg := range path {
		if seg.IsIndex {
			fmt.Fprintln(stderr, "jsonsaw: join --path takes object keys only (no [N] indexes)")
			return ExitUsage
		}
	}

	var sources []join.Source
	files := fs.Args()
	if len(files) == 0 {
		sources = append(sources, join.Source{Name: "stdin", R: stdin})
	}
	for _, name := range files {
		if name == "-" {
			sources = append(sources, join.Source{Name: "stdin", R: stdin})
			continue
		}
		f, err := os.Open(name)
		if err != nil {
			fmt.Fprintf(stderr, "jsonsaw: %v\n", err)
			return ExitRuntime
		}
		defer f.Close()
		sources = append(sources, join.Source{Name: name, R: f})
	}

	var w io.Writer = stdout
	var outFile *os.File
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "jsonsaw: %v\n", err)
			return ExitRuntime
		}
		outFile = f
		w = f
	}

	unit := ""
	if *pretty {
		unit = strings.Repeat(" ", *indent)
	}
	stats, err := join.Run(sources, w, join.Options{Path: path, Indent: unit})
	if err != nil {
		if outFile != nil {
			outFile.Close()
		}
		return exitFor(err, stderr)
	}
	// Close is part of the write: some filesystems only surface write
	// failures here, and silently truncated output must not exit 0.
	if outFile != nil {
		if err := outFile.Close(); err != nil {
			fmt.Fprintf(stderr, "jsonsaw: %v\n", err)
			return ExitRuntime
		}
	}
	if !*quiet {
		fmt.Fprintf(stderr, "jsonsaw join: %s from %s\n", plural(stats.Elements, "element"), plural(stats.Sources, "source"))
	}
	return ExitOK
}

func runCount(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("count", stderr)
	pathStr := fs.String("path", "", "path to the array to count (default: the root value)")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	path, ok := parsePath(*pathStr, stderr)
	if !ok {
		return ExitUsage
	}
	in, _, code := openInput(fs.Args(), stdin, stderr)
	if code != ExitOK {
		return code
	}
	defer in.Close()

	tz := token.New(in)
	first, err := stream.Navigate(tz, path)
	if err != nil {
		return exitFor(err, stderr)
	}
	n, err := inspect.Count(tz, first, path.String())
	if err != nil {
		return exitFor(err, stderr)
	}
	if err := stream.Drain(tz); err != nil {
		return exitFor(err, stderr)
	}
	fmt.Fprintln(stdout, n)
	return ExitOK
}

func runPaths(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("paths", stderr)
	depth := fs.Int("depth", 2, "report paths down to this depth (root is 0)")
	format := fs.String("format", "text", "output format: text or json")
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	if *depth < 0 {
		fmt.Fprintln(stderr, "jsonsaw: --depth must be >= 0")
		return ExitUsage
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "jsonsaw: unknown --format %q (want text or json)\n", *format)
		return ExitUsage
	}
	in, _, code := openInput(fs.Args(), stdin, stderr)
	if code != ExitOK {
		return code
	}
	defer in.Close()

	entries, err := inspect.Inspect(in, *depth)
	if err != nil {
		return exitFor(err, stderr)
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			return exitFor(err, stderr)
		}
		return ExitOK
	}
	width := 0
	for _, e := range entries {
		if len(e.Path) > width {
			width = len(e.Path)
		}
	}
	for _, e := range entries {
		fmt.Fprintf(stdout, "%-*s  %s\n", width, e.Path, e.Label())
	}
	return ExitOK
}
