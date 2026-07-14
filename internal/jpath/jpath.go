// Package jpath parses the small path language jsonsaw uses to point at a
// value inside a document: dot-separated object keys plus bracketed array
// indexes, e.g. `data.users`, `results[0].rows`, or `payload."weird.key"`.
//
// The language is deliberately tiny — no wildcards, no filters, no
// recursion — because navigation has to work in one forward pass over a
// stream. Bare numeric segments are object keys; array indexing is always
// written with brackets.
package jpath

import (
	"fmt"
	"strings"
)

// Segment is one step of a path: either an object key or an array index.
type Segment struct {
	Key     string
	Index   int
	IsIndex bool
}

// Path is a sequence of segments; nil or empty means the document root.
type Path []Segment

// ParseError reports where in the path string parsing failed.
type ParseError struct {
	Input string
	Pos   int // 0-based byte offset
	Msg   string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("invalid path %q at offset %d: %s", e.Input, e.Pos, e.Msg)
}

// Parse turns a path string into a Path. "" and "." both mean the root.
// A single leading dot is tolerated (`.users` == `users`) so paths copied
// from `jsonsaw paths` output work directly.
func Parse(s string) (Path, error) {
	if s == "" || s == "." {
		return nil, nil
	}
	in := s
	i := 0
	if s[0] == '.' {
		i++
	}
	var p Path
	needSegment := true // a key or index must follow
	for i < len(s) {
		switch c := s[i]; {
		case c == '.':
			if needSegment {
				return nil, &ParseError{in, i, "empty segment"}
			}
			i++
			needSegment = true
		case c == '[':
			start := i
			i++
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			if j == i {
				return nil, &ParseError{in, i, "index must be one or more digits"}
			}
			if j >= len(s) || s[j] != ']' {
				return nil, &ParseError{in, start, "unterminated index (missing ']')"}
			}
			n := 0
			for _, d := range s[i:j] {
				n = n*10 + int(d-'0')
				if n > 1<<31 {
					return nil, &ParseError{in, start, "index too large"}
				}
			}
			p = append(p, Segment{Index: n, IsIndex: true})
			i = j + 1
			needSegment = false
		case c == '"':
			if !needSegment {
				return nil, &ParseError{in, i, "expected '.' or '[' before segment"}
			}
			key, next, err := parseQuoted(in, s, i)
			if err != nil {
				return nil, err
			}
			p = append(p, Segment{Key: key})
			i = next
			needSegment = false
		case c == ']':
			return nil, &ParseError{in, i, "unexpected ']'"}
		default:
			if !needSegment {
				return nil, &ParseError{in, i, "expected '.' or '[' before segment"}
			}
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' && s[j] != ']' && s[j] != '"' {
				j++
			}
			p = append(p, Segment{Key: s[i:j]})
			i = j
			needSegment = false
		}
	}
	if needSegment {
		return nil, &ParseError{in, len(s), "trailing '.'"}
	}
	return p, nil
}

// parseQuoted scans a double-quoted segment starting at s[i] == '"'.
// Backslash escapes `\"` and `\\` are supported; everything else is
// literal, so keys containing dots or brackets can be addressed.
func parseQuoted(in, s string, i int) (key string, next int, err error) {
	start := i
	i++
	var b strings.Builder
	for i < len(s) {
		switch c := s[i]; c {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			if i+1 >= len(s) {
				return "", 0, &ParseError{in, i, "truncated escape in quoted segment"}
			}
			e := s[i+1]
			if e != '"' && e != '\\' {
				return "", 0, &ParseError{in, i, fmt.Sprintf("unsupported escape '\\%c' in quoted segment", e)}
			}
			b.WriteByte(e)
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", 0, &ParseError{in, start, "unterminated quoted segment"}
}

// String renders the path back into parseable form. Keys that need it are
// quoted; the root renders as ".".
func (p Path) String() string {
	if len(p) == 0 {
		return "."
	}
	var b strings.Builder
	for i, seg := range p {
		if seg.IsIndex {
			fmt.Fprintf(&b, "[%d]", seg.Index)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		if bareSafe(seg.Key) {
			b.WriteString(seg.Key)
		} else {
			b.WriteByte('"')
			for _, c := range []byte(seg.Key) {
				if c == '"' || c == '\\' {
					b.WriteByte('\\')
				}
				b.WriteByte(c)
			}
			b.WriteByte('"')
		}
	}
	return b.String()
}

// bareSafe reports whether a key round-trips without quoting.
func bareSafe(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		switch c := key[i]; {
		case c == '.' || c == '[' || c == ']' || c == '"' || c == '\\':
			return false
		case c <= ' ': // control characters and spaces
			return false
		}
	}
	return true
}
