// Package token implements an incremental JSON tokenizer that reads its
// input in a single forward pass. Memory use is O(nesting depth + largest
// single token) — independent of document size — which is what lets jsonsaw
// saw through multi-gigabyte documents in a few megabytes of RAM.
//
// The tokenizer is strict RFC 8259: it rejects leading zeros, trailing
// commas, bare words, unescaped control characters, and trailing data after
// the top-level value. A sequence mode (NewSequence) accepts a stream of
// whitespace-separated top-level values instead of exactly one, which is
// how jsonsaw reads JSONL without ever buffering a whole line.
package token

import (
	"bufio"
	"fmt"
	"io"
)

// Kind identifies a token. Structural commas and colons are consumed by the
// tokenizer itself and never surface; callers see only values, keys, and
// container boundaries.
type Kind int

const (
	BeginObject Kind = iota // {
	EndObject               // }
	BeginArray              // [
	EndArray                // ]
	Key                     // an object key (raw bytes include the quotes)
	String                  // a string value (raw bytes include the quotes)
	Number                  // a number value (raw source bytes)
	True                    // literal true
	False                   // literal false
	Null                    // literal null
)

// ValueKind names the JSON type a token starts, for human-readable errors
// and for `jsonsaw paths` output.
func ValueKind(k Kind) string {
	switch k {
	case BeginObject, EndObject:
		return "object"
	case BeginArray, EndArray:
		return "array"
	case Key:
		return "key"
	case String:
		return "string"
	case Number:
		return "number"
	case True, False:
		return "boolean"
	case Null:
		return "null"
	}
	return "unknown"
}

// Token is one lexical unit of the input.
//
// Bytes holds the raw source bytes for Key, String, and Number tokens
// (strings keep their quotes and escapes exactly as written, so copying a
// token back out is byte-faithful). For True, False, and Null it holds the
// literal text. The slice aliases an internal scratch buffer and is only
// valid until the next call to Next; copy it if you need to keep it.
type Token struct {
	Kind  Kind
	Bytes []byte
	Line  int // 1-based line of the token's first byte
	Col   int // 1-based byte column of the token's first byte
}

// SyntaxError reports malformed JSON with the position of the offending
// byte. jsonsaw maps it to exit code 1.
type SyntaxError struct {
	Line int
	Col  int
	Msg  string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("line %d, col %d: %s", e.Line, e.Col, e.Msg)
}

// DefaultMaxDepth caps container nesting so a hostile input of a billion
// open brackets cannot grow the state stack without bound.
const DefaultMaxDepth = 10000

type state int

const (
	stValue      state = iota // a value must follow
	stValueOrEnd              // just after '[': a value or ']'
	stKeyOrEnd                // just after '{': a key or '}'
	stKey                     // after ',' in an object: a key must follow
	stCommaOrEnd              // after a value inside a container
	stEnd                     // top-level value complete; only EOF may follow
)

// Tokenizer streams tokens from r. Create one with New or NewSequence.
type Tokenizer struct {
	r        *bufio.Reader
	line     int
	col      int
	stack    []byte // '{' or '[' per open container
	st       state
	scratch  []byte
	err      error // sticky: once Next fails, it keeps failing
	maxDepth int
	sequence bool
	started  bool // set after the first byte; gates BOM stripping
}

// New returns a tokenizer for exactly one top-level JSON value. Trailing
// non-whitespace after the value is a SyntaxError.
func New(r io.Reader) *Tokenizer {
	return &Tokenizer{
		r:        bufio.NewReaderSize(r, 64*1024),
		line:     1,
		col:      1,
		st:       stValue,
		maxDepth: DefaultMaxDepth,
	}
}

// NewSequence returns a tokenizer that accepts any number of
// whitespace-separated top-level values (a superset of JSONL). Next returns
// io.EOF after the last value; an empty input is a zero-value sequence.
func NewSequence(r io.Reader) *Tokenizer {
	t := New(r)
	t.sequence = true
	return t
}

