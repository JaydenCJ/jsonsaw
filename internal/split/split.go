// Package split implements the split engine: stream the array at a path
// and emit one compact JSON value per line (JSONL), optionally sharded
// into fixed-size part files. Each element flows token-by-token from the
// reader to the writer; nothing is ever buffered per element, so a
// 100-byte record and a 100 MB record cost the same memory.
package split

import (
	"bufio"
	"fmt"
	"io"

	"github.com/JaydenCJ/jsonsaw/internal/jpath"
	"github.com/JaydenCJ/jsonsaw/internal/stream"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

// Options configures a split run.
type Options struct {
	Path  jpath.Path // where the array lives; empty means the root value
	Skip  int        // skip this many leading elements
	Limit int        // stop after this many written elements; <0 = unlimited
	Chunk int        // elements per part file; 0 = one continuous stream
}

// Stats summarizes what a run produced.
type Stats struct {
	Elements int // elements written
	Parts    int // part files opened (1 for a single stream with output)
}

// NewPart is called each time the engine needs somewhere to write: once
// for a single stream, once per part file when chunking. index is 0-based.
type NewPart func(index int) (io.WriteCloser, error)

// Run splits the document read from r. It validates the whole document
// (including everything after the target array) unless Limit cuts the run
// short, in which case it returns immediately without reading further.
func Run(r io.Reader, opts Options, newPart NewPart) (Stats, error) {
	tz := token.New(r)
	first, err := stream.Navigate(tz, opts.Path)
	if err != nil {
		return Stats{}, err
	}
	if first.Kind != token.BeginArray {
		return Stats{}, &stream.PathError{
			Path: opts.Path.String(),
			Msg:  fmt.Sprintf("expected an array to split, found %s", token.ValueKind(first.Kind)),
		}
	}

	var (
		stats   Stats
		cur     io.WriteCloser
		bw      *bufio.Writer
		inPart  int // elements written to the current part
		skipped int
	)
	closeCur := func() error {
		if cur == nil {
			return nil
		}
		if err := bw.Flush(); err != nil {
			return err
		}
		err := cur.Close()
		cur, bw = nil, nil
		return err
	}

	for {
		if opts.Limit >= 0 && stats.Elements >= opts.Limit {
			// Early exit: with --limit the caller asked for a prefix, so we
			// deliberately do not read (or validate) the rest of the input.
			return stats, closeCur()
		}
		el, err := tz.Next()
		if err != nil {
			return stats, err
		}
		if el.Kind == token.EndArray {
			break
		}
		if skipped < opts.Skip {
			if err := stream.SkipFrom(tz, el); err != nil {
				return stats, err
			}
			skipped++
			continue
		}
		if cur == nil || (opts.Chunk > 0 && inPart == opts.Chunk) {
			if err := closeCur(); err != nil {
				return stats, err
			}
			w, err := newPart(stats.Parts)
			if err != nil {
				return stats, err
			}
			cur = w
			bw = bufio.NewWriterSize(w, 64*1024)
			stats.Parts++
			inPart = 0
		}
		if err := stream.CopyFrom(tz, el, bw); err != nil {
			return stats, err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return stats, err
		}
		stats.Elements++
		inPart++
	}

	if err := closeCur(); err != nil {
		return stats, err
	}
	// Read the remainder (closing brackets of enclosing containers, sibling
	// keys after the array, …) so a truncated or corrupt tail still fails.
	if err := stream.Drain(tz); err != nil {
		return stats, err
	}
	return stats, nil
}
