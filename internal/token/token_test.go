// Tests for the incremental tokenizer: token sequences, RFC 8259 strictness
// (the rejection cases matter most — every one is a document that a sloppy
// splitter would silently corrupt), positions, and sequence mode.
package token

import (
	"io"
	"strings"
	"testing"
)

// collect drains a tokenizer into (kinds, raw-bytes) for compact assertions.
func collect(t *testing.T, input string) ([]Kind, []string) {
	t.Helper()
	tz := New(strings.NewReader(input))
	var kinds []Kind
	var raws []string
	for {
		tok, err := tz.Next()
		if err == io.EOF {
			return kinds, raws
		}
		if err != nil {
			t.Fatalf("Next() on %q: %v", input, err)
		}
		kinds = append(kinds, tok.Kind)
		raws = append(raws, string(tok.Bytes))
	}
}

// wantErr asserts that tokenizing input fails with a SyntaxError whose
// message contains substr.
func wantErr(t *testing.T, input, substr string) *SyntaxError {
	t.Helper()
	tz := New(strings.NewReader(input))
	for {
		_, err := tz.Next()
		if err == io.EOF {
			t.Fatalf("input %q tokenized cleanly; want error containing %q", input, substr)
		}
		if err != nil {
			se, ok := err.(*SyntaxError)
			if !ok {
				t.Fatalf("input %q: got %T (%v), want *SyntaxError", input, err, err)
			}
			if !strings.Contains(se.Msg, substr) {
				t.Fatalf("input %q: error %q does not contain %q", input, se.Msg, substr)
			}
			return se
		}
	}
}