// SetMaxDepth overrides DefaultMaxDepth. n must be at least 1.
func (t *Tokenizer) SetMaxDepth(n int) {
	if n >= 1 {
		t.maxDepth = n
	}
}

// Pos reports the position of the next unread byte (1-based line and
// byte column).
func (t *Tokenizer) Pos() (line, col int) { return t.line, t.col }

// Depth reports how many containers are currently open.
func (t *Tokenizer) Depth() int { return len(t.stack) }

// Next returns the next token. It returns io.EOF exactly once, at a clean
// end of input; any other error is sticky and repeats on later calls.
func (t *Tokenizer) Next() (Token, error) {
	if t.err != nil {
		return Token{}, t.err
	}
	tok, err := t.next()
	if err != nil {
		t.err = err
	}
	return tok, err
}

func (t *Tokenizer) next() (Token, error) {
	for {
		if err := t.skipSpace(); err != nil {
			return Token{}, err
		}
		c, ok, err := t.peekByte()
		if err != nil {
			return Token{}, err
		}
		if !ok { // end of input
			if t.st == stEnd {
				return Token{}, io.EOF
			}
			if t.sequence && t.st == stValue && len(t.stack) == 0 {
				return Token{}, io.EOF
			}
			return Token{}, t.errf("unexpected end of input")
		}
		if t.st == stEnd {
			return Token{}, t.errf("trailing data after top-level value")
		}

		line, col := t.line, t.col
		t.readByte() // consume c

		switch t.st {
		case stValue, stValueOrEnd:
			if c == ']' && t.st == stValueOrEnd {
				t.pop()
				t.afterValue()
				return Token{Kind: EndArray, Line: line, Col: col}, nil
			}
			return t.scanValue(c, line, col)

		case stKeyOrEnd, stKey:
			if c == '}' && t.st == stKeyOrEnd {
				t.pop()
				t.afterValue()
				return Token{Kind: EndObject, Line: line, Col: col}, nil
			}
			if c != '"' {
				if t.st == stKeyOrEnd {
					return Token{}, t.errAtf(line, col, "expected object key or '}', found %q", c)
				}
				return Token{}, t.errAtf(line, col, "expected object key, found %q", c)
			}
			if err := t.scanString(); err != nil {
				return Token{}, err
			}
			key := t.scratch
			if err := t.skipSpace(); err != nil {
				return Token{}, err
			}
			sep, ok, err := t.peekByte()
			if err != nil {
				return Token{}, err
			}
			if !ok {
				return Token{}, t.errf("unexpected end of input after object key")
			}
			if sep != ':' {
				return Token{}, t.errf("expected ':' after object key, found %q", sep)
			}
			t.readByte()
			t.st = stValue
			return Token{Kind: Key, Bytes: key, Line: line, Col: col}, nil

		case stCommaOrEnd:
			switch c {
			case ',':
				if t.stack[len(t.stack)-1] == '{' {
					t.st = stKey
				} else {
					t.st = stValue
				}
				continue // a comma is not a token; keep scanning
			case '}':
				if t.stack[len(t.stack)-1] != '{' {
					return Token{}, t.errAtf(line, col, "expected ',' or ']', found '}'")
				}
				t.pop()
				t.afterValue()
				return Token{Kind: EndObject, Line: line, Col: col}, nil
			case ']':
				if t.stack[len(t.stack)-1] != '[' {
					return Token{}, t.errAtf(line, col, "expected ',' or '}', found ']'")
				}
				t.pop()
				t.afterValue()
				return Token{Kind: EndArray, Line: line, Col: col}, nil
			default:
				if t.stack[len(t.stack)-1] == '{' {
					return Token{}, t.errAtf(line, col, "expected ',' or '}', found %q", c)
				}
				return Token{}, t.errAtf(line, col, "expected ',' or ']', found %q", c)
			}
		}
	}
}

