// Package inspect answers "where is the array in this thing?" without
// loading the document: it walks the stream once and reports the shape —
// paths, types, array lengths, object key counts — down to a depth limit.
// Arrays are summarized from their first element (shown as `path[]`) and
// the rest are counted, so a ten-million-row export costs one pass and a
// few kilobytes.
package inspect

import (
	"fmt"
	"io"

	"github.com/JaydenCJ/jsonsaw/internal/stream"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

// Entry describes one node of the document, in document order.
type Entry struct {
	Path  string `json:"path"`            // "." for the root, then ".users", ".users[]", …
	Kind  string `json:"kind"`            // object, array, string, number, boolean, null
	Count *int   `json:"count,omitempty"` // elements (array) or keys (object)
}

// Label renders an entry's kind with its count, e.g. `array[10240]` or
// `object{3}` — the form used by `jsonsaw paths` text output.
func (e Entry) Label() string {
	if e.Count == nil {
		return e.Kind
	}
	if e.Kind == "object" {
		return fmt.Sprintf("object{%d}", *e.Count)
	}
	return fmt.Sprintf("%s[%d]", e.Kind, *e.Count)
}

// Inspect walks the single top-level value of r and returns entries for
// every node at depth ≤ maxDepth (the root is depth 0 and always present).
// Containers below the cutoff are still traversed — their counts are exact
// — but their children are not reported.
func Inspect(r io.Reader, maxDepth int) ([]Entry, error) {
	tz := token.New(r)
	first, err := tz.Next()
	if err != nil {
		return nil, err
	}
	w := &walker{tz: tz, maxDepth: maxDepth}
	if err := w.value(first, ".", 0); err != nil {
		return nil, err
	}
	if err := stream.Drain(tz); err != nil {
		return nil, err
	}
	return w.entries, nil
}

type walker struct {
	tz       *token.Tokenizer
	maxDepth int
	entries  []Entry
}

// value visits one value whose first token is tok. It appends an entry if
// the value is within the depth budget, and always consumes the value.
func (w *walker) value(tok token.Token, path string, depth int) error {
	emit := depth <= w.maxDepth

	switch tok.Kind {
	case token.BeginObject:
		idx := -1
		if emit {
			w.entries = append(w.entries, Entry{Path: path, Kind: "object"})
			idx = len(w.entries) - 1
		}
		n := 0
		for {
			k, err := w.tz.Next()
			if err != nil {
				return err
			}
			if k.Kind == token.EndObject {
				break
			}
			name, err := token.Unquote(k.Bytes)
			if err != nil {
				return err
			}
			val, err := w.tz.Next()
			if err != nil {
				return err
			}
			if depth < w.maxDepth {
				if err := w.value(val, childPath(path, name), depth+1); err != nil {
					return err
				}
			} else if err := stream.SkipFrom(w.tz, val); err != nil {
				return err
			}
			n++
		}
		if idx >= 0 {
			c := n
			w.entries[idx].Count = &c
		}

	case token.BeginArray:
		idx := -1
		if emit {
			w.entries = append(w.entries, Entry{Path: path, Kind: "array"})
			idx = len(w.entries) - 1
		}
		n := 0
		for {
			el, err := w.tz.Next()
			if err != nil {
				return err
			}
			if el.Kind == token.EndArray {
				break
			}
			// Sample the shape of the first element only; homogeneous
			// arrays are the overwhelmingly common case and sampling keeps
			// the report readable for million-element exports.
			if n == 0 && depth < w.maxDepth {
				if err := w.value(el, path+"[]", depth+1); err != nil {
					return err
				}
			} else if err := stream.SkipFrom(w.tz, el); err != nil {
				return err
			}
			n++
		}
		if idx >= 0 {
			c := n
			w.entries[idx].Count = &c
		}

	default: // scalar
		if emit {
			w.entries = append(w.entries, Entry{Path: path, Kind: token.ValueKind(tok.Kind)})
		}
	}
	return nil
}

// childPath joins a parent path with a key, quoting keys that would not
// survive a round trip through the path parser (dots, brackets, quotes).
func childPath(parent, key string) string {
	if parent == "." {
		parent = ""
	}
	if plainKey(key) {
		return parent + "." + key
	}
	quoted := make([]byte, 0, len(key)+2)
	quoted = append(quoted, '"')
	for i := 0; i < len(key); i++ {
		if key[i] == '"' || key[i] == '\\' {
			quoted = append(quoted, '\\')
		}
		quoted = append(quoted, key[i])
	}
	quoted = append(quoted, '"')
	return parent + "." + string(quoted)
}

func plainKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		switch c := key[i]; {
		case c == '.' || c == '[' || c == ']' || c == '"' || c == '\\':
			return false
		case c <= ' ':
			return false
		}
	}
	return true
}

// Count streams the array whose first token is first and returns its
// element count. Used by `jsonsaw count` after navigation.
func Count(tz *token.Tokenizer, first token.Token, at string) (int, error) {
	if first.Kind != token.BeginArray {
		return 0, &stream.PathError{
			Path: at,
			Msg:  fmt.Sprintf("expected an array to count, found %s", token.ValueKind(first.Kind)),
		}
	}
	n := 0
	for {
		el, err := tz.Next()
		if err != nil {
			return n, err
		}
		if el.Kind == token.EndArray {
			return n, nil
		}
		if err := stream.SkipFrom(tz, el); err != nil {
			return n, err
		}
		n++
	}
}
