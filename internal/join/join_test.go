// Tests for the join engine: exact compact and pretty output, path
// wrapping, multi-source concatenation, error attribution, and the
// lossless split→join round trip that is jsonsaw's core promise.
package join

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/JaydenCJ/jsonsaw/internal/jpath"
	"github.com/JaydenCJ/jsonsaw/internal/split"
	"github.com/JaydenCJ/jsonsaw/internal/stream"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

func runJoin(t *testing.T, inputs []string, path, indent string) (string, Stats, error) {
	t.Helper()
	p, err := jpath.Parse(path)
	if err != nil {
		t.Fatalf("Parse(%q): %v", path, err)
	}
	var sources []Source
	for i, in := range inputs {
		sources = append(sources, Source{Name: names(i), R: strings.NewReader(in)})
	}
	var b strings.Builder
	stats, err := Run(sources, &b, Options{Path: p, Indent: indent})
	return b.String(), stats, err
}

func names(i int) string {
	return []string{"a.jsonl", "b.jsonl", "c.jsonl"}[i]
}

func TestJoinCompact(t *testing.T) {
	out, stats, err := runJoin(t, []string{"{\"id\":1}\n{\"id\":2}\n3\n"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != `[{"id":1},{"id":2},3]`+"\n" {
		t.Fatalf("got %q", out)
	}
	if stats.Elements != 3 || stats.Sources != 1 {
		t.Fatalf("stats %+v", stats)
	}
}

func TestJoinEmptyInputMakesEmptyArray(t *testing.T) {
	// Compact and pretty agree: no elements means a tight [] either way.
	for _, indent := range []string{"", "  "} {
		out, stats, err := runJoin(t, []string{"\n\n"}, "", indent)
		if err != nil {
			t.Fatal(err)
		}
		if out != "[]\n" || stats.Elements != 0 {
			t.Fatalf("indent %q: got %q, stats %+v", indent, out, stats)
		}
	}
}

func TestJoinPretty(t *testing.T) {
	out, _, err := runJoin(t, []string{"{\"id\":1}\n2\n"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"id\": 1\n  },\n  2\n]\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestJoinWrapPathCompact(t *testing.T) {
	out, _, err := runJoin(t, []string{"1\n2\n"}, "data.users", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"data":{"users":[1,2]}}`+"\n" {
		t.Fatalf("got %q", out)
	}
	// Wrapper keys are JSON-encoded, so awkward names survive.
	out, _, err = runJoin(t, []string{"1\n"}, `"weird.key"`, "")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"weird.key":[1]}`+"\n" {
		t.Fatalf("got %q", out)
	}
}

func TestJoinWrapPathPretty(t *testing.T) {
	out, _, err := runJoin(t, []string{"1\n"}, "data.users", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"data\": {\n    \"users\": [\n      1\n    ]\n  }\n}\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestJoinRejectsIndexInWrapPath(t *testing.T) {
	_, _, err := runJoin(t, []string{"1\n"}, "data[0]", "")
	var pe *stream.PathError
	if !errors.As(err, &pe) {
		t.Fatalf("got %v, want PathError", err)
	}
}

func TestJoinMultipleSourcesInOrder(t *testing.T) {
	out, stats, err := runJoin(t, []string{"1\n2\n", "3\n", "4\n5\n"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "[1,2,3,4,5]\n" || stats.Sources != 3 || stats.Elements != 5 {
		t.Fatalf("got %q, stats %+v", out, stats)
	}
}

func TestJoinSkipsBlankLinesAndIndentation(t *testing.T) {
	out, _, err := runJoin(t, []string{"  {\"a\":1}\n\n\t2\n"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != `[{"a":1},2]`+"\n" {
		t.Fatalf("got %q", out)
	}
}

func TestJoinErrorNamesSourceAndLine(t *testing.T) {
	_, _, err := runJoin(t, []string{"1\n", "2\nnot json\n"}, "", "")
	var se *SourceError
	if !errors.As(err, &se) || se.Name != "b.jsonl" {
		t.Fatalf("got %v, want SourceError from b.jsonl", err)
	}
	var syn *token.SyntaxError
	if !errors.As(err, &syn) || syn.Line != 2 {
		t.Fatalf("got %v, want line 2 within the source", err)
	}
}

func TestJoinPreservesElementBytes(t *testing.T) {
	line := `{"s":"a\nb","n":1e2,"u":"😀"}`
	out, _, err := runJoin(t, []string{line + "\n"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "["+line+"]\n" {
		t.Fatalf("got %q", out)
	}
}

// TestSplitJoinRoundTrip is the flagship property: split | join returns
// the original array, byte-for-byte in compact form.
func TestSplitJoinRoundTrip(t *testing.T) {
	original := `{"data":{"users":[{"id":1,"bio":"aé\nb"},{"id":2,"scores":[1e2,0.50]},{"id":3,"meta":{}}]}}`
	p, _ := jpath.Parse("data.users")

	var jsonl strings.Builder
	newPart := func(int) (io.WriteCloser, error) { return nopCloser{&jsonl}, nil }
	if _, err := split.Run(strings.NewReader(original), split.Options{Path: p, Limit: -1}, newPart); err != nil {
		t.Fatal(err)
	}

	var rejoined strings.Builder
	stats, err := Run([]Source{{Name: "pipe", R: strings.NewReader(jsonl.String())}}, &rejoined, Options{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"data":{"users":[{"id":1,"bio":"aé\nb"},{"id":2,"scores":[1e2,0.50]},{"id":3,"meta":{}}]}}` + "\n"
	if rejoined.String() != want {
		t.Fatalf("round trip drifted:\ngot  %q\nwant %q", rejoined.String(), want)
	}
	if stats.Elements != 3 {
		t.Fatalf("stats %+v", stats)
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