// scanValue dispatches on the first byte of a value.
func (t *Tokenizer) scanValue(c byte, line, col int) (Token, error) {
	switch {
	case c == '{':
		if err := t.push('{', line, col); err != nil {
			return Token{}, err
		}
		t.st = stKeyOrEnd
		return Token{Kind: BeginObject, Line: line, Col: col}, nil
	case c == '[':
		if err := t.push('[', line, col); err != nil {
			return Token{}, err
		}
		t.st = stValueOrEnd
		return Token{Kind: BeginArray, Line: line, Col: col}, nil
	case c == '"':
		if err := t.scanString(); err != nil {
			return Token{}, err
		}
		t.afterValue()
		return Token{Kind: String, Bytes: t.scratch, Line: line, Col: col}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		if err := t.scanNumber(c); err != nil {
			return Token{}, err
		}
		t.afterValue()
		return Token{Kind: Number, Bytes: t.scratch, Line: line, Col: col}, nil
	case c == 't':
		if err := t.scanLiteral("true"); err != nil {
			return Token{}, err
		}
		t.afterValue()
		return Token{Kind: True, Bytes: t.scratch, Line: line, Col: col}, nil
	case c == 'f':
		if err := t.scanLiteral("false"); err != nil {
			return Token{}, err
		}
		t.afterValue()
		return Token{Kind: False, Bytes: t.scratch, Line: line, Col: col}, nil
	case c == 'n':
		if err := t.scanLiteral("null"); err != nil {
			return Token{}, err
		}
		t.afterValue()
		return Token{Kind: Null, Bytes: t.scratch, Line: line, Col: col}, nil
	default:
		return Token{}, t.errAtf(line, col, "unexpected character %q at start of value", c)
	}
}

// afterValue picks the state that follows a completed value.
func (t *Tokenizer) afterValue() {
	if len(t.stack) == 0 {
		if t.sequence {
			t.st = stValue // ready for the next top-level value
		} else {
			t.st = stEnd
		}
		return
	}
	t.st = stCommaOrEnd
}

func (t *Tokenizer) push(c byte, line, col int) error {
	if len(t.stack) >= t.maxDepth {
		return t.errAtf(line, col, "nesting deeper than %d levels", t.maxDepth)
	}
	t.stack = append(t.stack, c)
	return nil
}

func (t *Tokenizer) pop() { t.stack = t.stack[:len(t.stack)-1] }

// scanString scans a string whose opening quote was already consumed. The
// raw bytes — quotes, escapes, and all — land in t.scratch so that copying
// the token out preserves the input exactly.
func (t *Tokenizer) scanString() error {
	t.scratch = append(t.scratch[:0], '"')
	for {
		c, err := t.readByte()
		if err != nil {
			return t.eofOr(err, "unterminated string")
		}
		if c < 0x20 {
			return t.errf("unescaped control character 0x%02x in string", c)
		}
		t.scratch = append(t.scratch, c)
		switch c {
		case '"':
			return nil
		case '\\':
			e, err := t.readByte()
			if err != nil {
				return t.eofOr(err, "unterminated string")
			}
			t.scratch = append(t.scratch, e)
			switch e {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				// valid single-character escape
			case 'u':
				for i := 0; i < 4; i++ {
					h, err := t.readByte()
					if err != nil {
						return t.eofOr(err, "unterminated string")
					}
					if !isHex(h) {
						return t.errf(`invalid \u escape: %q is not a hex digit`, h)
					}
					t.scratch = append(t.scratch, h)
				}
			default:
				return t.errf(`invalid escape character '\%c' in string`, e)
			}
		}
	}
}

