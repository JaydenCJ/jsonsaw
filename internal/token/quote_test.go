// Tests for Unquote/Quote — the pieces that make --path key comparison and
// join --path wrapper synthesis correct for non-trivial key names.
package token

import (
	"strings"
	"testing"
)

func TestUnquoteEscapes(t *testing.T) {
	for raw, want := range map[string]string{
		`"hello"`:                "hello",
		`"a\"b\\c\/d\b\f\n\r\t"`: "a\"b\\c/d\b\f\n\r\t",
		`"caf\u00e9"`:            "café", // \u escape decodes
		`"データ"`:                  "データ",  // raw UTF-8 passes through
	} {
		s, err := Unquote([]byte(raw))
		if err != nil || s != want {
			t.Fatalf("Unquote(%s): got %q, %v", raw, s, err)
		}
	}
}

func TestUnquoteSurrogates(t *testing.T) {
	// U+1F600 GRINNING FACE encoded as a UTF-16 surrogate pair.
	s, err := Unquote([]byte(`"😀"`))
	if err != nil || s != "\U0001F600" {
		t.Fatalf("pair: got %q, %v", s, err)
	}
	// encoding/json substitutes U+FFFD for unpaired surrogates; matching
	// that keeps key comparison consistent with users' other tools.
	s, err = Unquote([]byte(`"a\ud83db"`))
	if err != nil || s != "a�b" {
		t.Fatalf("lone: got %q, %v", s, err)
	}
}

func TestUnquoteRejectsUnquotedInput(t *testing.T) {
	if _, err := Unquote([]byte(`hello`)); err == nil {
		t.Fatal("unquoted input accepted")
	}
}

func TestQuoteRoundTripsThroughUnquote(t *testing.T) {
	for _, s := range []string{"", "plain", `with "quotes"`, "tab\there", "new\nline", "back\\slash", "café", "control\x01byte", "\U0001F600"} {
		got, err := Unquote(Quote(s))
		if err != nil || got != s {
			t.Fatalf("round trip of %q: got %q, %v", s, got, err)
		}
	}
}

func TestQuoteKeepsUTF8Readable(t *testing.T) {
	q := string(Quote("données"))
	if q != `"données"` || strings.Contains(q, `\u`) {
		t.Fatalf("got %s; non-ASCII must pass through unescaped", q)
	}
}