func kindsEqual(a []Kind, b ...Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScalars(t *testing.T) {
	for input, kind := range map[string]Kind{
		`"hello"`:  String,
		`-12.5e+3`: Number,
		"true":     True,
		"false":    False,
		"null":     Null,
	} {
		kinds, raws := collect(t, input)
		if !kindsEqual(kinds, kind) || raws[0] != input {
			t.Fatalf("input %q: got %v %v", input, kinds, raws)
		}
	}
}

func TestContainerTokenSequences(t *testing.T) {
	kinds, _ := collect(t, `{}`)
	if !kindsEqual(kinds, BeginObject, EndObject) {
		t.Fatalf("{}: got %v", kinds)
	}
	kinds, _ = collect(t, ` [ ] `)
	if !kindsEqual(kinds, BeginArray, EndArray) {
		t.Fatalf("[]: got %v", kinds)
	}
	kinds, raws := collect(t, `{"a":1,"b":[true,null]}`)
	want := []Kind{BeginObject, Key, Number, Key, BeginArray, True, Null, EndArray, EndObject}
	if !kindsEqual(kinds, want...) {
		t.Fatalf("got kinds %v, want %v", kinds, want)
	}
	if raws[1] != `"a"` || raws[3] != `"b"` {
		t.Fatalf("key raw bytes wrong: %v", raws)
	}
	// A key must never surface as a string value: navigation depends on it.
	kinds, _ = collect(t, `{"k":"v"}`)
	if kinds[1] != Key || kinds[2] != String {
		t.Fatalf("got %v; key and string value must have distinct kinds", kinds)
	}
}

func TestStringEscapesPreservedRaw(t *testing.T) {
	raw := `"a\"b\\c\/d\b\f\n\r\té"`
	_, raws := collect(t, raw)
	if raws[0] != raw {
		t.Fatalf("raw bytes altered: got %q want %q", raws[0], raw)
	}
}

func TestNumberForms(t *testing.T) {
	// Every spelling here is legal and must be preserved byte-for-byte —
	// re-encoding 1e2 as 100 would break checksum-based diffing of exports.
	for _, n := range []string{"0", "-0", "7", "-7", "120", "0.5", "-0.5", "1e2", "1E2", "1e+2", "1e-2", "1.25e10", "0e0"} {
		_, raws := collect(t, n)
		if raws[0] != n {
			t.Fatalf("number %q re-read as %q", n, raws[0])
		}
	}
}

func TestInvalidNumbersRejected(t *testing.T) {
	for input, substr := range map[string]string{
		"01":  "leading zero",
		"-01": "leading zero",
		"-":   "digit required after '-'",
		"1.":  "digit required after decimal point",
		"1e":  "digit required in exponent",
		"1e+": "digit required in exponent",
		".5":  "unexpected character",
	} {
		wantErr(t, input, substr)
	}
}

func TestInvalidStringsRejected(t *testing.T) {
	for input, substr := range map[string]string{
		`"\x41"`:    "invalid escape",
		`"\u12g4"`:  "not a hex digit",
		`"abc`:      "unterminated string",
		`"abc\`:     "unterminated string",
		"\"a\nb\"":  "control character",
		"\"a\x01\"": "control character",
	} {
		wantErr(t, input, substr)
	}
}

func TestStructuralErrorsRejected(t *testing.T) {
	for input, substr := range map[string]string{
		`[1,]`:          "unexpected character", // trailing comma in array
		`{"a":1,}`:      "expected object key",  // trailing comma in object
		`{"a" 1}`:       "expected ':'",         // missing colon
		`[1 2]`:         "expected ','",         // missing comma
		`{"a":1 "b":2}`: "expected ','",         // missing comma between pairs
		`[1}`:           "expected ',' or ']'",  // mismatched close
		`{"a":1]`:       "expected ',' or '}'",  // mismatched close
		`tru`:           "invalid literal",      // truncated literal
		`nulL`:          "invalid literal",      // case matters
		`undefined`:     "unexpected character", // not a JSON value
	} {
		wantErr(t, input, substr)
	}
}

func TestIncompleteOrTrailingInputRejected(t *testing.T) {
	for input, substr := range map[string]string{
		`{} {}`: "trailing data",
		`1 1`:   "trailing data",
		``:      "unexpected end of input",
		`   `:   "unexpected end of input",
		`{"a":`: "unexpected end of input",
		`[1,2`:  "unexpected end of input",
	} {
		wantErr(t, input, substr)
	}
}

func TestBOMAndWhitespaceTolerated(t *testing.T) {
	kinds, _ := collect(t, "\xEF\xBB\xBF{}")
	if !kindsEqual(kinds, BeginObject, EndObject) {
		t.Fatalf("BOM not skipped: %v", kinds)
	}
	kinds, _ = collect(t, "\r\n\t {\r\n\"a\" : 1\r\n}\r\n")
	if !kindsEqual(kinds, BeginObject, Key, Number, EndObject) {
		t.Fatalf("CRLF whitespace mishandled: %v", kinds)
	}
}

func TestErrorPositionIsAccurate(t *testing.T) {
	// The bad byte is the 'x' on line 3, column 10 (1-based, bytes).
	input := "{\n  \"a\": 1,\n  \"bb\":  x\n}"
	se := wantErr(t, input, "unexpected character")
	if se.Line != 3 || se.Col != 10 {
		t.Fatalf("error at line %d col %d, want 3:10", se.Line, se.Col)
	}
}

func TestTokenPositionsReported(t *testing.T) {
	tz := New(strings.NewReader("{\n  \"key\": 42}"))
	tok, _ := tz.Next() // {
	if tok.Line != 1 || tok.Col != 1 {
		t.Fatalf("{ at %d:%d, want 1:1", tok.Line, tok.Col)
	}
	tok, _ = tz.Next() // "key"
	if tok.Line != 2 || tok.Col != 3 {
		t.Fatalf("key at %d:%d, want 2:3", tok.Line, tok.Col)
	}
}

func TestMaxDepth(t *testing.T) {
	// Deep-but-legal nesting works…
	input := strings.Repeat("[", 1000) + strings.Repeat("]", 1000)
	if kinds, _ := collect(t, input); len(kinds) != 2000 {
		t.Fatalf("got %d tokens, want 2000", len(kinds))
	}
	// …and a hostile bracket flood hits the configured ceiling.
	tz := New(strings.NewReader(strings.Repeat("[", 50)))
	tz.SetMaxDepth(10)
	var lastErr error
	for lastErr == nil {
		_, lastErr = tz.Next()
	}
	se, ok := lastErr.(*SyntaxError)
	if !ok || !strings.Contains(se.Msg, "nesting deeper than 10") {
		t.Fatalf("got %v, want max-depth error", lastErr)
	}
}

func TestDuplicateKeysStreamThrough(t *testing.T) {
	// A streaming tokenizer must not deduplicate: both pairs are surfaced
	// and policy is left to the caller (matching jq and encoding/json).
	kinds, raws := collect(t, `{"a":1,"a":2}`)
	if !kindsEqual(kinds, BeginObject, Key, Number, Key, Number, EndObject) {
		t.Fatalf("got %v", kinds)
	}
	if raws[2] != "1" || raws[4] != "2" {
		t.Fatalf("got %v", raws)
	}
}

func TestErrorIsSticky(t *testing.T) {
	tz := New(strings.NewReader(`[oops]`))
	tz.Next() // [
	_, err1 := tz.Next()
	_, err2 := tz.Next()
	if err1 == nil || err1 != err2 {
		t.Fatalf("error not sticky: %v then %v", err1, err2)
	}
}

func TestSequenceMode(t *testing.T) {
	// JSONL is a sequence of top-level values; blank lines are whitespace.
	tz := NewSequence(strings.NewReader("{\"a\":1}\n\n[2]\n\"x\"\n"))
	var kinds []Kind
	for {
		tok, err := tz.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		kinds = append(kinds, tok.Kind)
	}
	want := []Kind{BeginObject, Key, Number, EndObject, BeginArray, Number, EndArray, String}
	if !kindsEqual(kinds, want...) {
		t.Fatalf("got %v want %v", kinds, want)
	}
	// An empty sequence is a clean EOF, not an error.
	tz = NewSequence(strings.NewReader("\n  \n"))
	if _, err := tz.Next(); err != io.EOF {
		t.Fatalf("got %v, want io.EOF for a blank sequence", err)
	}
}

func TestSequenceModeRejectsBadValues(t *testing.T) {
	// A garbage line fails with its line number…
	tz := NewSequence(strings.NewReader("{\"a\":1}\ngarbage\n"))
	var err error
	for err == nil {
		_, err = tz.Next()
	}
	se, ok := err.(*SyntaxError)
	if !ok || se.Line != 2 {
		t.Fatalf("got %v, want syntax error on line 2", err)
	}
	// …and a truncated final value is not silently dropped.
	tz = NewSequence(strings.NewReader(`{"a":1} {"b":`))
	err = nil
	for err == nil {
		_, err = tz.Next()
	}
	if err == io.EOF || !strings.Contains(err.Error(), "unexpected end of input") {
		t.Fatalf("got %v, want truncation error", err)
	}
}
