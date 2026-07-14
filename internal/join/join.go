// Package join implements the inverse of split: read a stream of JSON
// values (JSONL, or any whitespace-separated sequence) and weld them back
// into a single JSON array — optionally wrapped under a path of object
// keys, optionally pretty-printed. Elements are re-emitted token-by-token,
// so joining a 50 GB stream costs megabytes, not gigabytes.
package join

import (
	"bufio"
	"fmt"
	"io"

	"github.com/JaydenCJ/jsonsaw/internal/jpath"
	"github.com/JaydenCJ/jsonsaw/internal/stream"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

// Options configures a join run.
type Options struct {
	Path   jpath.Path // wrap the array under these object keys; keys only
	Indent string     // "" = compact; otherwise the pretty-print unit
}

// Stats summarizes what a run consumed.
type Stats struct {
	Elements int
	Sources  int
}

// Source is one named input; the name prefixes any error so a bad line in
// the third of ten shards is attributable.
type Source struct {
	Name string
	R    io.Reader
}

// SourceError wraps a syntax error with the source it came from. jsonsaw
// maps it to exit code 1, same as a plain syntax error.
type SourceError struct {
	Name string
	Err  error
}

func (e *SourceError) Error() string { return fmt.Sprintf("%s: %v", e.Name, e.Err) }
func (e *SourceError) Unwrap() error { return e.Err }

// Run joins all sources, in order, into one array written to w. The output
// always ends with a newline.
func Run(sources []Source, w io.Writer, opts Options) (Stats, error) {
	for _, seg := range opts.Path {
		if seg.IsIndex {
			return Stats{}, &stream.PathError{
				Path: opts.Path.String(),
				Msg:  "join can only wrap under object keys, not array indexes",
			}
		}
	}

	bw := bufio.NewWriterSize(w, 64*1024)
	pretty := opts.Indent != ""
	base := len(opts.Path) + 1 // elements sit inside the wrappers + the array

	newline := func(n int) error {
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if _, err := bw.WriteString(opts.Indent); err != nil {
				return err
			}
		}
		return nil
	}

	// Opening wrappers: {"a":{"b":[  — or their indented form.
	for i, seg := range opts.Path {
		if err := bw.WriteByte('{'); err != nil {
			return Stats{}, err
		}
		if pretty {
			if err := newline(i + 1); err != nil {
				return Stats{}, err
			}
		}
		if _, err := bw.Write(token.Quote(seg.Key)); err != nil {
			return Stats{}, err
		}
		colon := ":"
		if pretty {
			colon = ": "
		}
		if _, err := bw.WriteString(colon); err != nil {
			return Stats{}, err
		}
	}
	if err := bw.WriteByte('['); err != nil {
		return Stats{}, err
	}

	stats := Stats{Sources: len(sources)}
	for _, src := range sources {
		tz := token.NewSequence(src.R)
		for {
			first, err := tz.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return stats, &SourceError{src.Name, err}
			}
			if stats.Elements > 0 {
				if err := bw.WriteByte(','); err != nil {
					return stats, err
				}
			}
			if pretty {
				if err := newline(base); err != nil {
					return stats, err
				}
			}
			if err := stream.WriteFrom(tz, first, bw, opts.Indent, base); err != nil {
				return stats, &SourceError{src.Name, err}
			}
			stats.Elements++
		}
	}

	// Closing brackets, innermost array first.
	if pretty && stats.Elements > 0 {
		if err := newline(base - 1); err != nil {
			return stats, err
		}
	}
	if err := bw.WriteByte(']'); err != nil {
		return stats, err
	}
	for i := len(opts.Path) - 1; i >= 0; i-- {
		if pretty {
			if err := newline(i); err != nil {
				return stats, err
			}
		}
		if err := bw.WriteByte('}'); err != nil {
			return stats, err
		}
	}
	if err := bw.WriteByte('\n'); err != nil {
		return stats, err
	}
	return stats, bw.Flush()
}
