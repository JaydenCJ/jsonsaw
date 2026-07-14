// Tests for the split engine: JSONL output, skip/limit windows, chunked
// part files (via an in-memory part recorder), and the validation policy —
// a corrupt tail must fail unless --limit deliberately cut the read short.
package split

import (
	"io"
	"strings"
	"testing"

	"github.com/JaydenCJ/jsonsaw/internal/jpath"
	"github.com/JaydenCJ/jsonsaw/internal/stream"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

// recorder captures every part the engine opens, in memory.
type recorder struct {
	parts []*strings.Builder
}

func (r *recorder) newPart(index int) (io.WriteCloser, error) {
	if index != len(r.parts) {
		panic("parts opened out of order")
	}
	b := &strings.Builder{}
	r.parts = append(r.parts, b)
	return nopCloser{b}, nil
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func run(t *testing.T, input, path string, opts Options) (Stats, *recorder, error) {
	t.Helper()
	p, err := jpath.Parse(path)
	if err != nil {
		t.Fatalf("Parse(%q): %v", path, err)
	}
	opts.Path = p
	if opts.Limit == 0 {
		opts.Limit = -1 // tests opt into limits explicitly
	}
	rec := &recorder{}
	stats, err := Run(strings.NewReader(input), opts, rec.newPart)
	return stats, rec, err
}

func TestSplitRootArrayToJSONL(t *testing.T) {
	stats, rec, err := run(t, `[{"id":1}, {"id":2}, 3, "four"]`, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"id\":1}\n{\"id\":2}\n3\n\"four\"\n"
	if rec.parts[0].String() != want {
		t.Fatalf("got %q want %q", rec.parts[0].String(), want)
	}
	if stats.Elements != 4 || stats.Parts != 1 {
		t.Fatalf("stats %+v", stats)
	}
}

func TestSplitAtPath(t *testing.T) {
	in := `{"meta":{"page":1},"data":{"users":[{"id":1},{"id":2}]},"next":null}`
	stats, rec, err := run(t, in, "data.users", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rec.parts[0].String() != "{\"id\":1}\n{\"id\":2}\n" || stats.Elements != 2 {
		t.Fatalf("got %q, stats %+v", rec.parts[0].String(), stats)
	}
}

func TestSplitElementsAreOneLineEach(t *testing.T) {
	// Input strings may contain escaped newlines but never raw ones, so
	// every output line is exactly one element — the JSONL contract.
	in := "[\n  {\"txt\": \"line1\\nline2\"},\n  {\"txt\": \"ok\"}\n]"
	_, rec, err := run(t, in, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := rec.parts[0].String()
	if strings.Count(out, "\n") != 2 {
		t.Fatalf("expected exactly 2 lines, got %q", out)
	}
	if !strings.Contains(out, `"line1\nline2"`) {
		t.Fatalf("escape not preserved: %q", out)
	}
}

func TestSplitSkipAndLimitWindows(t *testing.T) {
	cases := []struct {
		opts Options
		want string
	}{
		{Options{Skip: 3}, "4\n5\n"},              // skip a prefix
		{Options{Limit: 2}, "1\n2\n"},             // take a prefix
		{Options{Skip: 1, Limit: 3}, "2\n3\n4\n"}, // a window in the middle
	}
	for _, c := range cases {
		_, rec, err := run(t, `[1,2,3,4,5]`, "", c.opts)
		if err != nil {
			t.Fatal(err)
		}
		if rec.parts[0].String() != c.want {
			t.Fatalf("%+v: got %q want %q", c.opts, rec.parts[0].String(), c.want)
		}
	}
	// Limit 0 is a real limit (the run helper treats 0 as "no limit", so
	// call Run directly): nothing is written and no part is opened.
	rec := &recorder{}
	stats, err := Run(strings.NewReader(`[1,2,3]`), Options{Limit: 0}, rec.newPart)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Elements != 0 || len(rec.parts) != 0 {
		t.Fatalf("stats %+v, parts %d", stats, len(rec.parts))
	}
}

func TestSplitChunking(t *testing.T) {
	stats, rec, err := run(t, `[1,2,3,4,5,6,7]`, "", Options{Chunk: 3})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Parts != 3 || stats.Elements != 7 {
		t.Fatalf("stats %+v", stats)
	}
	want := []string{"1\n2\n3\n", "4\n5\n6\n", "7\n"}
	for i := range want {
		if got := rec.parts[i].String(); got != want[i] {
			t.Fatalf("part %d: got %q want %q", i, got, want[i])
		}
	}
	// 6 elements at chunk 3 must make exactly 2 parts, not an empty third.
	stats, _, err = run(t, `[1,2,3,4,5,6]`, "", Options{Chunk: 3})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Parts != 2 {
		t.Fatalf("stats %+v; an empty trailing part was opened", stats)
	}
}

func TestSplitEmptyArrayOpensNoParts(t *testing.T) {
	stats, rec, err := run(t, `{"users":[]}`, "users", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Elements != 0 || len(rec.parts) != 0 {
		t.Fatalf("stats %+v, parts %d", stats, len(rec.parts))
	}
}

func TestSplitNonArrayTargetFails(t *testing.T) {
	_, _, err := run(t, `{"users":{"a":1}}`, "users", Options{})
	pe, ok := err.(*stream.PathError)
	if !ok || !strings.Contains(pe.Msg, "expected an array to split, found object") {
		t.Fatalf("got %v", err)
	}
}

func TestSplitInvalidElementFails(t *testing.T) {
	_, _, err := run(t, `[{"ok":1},{"bad":}]`, "", Options{})
	if _, ok := err.(*token.SyntaxError); !ok {
		t.Fatalf("got %v, want *token.SyntaxError", err)
	}
}

func TestSplitValidatesDocumentTail(t *testing.T) {
	// The array itself is fine but the document is truncated after it.
	// Without --limit the engine must read to EOF and fail loudly.
	_, _, err := run(t, `{"users":[1,2],"cursor":`, "users", Options{})
	if err == nil {
		t.Fatal("truncated tail accepted")
	}
}

func TestSplitLimitSkipsTailValidation(t *testing.T) {
	// With --limit the caller asked for a prefix of a possibly enormous
	// array; jsonsaw stops reading immediately, so a later corruption is
	// intentionally not seen.
	stats, rec, err := run(t, `{"users":[1,2,3,GARBAGE`, "users", Options{Limit: 2})
	if err != nil {
		t.Fatalf("early exit should not read the tail: %v", err)
	}
	if stats.Elements != 2 || rec.parts[0].String() != "1\n2\n" {
		t.Fatalf("stats %+v, got %q", stats, rec.parts[0].String())
	}
}

func TestSplitStopsAtNestedArrayOfTarget(t *testing.T) {
	// Elements that are themselves arrays must not terminate the split.
	_, rec, err := run(t, `[[1,2],[3],[]]`, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rec.parts[0].String() != "[1,2]\n[3]\n[]\n" {
		t.Fatalf("got %q", rec.parts[0].String())
	}
}
