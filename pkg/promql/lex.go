package promql

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokNumber
	tokDuration
	tokString
	tokOp // operators and punctuation; the literal says which
)

type token struct {
	kind tokenKind
	val  string // for strings: the unquoted value
	pos  int
}

func (t token) String() string {
	switch t.kind {
	case tokEOF:
		return "end of input"
	case tokString:
		return fmt.Sprintf("string %q", t.val)
	}
	return fmt.Sprintf("%q", t.val)
}

// ParseError is a syntax or type error with its byte offset in the query.
type ParseError struct {
	Pos int
	Msg string
	Err error // underlying cause, e.g. ErrUnsupported
}

func (e *ParseError) Error() string { return fmt.Sprintf("parse error at char %d: %s", e.Pos+1, e.Msg) }

func (e *ParseError) Unwrap() error { return e.Err }

// lex splits a query into tokens.
func lex(input string) ([]token, error) {
	var toks []token
	i := 0
	errAt := func(pos int, format string, args ...any) error {
		return &ParseError{Pos: pos, Msg: fmt.Sprintf(format, args...)}
	}
	for i < len(input) {
		c := input[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#': // comment to end of line
			for i < len(input) && input[i] != '\n' {
				i++
			}
		case c == ':' && len(toks) > 0 && (toks[len(toks)-1].kind == tokDuration || toks[len(toks)-1].kind == tokNumber):
			// After a duration, ':' separates a subquery's range and step.
			toks = append(toks, token{tokOp, ":", i})
			i++
		case isIdentStart(c):
			start := i
			for i < len(input) && isIdentChar(input[i]) {
				i++
			}
			toks = append(toks, token{tokIdent, input[start:i], start})
		case isDigit(c) || (c == '.' && i+1 < len(input) && isDigit(input[i+1])):
			start := i
			tok, n, err := lexNumberOrDuration(input[i:])
			if err != nil {
				return nil, errAt(start, "%s", err)
			}
			tok.pos = start
			toks = append(toks, tok)
			i += n
		case c == '"' || c == '\'' || c == '`':
			start := i
			s, n, err := unquote(input[i:])
			if err != nil {
				return nil, errAt(start, "%s", err)
			}
			toks = append(toks, token{tokString, s, start})
			i += n
		default:
			op := ""
			for _, cand := range []string{"==", "!=", ">=", "<=", "=~", "!~", "+", "-", "*", "/", "%", "^", "=", ">", "<", "(", ")", "{", "}", "[", "]", ",", ":", "@"} {
				if strings.HasPrefix(input[i:], cand) {
					op = cand
					break
				}
			}
			if op == "" {
				r, _ := utf8.DecodeRuneInString(input[i:])
				return nil, errAt(i, "unexpected character %q", r)
			}
			toks = append(toks, token{tokOp, op, i})
			i += len(op)
		}
	}
	return append(toks, token{tokEOF, "", len(input)}), nil
}

func isIdentStart(c byte) bool {
	return c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) }
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }

// lexNumberOrDuration reads a number (decimal, hex, exponent) or a duration
// such as 5m or 1h30m. Inf and NaN are identifiers and handled by the parser.
func lexNumberOrDuration(s string) (token, int, error) {
	// Duration: digits followed immediately by a unit, possibly repeated.
	if n := durationLen(s); n > 0 {
		return token{kind: tokDuration, val: s[:n]}, n, nil
	}
	i := 0
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		i = 2
		for i < len(s) && strings.ContainsRune("0123456789abcdefABCDEF", rune(s[i])) {
			i++
		}
	} else {
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if i < len(s) && s[i] == '.' {
			i++
			for i < len(s) && isDigit(s[i]) {
				i++
			}
		}
		if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
			j := i + 1
			if j < len(s) && (s[j] == '+' || s[j] == '-') {
				j++
			}
			if j < len(s) && isDigit(s[j]) {
				i = j
				for i < len(s) && isDigit(s[i]) {
					i++
				}
			}
		}
	}
	if i < len(s) && ((isIdentChar(s[i]) && s[i] != ':') || s[i] == '.') {
		return token{}, 0, fmt.Errorf("bad number or duration syntax %q", s[:i+1])
	}
	return token{kind: tokNumber, val: s[:i]}, i, nil
}

