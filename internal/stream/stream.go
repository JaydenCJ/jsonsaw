// Package stream builds value-level operations on top of the tokenizer:
// skipping a value, copying it out compactly or pretty-printed, navigating
// to a path, and draining a document to validate its tail. Everything here
// is single-pass and never materializes a value tree, so memory stays at
// O(nesting depth + largest single token) no matter how large the input is.
package stream

import (
	"fmt"
	"io"

	"github.com/JaydenCJ/jsonsaw/internal/jpath"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

// PathError reports that navigation failed: a missing key, an index out of
// range, or a value of the wrong type along the way. jsonsaw maps it to
// exit code 1.
type PathError struct {
	Path string // the path prefix that failed, rendered
	Msg  string
}

func (e *PathError) Error() string {
	return fmt.Sprintf("path %s: %s", e.Path, e.Msg)
}

// SkipFrom consumes the remainder of the value whose first token is first,
// without writing anything.
func SkipFrom(tz *token.Tokenizer, first token.Token) error {
	depth := 0
	tok := first
	for {
		switch tok.Kind {
		case token.BeginObject, token.BeginArray:
			depth++
		case token.EndObject, token.EndArray:
			depth--
		}
		if depth == 0 {
			return nil
		}
		var err error
		tok, err = tz.Next()
		if err != nil {
			return err
		}
	}
}

// CopyFrom writes the value whose first token is first to w as compact
// JSON. String and number tokens are copied byte-for-byte from the source,
// so escapes and number spellings survive a split/join round trip exactly.
func CopyFrom(tz *token.Tokenizer, first token.Token, w io.Writer) error {
	return WriteFrom(tz, first, w, "", 0)
}

// prev tracks what was last written, which decides the separator before
// the next token.
type prev int

const (
	prevNone  prev = iota // nothing yet
	prevBegin             // an opening bracket
	prevValue             // a completed value (scalar or closing bracket)
	prevKey               // an object key (colon already written)
)

// WriteFrom writes the value whose first token is first to w. With
// indent == "" the output is compact; otherwise it is pretty-printed with
// that unit of indentation, and base sets how many units the value is
// already nested (used by `join --pretty` to indent elements inside the
// wrapper it prints itself).
func WriteFrom(tz *token.Tokenizer, first token.Token, w io.Writer, indent string, base int) error {
	pretty := indent != ""
	depth := base
	last := prevNone
	tok := first

	newline := func(n int) error {
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if _, err := io.WriteString(w, indent); err != nil {
				return err
			}
		}
		return nil
	}
	// sep writes whatever must precede a key or value token at depth n.
	sep := func(n int) error {
		switch last {
		case prevBegin:
			if pretty {
				return newline(n)
			}
		case prevValue:
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
			if pretty {
				return newline(n)
			}
		}
		return nil // prevNone and prevKey need nothing
	}

	for {
		switch tok.Kind {
		case token.BeginObject, token.BeginArray:
			if err := sep(depth); err != nil {
				return err
			}
			b := byte('{')
			if tok.Kind == token.BeginArray {
				b = '['
			}
			if _, err := w.Write([]byte{b}); err != nil {
				return err
			}
			depth++
			last = prevBegin

		case token.EndObject, token.EndArray:
			depth--
			if pretty && last != prevBegin {
				if err := newline(depth); err != nil {
					return err
				}
			}
			b := byte('}')
			if tok.Kind == token.EndArray {
				b = ']'
			}
			if _, err := w.Write([]byte{b}); err != nil {
				return err
			}
			last = prevValue
			if depth == base {
				return nil
			}

		case token.Key:
			if err := sep(depth); err != nil {
				return err
			}
			if _, err := w.Write(tok.Bytes); err != nil {
				return err
			}
			colon := ":"
			if pretty {
				colon = ": "
			}
			if _, err := io.WriteString(w, colon); err != nil {
				return err
			}
			last = prevKey

		default: // String, Number, True, False, Null
			if err := sep(depth); err != nil {
				return err
			}
			if _, err := w.Write(tok.Bytes); err != nil {
				return err
			}
			last = prevValue
			if depth == base {
				return nil
			}
		}

		var err error
		tok, err = tz.Next()
		if err != nil {
			return err
		}
	}
}

// Navigate consumes tokens until it reaches the value addressed by p and
// returns that value's first token. Sibling values along the way are
// skipped token-by-token, never buffered. An empty path returns the
// document's first token.
func Navigate(tz *token.Tokenizer, p jpath.Path) (token.Token, error) {
	tok, err := tz.Next()
	if err != nil {
		return token.Token{}, err
	}
	for i, seg := range p {
		at := p[:i+1].String()
		if seg.IsIndex {
			if tok.Kind != token.BeginArray {
				return token.Token{}, &PathError{at, fmt.Sprintf("expected an array to index into, found %s", token.ValueKind(tok.Kind))}
			}
			for j := 0; ; j++ {
				el, err := tz.Next()
				if err != nil {
					return token.Token{}, err
				}
				if el.Kind == token.EndArray {
					return token.Token{}, &PathError{at, fmt.Sprintf("index %d out of range (array has %d elements)", seg.Index, j)}
				}
				if j == seg.Index {
					tok = el
					break
				}
				if err := SkipFrom(tz, el); err != nil {
					return token.Token{}, err
				}
			}
		} else {
			if tok.Kind != token.BeginObject {
				return token.Token{}, &PathError{at, fmt.Sprintf("expected an object, found %s", token.ValueKind(tok.Kind))}
			}
			for {
				k, err := tz.Next()
				if err != nil {
					return token.Token{}, err
				}
				if k.Kind == token.EndObject {
					return token.Token{}, &PathError{at, fmt.Sprintf("key %q not found", seg.Key)}
				}
				name, err := token.Unquote(k.Bytes)
				if err != nil {
					return token.Token{}, err
				}
				val, err := tz.Next()
				if err != nil {
					return token.Token{}, err
				}
				if name == seg.Key {
					tok = val
					break
				}
				if err := SkipFrom(tz, val); err != nil {
					return token.Token{}, err
				}
			}
		}
	}
	return tok, nil
}

// Drain reads the rest of the document so that syntax errors after the
// region of interest are still reported. It stops at io.EOF.
func Drain(tz *token.Tokenizer) error {
	for {
		if _, err := tz.Next(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
