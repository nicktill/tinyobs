package query

import (
	"strings"
	"unicode"
)

// Lexer tokenizes query strings
type Lexer struct {
	input   string
	pos     int  // current position
	readPos int  // next read position
	ch      byte // current character
}

// NewLexer creates a new lexer for the given input
func NewLexer(input string) *Lexer {
	l := &Lexer{input: input}
	l.readChar()
	return l
}

// readChar advances to the next character
func (l *Lexer) readChar() {
	if l.readPos >= len(l.input) {
		l.ch = 0 // EOF
	} else {
		l.ch = l.input[l.readPos]
	}
	l.pos = l.readPos
	l.readPos++
}

// peekChar looks at the next character without advancing
func (l *Lexer) peekChar() byte {
	if l.readPos >= len(l.input) {
		return 0
	}
	return l.input[l.readPos]
}

// NextToken returns the next token from the input
func (l *Lexer) NextToken() Token {
	var tok Token

	l.skipWhitespace()

	tok.Pos = l.pos

	switch l.ch {
	case '(':
		tok = Token{Type: TokenLeftParen, Literal: string(l.ch)}
	case ')':
		tok = Token{Type: TokenRightParen, Literal: string(l.ch)}
	case '{':
		tok = Token{Type: TokenLeftBrace, Literal: string(l.ch)}
	case '}':
		tok = Token{Type: TokenRightBrace, Literal: string(l.ch)}
	case '[':
		tok = Token{Type: TokenLeftBracket, Literal: string(l.ch)}
	case ']':
		tok = Token{Type: TokenRightBracket, Literal: string(l.ch)}
	case ',':
		tok = Token{Type: TokenComma, Literal: string(l.ch)}
	case ':':
		tok = Token{Type: TokenColon, Literal: string(l.ch)}
	case '+':
		tok = Token{Type: TokenPlus, Literal: string(l.ch)}
	case '-':
		tok = Token{Type: TokenMinus, Literal: string(l.ch)}
	case '*':
		tok = Token{Type: TokenMultiply, Literal: string(l.ch)}
	case '/':
		tok = Token{Type: TokenDivide, Literal: string(l.ch)}
	case '^':
		tok = Token{Type: TokenPower, Literal: string(l.ch)}
	case '%':
		tok = Token{Type: TokenMod, Literal: string(l.ch)}
	case '=':
		if l.peekChar() == '~' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenMatch, Literal: string(ch) + string(l.ch)}
		} else if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenEqualEqual, Literal: string(ch) + string(l.ch)}
		} else {
			tok = Token{Type: TokenEqual, Literal: string(l.ch)}
		}
	case '!':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenNotEqual, Literal: string(ch) + string(l.ch)}
		} else if l.peekChar() == '~' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenNotMatch, Literal: string(ch) + string(l.ch)}
		} else {
			tok = Token{Type: TokenIllegal, Literal: string(l.ch)}
		}
	case '<':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenLessEqual, Literal: string(ch) + string(l.ch)}
		} else {
			tok = Token{Type: TokenLess, Literal: string(l.ch)}
		}
	case '>':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenGreaterEqual, Literal: string(ch) + string(l.ch)}
		} else {
			tok = Token{Type: TokenGreater, Literal: string(l.ch)}
		}
	case '"', '\'':
		tok.Type = TokenString
		tok.Literal = l.readString(l.ch)
	case 0:
		tok = Token{Type: TokenEOF, Literal: ""}
	default:
		if isLetter(l.ch) {
			tok.Literal = l.readIdentifier()
			tok.Type = lookupKeyword(tok.Literal)
			return tok
		} else if isDigit(l.ch) {
			tok.Type = TokenNumber
			tok.Literal = l.readNumber()
			// Check if it's a duration (5m, 1h, etc.)
			if isLetter(l.ch) {
				tok.Literal += l.readDurationUnit()
				tok.Type = TokenDuration
			}
			return tok
		} else {
			tok = Token{Type: TokenIllegal, Literal: string(l.ch)}
		}
	}

	l.readChar()
	return tok
}

// skipWhitespace skips whitespace and comments
func (l *Lexer) skipWhitespace() {
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\n' || l.ch == '\r' {
		l.readChar()
	}
	// Skip comments
	if l.ch == '#' {
		for l.ch != '\n' && l.ch != 0 {
			l.readChar()
		}
		l.skipWhitespace()
	}
}

// readIdentifier reads an identifier (metric name, label name, function name)
func (l *Lexer) readIdentifier() string {
	pos := l.pos
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' || l.ch == ':' {
		l.readChar()
	}
	return l.input[pos:l.pos]
}

// readNumber reads a number (integer or float with scientific notation)
func (l *Lexer) readNumber() string {
	pos := l.pos

	// Read integer part (with optional underscores)
	for isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}

	// Handle decimal point
	if l.ch == '.' && isDigit(l.peekChar()) {
		l.readChar() // consume '.'
		for isDigit(l.ch) || l.ch == '_' {
			l.readChar()
		}
	}

	// Handle scientific notation (e or E)
	if l.ch == 'e' || l.ch == 'E' {
		l.readChar()
		if l.ch == '+' || l.ch == '-' {
			l.readChar()
		}
		for isDigit(l.ch) || l.ch == '_' {
			l.readChar()
		}
	}

	return l.input[pos:l.pos]
}

// readDurationUnit reads duration unit (m, h, d, etc.)
func (l *Lexer) readDurationUnit() string {
	pos := l.pos
	for isLetter(l.ch) {
		l.readChar()
	}
	return l.input[pos:l.pos]
}

// readString reads a quoted string
func (l *Lexer) readString(quote byte) string {
	pos := l.pos + 1 // skip opening quote
	for {
		l.readChar()
		if l.ch == quote || l.ch == 0 {
			break
		}
		// Handle escape sequences
		if l.ch == '\\' {
			l.readChar()
		}
	}
	return l.input[pos:l.pos]
}

// isLetter checks if character is a letter
func isLetter(ch byte) bool {
	return unicode.IsLetter(rune(ch))
}

// isDigit checks if character is a digit
func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

// lookupKeyword checks if identifier is a keyword or aggregation function
func lookupKeyword(ident string) TokenType {
	keywords := map[string]TokenType{
		// Keywords
		"by":          TokenBy,
		"without":     TokenWithout,
		"on":          TokenOn,
		"ignoring":    TokenIgnoring,
		"group_left":  TokenGroupLeft,
		"group_right": TokenGroupRight,
		"bool":        TokenBool,
		"offset":      TokenOffset,
		// Set operators
		"and":    TokenAnd,
		"or":     TokenOr,
		"unless": TokenUnless,
		// Aggregation functions
		"sum":          TokenSum,
		"avg":          TokenAvg,
		"max":          TokenMax,
		"min":          TokenMin,
		"count":        TokenCount,
		"stddev":       TokenStddev,
		"stdvar":       TokenStdvar,
		"topk":         TokenTopK,
		"bottomk":      TokenBottomK,
		"quantile":     TokenQuantile,
		"count_values": TokenCountValues,
	}

	if tok, ok := keywords[strings.ToLower(ident)]; ok {
		return tok
	}
	return TokenIdentifier
}
