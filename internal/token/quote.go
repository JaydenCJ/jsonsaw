// String quoting and unquoting for JSON. Unquote is what lets jsonsaw
// compare object keys against a --path without allocating per element;
// Quote is used when `jsonsaw join --path` has to synthesize wrapper keys.
package token

import (
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// Unquote decodes the raw bytes of a String or Key token (including the
// surrounding quotes) into the string it denotes. Escapes are resolved;
// lone UTF-16 surrogates become U+FFFD, matching encoding/json. The input
// is assumed to have passed the tokenizer, but malformed escapes still
// return an error rather than panic.
func Unquote(raw []byte) (string, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("not a quoted string: %q", raw)
	}
	s := raw[1 : len(raw)-1]

	// Fast path: no escapes means the payload is the string.
	hasEscape := false
	for _, c := range s {
		if c == '\\' {
			hasEscape = true
			break
		}
	}
	if !hasEscape {
		return string(s), nil
	}

	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		if i+1 >= len(s) {
			return "", fmt.Errorf("truncated escape at offset %d", i)
		}
		switch e := s[i+1]; e {
		case '"', '\\', '/':
			out = append(out, e)
			i += 2
		case 'b':
			out = append(out, '\b')
			i += 2
		case 'f':
			out = append(out, '\f')
			i += 2
		case 'n':
			out = append(out, '\n')
			i += 2
		case 'r':
			out = append(out, '\r')
			i += 2
		case 't':
			out = append(out, '\t')
			i += 2
		case 'u':
			r, n, err := decodeHexRune(s[i:])
			if err != nil {
				return "", err
			}
			i += n
			if utf16.IsSurrogate(r) {
				if r2, n2, err2 := decodeHexRune(s[i:]); err2 == nil {
					if paired := utf16.DecodeRune(r, r2); paired != utf8.RuneError {
						r = paired
						i += n2
					} else {
						r = utf8.RuneError
					}
				} else {
					r = utf8.RuneError
				}
			}
			out = utf8.AppendRune(out, r)
		default:
			return "", fmt.Errorf("invalid escape '\\%c'", e)
		}
	}
	return string(out), nil
}

// decodeHexRune reads a leading `\uXXXX` sequence and returns the code
// unit plus the number of bytes consumed (always 6 on success).
func decodeHexRune(s []byte) (rune, int, error) {
	if len(s) < 6 || s[0] != '\\' || s[1] != 'u' {
		return 0, 0, fmt.Errorf(`expected \uXXXX escape`)
	}
	var r rune
	for _, c := range s[2:6] {
		var v rune
		switch {
		case c >= '0' && c <= '9':
			v = rune(c - '0')
		case c >= 'a' && c <= 'f':
			v = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = rune(c-'A') + 10
		default:
			return 0, 0, fmt.Errorf(`invalid hex digit %q in \u escape`, c)
		}
		r = r<<4 | v
	}
	return r, 6, nil
}

// Quote encodes s as a JSON string, quotes included. Non-ASCII runes pass
// through as UTF-8; only quotes, backslashes, and control characters are
// escaped, so quoted keys stay human-readable.
func Quote(s string) []byte {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			out = append(out, '\\', '"')
		case c == '\\':
			out = append(out, '\\', '\\')
		case c == '\n':
			out = append(out, '\\', 'n')
		case c == '\r':
			out = append(out, '\\', 'r')
		case c == '\t':
			out = append(out, '\\', 't')
		case c == '\b':
			out = append(out, '\\', 'b')
		case c == '\f':
			out = append(out, '\\', 'f')
		case c < 0x20:
			out = append(out, fmt.Sprintf(`\u%04x`, c)...)
		default:
			out = append(out, c)
		}
	}
	return append(out, '"')
}