// scanNumber scans a number whose first byte c was already consumed,
// enforcing the RFC 8259 grammar (no leading zeros, no bare '.', a digit
// required after '.', 'e', and '-').
func (t *Tokenizer) scanNumber(c byte) error {
	t.scratch = append(t.scratch[:0], c)
	if c == '-' {
		d, err := t.readByte()
		if err != nil || d < '0' || d > '9' {
			if err != nil && err != io.EOF {
				return err
			}
			return t.errf("invalid number: digit required after '-'")
		}
		t.scratch = append(t.scratch, d)
		c = d
	}
	if c == '0' {
		if d, ok, err := t.peekByte(); err != nil {
			return err
		} else if ok && d >= '0' && d <= '9' {
			return t.errf("invalid number: leading zero")
		}
	} else {
		if _, err := t.readDigits(); err != nil {
			return err
		}
	}
	if d, ok, err := t.peekByte(); err != nil {
		return err
	} else if ok && d == '.' {
		t.readByte()
		t.scratch = append(t.scratch, '.')
		n, err := t.readDigits()
		if err != nil {
			return err
		}
		if n == 0 {
			return t.errf("invalid number: digit required after decimal point")
		}
	}
	if d, ok, err := t.peekByte(); err != nil {
		return err
	} else if ok && (d == 'e' || d == 'E') {
		t.readByte()
		t.scratch = append(t.scratch, d)
		if s, ok, err := t.peekByte(); err != nil {
			return err
		} else if ok && (s == '+' || s == '-') {
			t.readByte()
			t.scratch = append(t.scratch, s)
		}
		n, err := t.readDigits()
		if err != nil {
			return err
		}
		if n == 0 {
			return t.errf("invalid number: digit required in exponent")
		}
	}
	return nil
}

// readDigits appends consecutive ASCII digits to scratch and returns how
// many it consumed.
func (t *Tokenizer) readDigits() (int, error) {
	n := 0
	for {
		d, ok, err := t.peekByte()
		if err != nil {
			return n, err
		}
		if !ok || d < '0' || d > '9' {
			return n, nil
		}
		t.readByte()
		t.scratch = append(t.scratch, d)
		n++
	}
}

// scanLiteral consumes the remainder of `true`, `false`, or `null` (the
// first byte is already gone) and leaves the full literal in scratch.
func (t *Tokenizer) scanLiteral(lit string) error {
	t.scratch = append(t.scratch[:0], lit[0])
	for i := 1; i < len(lit); i++ {
		c, err := t.readByte()
		if err != nil {
			return t.eofOr(err, fmt.Sprintf("invalid literal: expected %q", lit))
		}
		if c != lit[i] {
			return t.errf("invalid literal: expected %q", lit)
		}
		t.scratch = append(t.scratch, c)
	}
	return nil
}

// skipSpace consumes insignificant whitespace (and a UTF-8 BOM, but only
// before the very first byte of the input).
func (t *Tokenizer) skipSpace() error {
	if !t.started {
		t.started = true
		if b, err := t.r.Peek(3); err == nil && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
			t.r.Discard(3)
		}
	}
	for {
		c, ok, err := t.peekByte()
		if err != nil {
			return err
		}
		if !ok || (c != ' ' && c != '\t' && c != '\n' && c != '\r') {
			return nil
		}
		t.readByte()
	}
}

// peekByte returns the next byte without consuming it. ok is false at end
// of input; err is only ever a real read failure, never io.EOF.
func (t *Tokenizer) peekByte() (byte, bool, error) {
	b, err := t.r.Peek(1)
	if len(b) > 0 {
		return b[0], true, nil
	}
	if err == io.EOF {
		return 0, false, nil
	}
	return 0, false, err
}

// readByte consumes one byte and advances the line/column counters.
func (t *Tokenizer) readByte() (byte, error) {
	c, err := t.r.ReadByte()
	if err != nil {
		return 0, err
	}
	if c == '\n' {
		t.line++
		t.col = 1
	} else {
		t.col++
	}
	return c, nil
}

// eofOr turns io.EOF into a positioned syntax error and passes real read
// failures through untouched.
func (t *Tokenizer) eofOr(err error, msg string) error {
	if err == io.EOF {
		return t.errf("%s: unexpected end of input", msg)
	}
	return err
}

func (t *Tokenizer) errf(format string, args ...interface{}) error {
	return &SyntaxError{Line: t.line, Col: t.col, Msg: fmt.Sprintf(format, args...)}
}

func (t *Tokenizer) errAtf(line, col int, format string, args ...interface{}) error {
	return &SyntaxError{Line: line, Col: col, Msg: fmt.Sprintf(format, args...)}
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