var durationUnits = []string{"ms", "y", "w", "d", "h", "m", "s"}

// durationLen returns the length of a duration literal at the start of s, or 0.
func durationLen(s string) int {
	i := 0
	for {
		j := i
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		if j == i {
			break
		}
		unit := ""
		for _, u := range durationUnits {
			if strings.HasPrefix(s[j:], u) {
				unit = u
				break
			}
		}
		if unit == "" {
			break
		}
		i = j + len(unit)
	}
	if i > 0 && i < len(s) && isIdentChar(s[i]) && s[i] != ':' {
		return 0 // e.g. "5mx": not a duration; "5m:" is a subquery range
	}
	return i
}

// unquote reads a quoted string at the start of s: double or single quotes
// with Go escapes, or backquotes with no escapes.
func unquote(s string) (string, int, error) {
	q := s[0]
	if q == '`' {
		end := strings.IndexByte(s[1:], '`')
		if end < 0 {
			return "", 0, fmt.Errorf("unterminated raw string")
		}
		return s[1 : end+1], end + 2, nil
	}
	var b strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		switch {
		case c == q:
			return b.String(), i + 1, nil
		case c == '\n':
			return "", 0, fmt.Errorf("unterminated quoted string")
		case c == '\\':
			if i+1 >= len(s) {
				return "", 0, fmt.Errorf("unterminated quoted string")
			}
			n, err := unescape(s[i:], q, &b)
			if err != nil {
				return "", 0, err
			}
			i += n
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				return "", 0, fmt.Errorf("invalid UTF-8 in string")
			}
			b.WriteRune(r)
			i += size
		}
	}
	return "", 0, fmt.Errorf("unterminated quoted string")
}

func unescape(s string, quote byte, b *strings.Builder) (int, error) {
	c := s[1]
	simple := map[byte]byte{'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\'}
	if r, ok := simple[c]; ok {
		b.WriteByte(r)
		return 2, nil
	}
	if c == quote {
		b.WriteByte(c)
		return 2, nil
	}
	hexVal := func(h string) (rune, bool) {
		var v rune
		for _, ch := range h {
			switch {
			case ch >= '0' && ch <= '9':
				v = v*16 + ch - '0'
			case ch >= 'a' && ch <= 'f':
				v = v*16 + ch - 'a' + 10
			case ch >= 'A' && ch <= 'F':
				v = v*16 + ch - 'A' + 10
			default:
				return 0, false
			}
		}
		return v, true
	}
	switch c {
	case 'x':
		if len(s) >= 4 {
			if v, ok := hexVal(s[2:4]); ok {
				b.WriteByte(byte(v))
				return 4, nil
			}
		}
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if len(s) >= 2+n {
			if v, ok := hexVal(s[2 : 2+n]); ok && utf8.ValidRune(v) {
				b.WriteRune(v)
				return 2 + n, nil
			}
		}
	case '0', '1', '2', '3', '4', '5', '6', '7':
		if len(s) >= 4 {
			v := 0
			ok := true
			for _, ch := range s[1:4] {
				if ch < '0' || ch > '7' {
					ok = false
					break
				}
				v = v*8 + int(ch-'0')
			}
			if ok && v <= 255 {
				b.WriteByte(byte(v))
				return 4, nil
			}
		}
	}
	return 0, fmt.Errorf("invalid escape sequence %q", s[:2])
}

// isKeyword reports reserved words that cannot start an expression as a
// metric name without braces.
func isKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "and", "or", "unless", "by", "without", "on", "ignoring", "group_left", "group_right", "bool", "offset", "atan2":
		return true
	}
	return false
}
