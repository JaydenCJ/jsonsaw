// Tests for value-level streaming: compact copies must be byte-faithful
// (the whole point of jsonsaw is that split|join is lossless), pretty
// printing must be stable, and navigation must skip siblings correctly.
package stream

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/jsonsaw/internal/jpath"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

// compact runs CopyFrom over the first value of input.
func compact(t *testing.T, input string) string {
	t.Helper()
	tz := token.New(strings.NewReader(input))
	first, err := tz.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	var b strings.Builder
	if err := CopyFrom(tz, first, &b); err != nil {
		t.Fatalf("CopyFrom(%q): %v", input, err)
	}
	return b.String()
}

// pretty runs WriteFrom with two-space indentation at base depth 0.
func pretty(t *testing.T, input string) string {
	t.Helper()
	tz := token.New(strings.NewReader(input))
	first, err := tz.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	var b strings.Builder
	if err := WriteFrom(tz, first, &b, "  ", 0); err != nil {
		t.Fatalf("WriteFrom(%q): %v", input, err)
	}
	return b.String()
}

func TestCopyCompactsWhitespace(t *testing.T) {
	in := "{\n  \"a\" : [ 1 , 2 ],\n  \"b\" : { }\n}"
	if got := compact(t, in); got != `{"a":[1,2],"b":{}}` {
		t.Fatalf("got %s", got)
	}
}

func TestCopyPreservesSourceBytes(t *testing.T) {
	// Escapes must survive verbatim: \u00e9 must NOT become é (nor the
	// reverse), and 1e2 must not become 100 — otherwise checksums over
	// split output stop matching the source.
	for _, in := range []string{
		`["\u00e9","é","a\nb","😀"]`,
		`[1e2,0.50,-0,12345678901234567890]`,
	} {
		if got := compact(t, in); got != in {
			t.Fatalf("got %s want %s", got, in)
		}
	}
}

func TestCopyScalarRootAndDeepNesting(t *testing.T) {
	if got := compact(t, `  42 `); got != "42" {
		t.Fatalf("got %s", got)
	}
	in := `{"a":{"b":{"c":[[[{"d":null}]]]}}}`
	if got := compact(t, in); got != in {
		t.Fatalf("got %s", got)
	}
}

func TestPrettyOutput(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1,"b":"x"}`:   "{\n  \"a\": 1,\n  \"b\": \"x\"\n}",
		`{"a":[1,{"b":2}]}`: "{\n  \"a\": [\n    1,\n    {\n      \"b\": 2\n    }\n  ]\n}",
		`"solo"`:            `"solo"`,
	} {
		if got := pretty(t, in); got != want {
			t.Fatalf("pretty(%s):\ngot:\n%s\nwant:\n%s", in, got, want)
		}
	}
}

func TestPrettyEmptyContainersStayTight(t *testing.T) {
	got := pretty(t, `{"a":{},"b":[]}`)
	want := "{\n  \"a\": {},\n  \"b\": []\n}"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrettyBaseDepthIndentsContinuationLines(t *testing.T) {
	// base=2 is what join --pretty uses for elements below a one-key
	// wrapper: the first line starts at the cursor, continuations indent.
	tz := token.New(strings.NewReader(`{"a":1}`))
	first, _ := tz.Next()
	var b strings.Builder
	if err := WriteFrom(tz, first, &b, "  ", 2); err != nil {
		t.Fatal(err)
	}
	want := "{\n      \"a\": 1\n    }"
	if b.String() != want {
		t.Fatalf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestSkipFromConsumesExactlyOneValue(t *testing.T) {
	tz := token.New(strings.NewReader(`[{"skip":[1,2,{"deep":true}]},"next"]`))
	tz.Next() // [
	first, _ := tz.Next()
	if err := SkipFrom(tz, first); err != nil {
		t.Fatal(err)
	}
	tok, err := tz.Next()
	if err != nil || tok.Kind != token.String || string(tok.Bytes) != `"next"` {
		t.Fatalf("after skip got %v %q %v", tok.Kind, tok.Bytes, err)
	}
}

func navigate(t *testing.T, input, path string) (token.Token, *token.Tokenizer, error) {
	t.Helper()
	p, err := jpath.Parse(path)
	if err != nil {
		t.Fatalf("Parse(%q): %v", path, err)
	}
	tz := token.New(strings.NewReader(input))
	tok, err := Navigate(tz, p)
	return tok, tz, err
}

func TestNavigateRoot(t *testing.T) {
	tok, _, err := navigate(t, `[1,2]`, "")
	if err != nil || tok.Kind != token.BeginArray {
		t.Fatalf("got %v %v", tok.Kind, err)
	}
}

func TestNavigateKeysAndIndexes(t *testing.T) {
	tok, tz, err := navigate(t, `{"meta":{"x":1},"data":{"users":[7]}}`, "data.users")
	if err != nil || tok.Kind != token.BeginArray {
		t.Fatalf("keys: got %v %v", tok.Kind, err)
	}
	el, _ := tz.Next()
	if string(el.Bytes) != "7" {
		t.Fatalf("array content wrong: %q", el.Bytes)
	}
	tok, _, err = navigate(t, `{"batches":[{"rows":[1]},{"rows":[2,3]}]}`, "batches[1].rows")
	if err != nil || tok.Kind != token.BeginArray {
		t.Fatalf("index: got %v %v", tok.Kind, err)
	}
}

func TestNavigateEscapedKeyMatch(t *testing.T) {
	// The document spells the key with a \u escape; the path spells it
	// literally. Match is on decoded strings, so they must agree.
	tok, _, err := navigate(t, `{"caf\u00e9":[1]}`, "café")
	if err != nil || tok.Kind != token.BeginArray {
		t.Fatalf("got %v %v", tok.Kind, err)
	}
}

func TestNavigateErrors(t *testing.T) {
	cases := []struct {
		input, path, want string
	}{
		{`{"a":1}`, "missing", `key "missing" not found`},
		{`{"a":[1,2]}`, "a[5]", "index 5 out of range (array has 2 elements)"},
		{`{"a":"scalar"}`, "a.b", "expected an object, found string"},
		{`{"a":{"b":1}}`, "a[0]", "expected an array to index into, found object"},
	}
	for _, c := range cases {
		_, _, err := navigate(t, c.input, c.path)
		pe, ok := err.(*PathError)
		if !ok || !strings.Contains(pe.Msg, c.want) {
			t.Fatalf("navigate(%s, %s): got %v, want message containing %q", c.input, c.path, err, c.want)
		}
	}
	// Errors are attributed to the exact prefix that failed.
	_, _, err := navigate(t, `{"a":{"b":{}}}`, "a.b.c.d")
	pe, ok := err.(*PathError)
	if !ok || pe.Path != "a.b.c" {
		t.Fatalf("got %v; want failure attributed to prefix a.b.c", err)
	}
}

func TestDrainValidatesTail(t *testing.T) {
	// Navigate to the array, consume it, then Drain must still notice the
	// document is truncated after it.
	tz := token.New(strings.NewReader(`{"a":[1],"b":`))
	p, _ := jpath.Parse("a")
	first, err := Navigate(tz, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := SkipFrom(tz, first); err != nil {
		t.Fatal(err)
	}
	if err := Drain(tz); err == nil {
		t.Fatal("truncated tail not detected")
	}
}
