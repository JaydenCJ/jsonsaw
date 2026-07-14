// Tests for the path language. The parser is tiny but it is the first
// thing every user touches; each rejection case below is a typo class we
// want to fail at exit 2, before any input is read.
package jpath

import (
	"reflect"
	"testing"
)

func mustParse(t *testing.T, s string) Path {
	t.Helper()
	p, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return p
}

func key(k string) Segment { return Segment{Key: k} }
func idx(n int) Segment    { return Segment{Index: n, IsIndex: true} }
func eq(a, b Path) bool    { return reflect.DeepEqual(a, b) }

func TestParseRoot(t *testing.T) {
	if p := mustParse(t, ""); p != nil {
		t.Fatalf(`Parse(""): got %v, want nil`, p)
	}
	if p := mustParse(t, "."); p != nil {
		t.Fatalf(`Parse("."): got %v, want nil`, p)
	}
}

func TestParseKeys(t *testing.T) {
	for input, want := range map[string]Path{
		"data.users": {key("data"), key("users")},
		// `jsonsaw paths` prints ".data.users"; pasting that must work.
		".data.users": {key("data"), key("users")},
		// Bare digits are object keys; only [N] indexes arrays. This is
		// the disambiguation rule that makes {"0": {...}} addressable.
		"data.0": {key("data"), key("0")},
		// Non-ASCII keys need no quoting.
		"データ.利用者": {key("データ"), key("利用者")},
	} {
		if p := mustParse(t, input); !eq(p, want) {
			t.Fatalf("Parse(%q): got %v want %v", input, p, want)
		}
	}
}

func TestParseIndexes(t *testing.T) {
	for input, want := range map[string]Path{
		"results[0].rows": {key("results"), idx(0), key("rows")},
		"grid[2][10]":     {key("grid"), idx(2), idx(10)},
		// A document whose root is an array of envelopes.
		"[0].items": {idx(0), key("items")},
	} {
		if p := mustParse(t, input); !eq(p, want) {
			t.Fatalf("Parse(%q): got %v want %v", input, p, want)
		}
	}
}

func TestParseQuotedKeys(t *testing.T) {
	for input, want := range map[string]Path{
		`payload."weird.key".list`: {key("payload"), key("weird.key"), key("list")},
		`"say \"hi\"".v`:           {key(`say "hi"`), key("v")},
		`"back\\slash"`:            {key(`back\slash`)},
	} {
		if p := mustParse(t, input); !eq(p, want) {
			t.Fatalf("Parse(%q): got %v want %v", input, p, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{
		"a..b",    // empty segment
		"a.",      // trailing dot
		"a[]",     // empty index
		"a[1",     // unterminated index
		"a[-1]",   // negative index
		"a[1]b",   // key jammed against an index
		`a."x`,    // unterminated quote
		`a."x\q"`, // unsupported escape
		"a]b",     // stray bracket
		`"a"b`,    // text after closing quote
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("Parse(%q) succeeded; want error", bad)
		} else if _, ok := err.(*ParseError); !ok {
			t.Fatalf("Parse(%q): got %T, want *ParseError", bad, err)
		}
	}
}

func TestStringRoundTrips(t *testing.T) {
	if got := (Path)(nil).String(); got != "." {
		t.Fatalf("root renders as %q, want \".\"", got)
	}
	for _, s := range []string{"data.users", "results[0].rows", "grid[2][10]", "[0].items", `payload."weird.key".list`, "データ.利用者"} {
		p := mustParse(t, s)
		back, err := Parse(p.String())
		if err != nil || !eq(p, back) {
			t.Fatalf("%q → %q did not round-trip: %v %v", s, p.String(), back, err)
		}
	}
}

func TestStringQuotesOnlyWhenNeeded(t *testing.T) {
	p := Path{key("plain"), key("has.dot"), idx(3)}
	if got := p.String(); got != `plain."has.dot"[3]` {
		t.Fatalf("got %q", got)
	}
}
