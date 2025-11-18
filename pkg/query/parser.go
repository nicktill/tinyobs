package query

import (
	"fmt"
	"strconv"
	"time"
)

// Parser parses PromQL-like query expressions using recursive descent
type Parser struct {
	lexer   *Lexer
	current Token
	peek    Token
}

// NewParser creates a new parser for the given input
func NewParser(input string) *Parser {
	p := &Parser{lexer: NewLexer(input)}
	// Read two tokens to initialize current and peek
	p.nextToken()
	p.nextToken()
	return p
}

// Parse parses the input and returns an expression or error
func (p *Parser) Parse() (Expr, error) {
	expr := p.parseExpression()
	if p.current.Type != TokenEOF {
		return nil, fmt.Errorf("unexpected token after expression: %s", p.current.Literal)
	}
	return expr, nil
}

// nextToken advances to the next token
func (p *Parser) nextToken() {
	p.current = p.peek
	p.peek = p.lexer.NextToken()
}

// parseExpression is the entry point for expression parsing
// Handles operator precedence: OR > AND > UNLESS > Comparison > Add/Sub > Mul/Div > Power > Unary
func (p *Parser) parseExpression() Expr {
	return p.parseOrExpression()
}

// parseOrExpression parses OR expressions (lowest precedence)
func (p *Parser) parseOrExpression() Expr {
	left := p.parseAndExpression()

	for p.current.Type == TokenOr {
		op := p.current.Type
		p.nextToken()
		right := p.parseAndExpression()
		left = &BinaryExpr{Left: left, Op: op, Right: right}
	}

	return left
}

// parseAndExpression parses AND/UNLESS expressions
func (p *Parser) parseAndExpression() Expr {
	left := p.parseUnlessExpression()

	for p.current.Type == TokenAnd {
		op := p.current.Type
		p.nextToken()
		right := p.parseUnlessExpression()
		left = &BinaryExpr{Left: left, Op: op, Right: right}
	}

	return left
}

// parseUnlessExpression parses UNLESS expressions
func (p *Parser) parseUnlessExpression() Expr {
	left := p.parseComparisonExpression()

	for p.current.Type == TokenUnless {
		op := p.current.Type
		p.nextToken()
		right := p.parseComparisonExpression()
		left = &BinaryExpr{Left: left, Op: op, Right: right}
	}

	return left
}

// parseComparisonExpression parses comparison expressions (==, !=, <, <=, >, >=)
func (p *Parser) parseComparisonExpression() Expr {
	left := p.parseAdditiveExpression()

	if p.isComparisonOp(p.current.Type) {
		op := p.current.Type
		p.nextToken()
		right := p.parseAdditiveExpression()
		return &BinaryExpr{Left: left, Op: op, Right: right}
	}

	return left
}

// parseAdditiveExpression parses addition and subtraction
func (p *Parser) parseAdditiveExpression() Expr {
	left := p.parseMultiplicativeExpression()

	for p.current.Type == TokenPlus || p.current.Type == TokenMinus {
		op := p.current.Type
		p.nextToken()
		right := p.parseMultiplicativeExpression()
		left = &BinaryExpr{Left: left, Op: op, Right: right}
	}

	return left
}

// parseMultiplicativeExpression parses multiplication, division, and modulo
func (p *Parser) parseMultiplicativeExpression() Expr {
	left := p.parsePowerExpression()

	for p.current.Type == TokenMultiply || p.current.Type == TokenDivide || p.current.Type == TokenMod {
		op := p.current.Type
		p.nextToken()
		right := p.parsePowerExpression()
		left = &BinaryExpr{Left: left, Op: op, Right: right}
	}

	return left
}

// parsePowerExpression parses power expressions (^)
func (p *Parser) parsePowerExpression() Expr {
	left := p.parseUnaryExpression()

	if p.current.Type == TokenPower {
		op := p.current.Type
		p.nextToken()
		right := p.parsePowerExpression() // Right associative
		return &BinaryExpr{Left: left, Op: op, Right: right}
	}

	return left
}

// parseUnaryExpression parses unary expressions (+expr, -expr)
func (p *Parser) parseUnaryExpression() Expr {
	if p.current.Type == TokenPlus || p.current.Type == TokenMinus {
		op := p.current.Type
		p.nextToken()
		expr := p.parseUnaryExpression()
		return &UnaryExpr{Op: op, Expr: expr}
	}

	return p.parsePrimaryExpression()
}

// parsePrimaryExpression parses primary expressions (numbers, vectors, functions, aggregations)
func (p *Parser) parsePrimaryExpression() Expr {
	switch p.current.Type {
	case TokenNumber:
		return p.parseNumber()
	case TokenLeftParen:
		return p.parseParenExpression()
	case TokenIdentifier:
		return p.parseVectorOrFunction()
	case TokenSum, TokenAvg, TokenMax, TokenMin, TokenCount, TokenStddev, TokenStdvar,
		TokenTopK, TokenBottomK, TokenQuantile, TokenCountValues:
		return p.parseAggregation()
	default:
		// Error: unexpected token
		return &NumberLiteral{Value: 0} // Return dummy value for now
	}
}

// parseNumber parses a number literal
func (p *Parser) parseNumber() Expr {
	val, _ := strconv.ParseFloat(p.current.Literal, 64)
	p.nextToken()
	return &NumberLiteral{Value: val}
}

// parseParenExpression parses a parenthesized expression
func (p *Parser) parseParenExpression() Expr {
	p.nextToken() // consume '('
	expr := p.parseExpression()
	if p.current.Type != TokenRightParen {
		// Error: expected ')'
		return expr
	}
	p.nextToken() // consume ')'
	return &ParenExpr{Expr: expr}
}

// parseVectorOrFunction parses either a vector selector or function call
func (p *Parser) parseVectorOrFunction() Expr {
	name := p.current.Literal
	p.nextToken()

	// Check if it's a function call
	if p.current.Type == TokenLeftParen {
		return p.parseFunctionCall(name)
	}

	// Otherwise it's a vector selector
	return p.parseVectorSelector(name)
}

// parseVectorSelector parses a vector selector: metric_name{label="value"}[5m]
func (p *Parser) parseVectorSelector(name string) Expr {
	vector := &VectorSelector{Name: name}

	// Parse label matchers {label="value"}
	if p.current.Type == TokenLeftBrace {
		vector.Matchers = p.parseLabelMatchers()
	}

	// Check for range selector [5m]
	if p.current.Type == TokenLeftBracket {
		return p.parseRangeSelector(vector)
	}

	return vector
}

// parseLabelMatchers parses label matchers: {label="value", label2!="value2"}
func (p *Parser) parseLabelMatchers() []*LabelMatcher {
	matchers := []*LabelMatcher{}
	p.nextToken() // consume '{'

	for p.current.Type != TokenRightBrace && p.current.Type != TokenEOF {
		matcher := &LabelMatcher{}

		// Parse label name
		if p.current.Type != TokenIdentifier {
			break
		}
		matcher.Name = p.current.Literal
		p.nextToken()

		// Parse operator (=, !=, =~, !~)
		if !p.isLabelMatchOp(p.current.Type) {
			break
		}
		matcher.Op = p.current.Type
		p.nextToken()

		// Parse value
		if p.current.Type != TokenString {
			break
		}
		matcher.Value = p.current.Literal
		p.nextToken()

		matchers = append(matchers, matcher)

		// Handle comma
		if p.current.Type == TokenComma {
			p.nextToken()
		}
	}

	if p.current.Type == TokenRightBrace {
		p.nextToken() // consume '}'
	}

	return matchers
}

// parseRangeSelector parses a range selector: [5m]
func (p *Parser) parseRangeSelector(vector *VectorSelector) Expr {
	p.nextToken() // consume '['

	// Parse duration
	if p.current.Type != TokenDuration {
		return vector
	}

	duration := p.parseDuration(p.current.Literal)
	p.nextToken()

	if p.current.Type == TokenRightBracket {
		p.nextToken() // consume ']'
	}

	return &RangeSelector{Vector: vector, Duration: duration}
}

// parseFunctionCall parses a function call: rate(metric[5m])
func (p *Parser) parseFunctionCall(name string) Expr {
	p.nextToken() // consume '('

	args := []Expr{}
	for p.current.Type != TokenRightParen && p.current.Type != TokenEOF {
		arg := p.parseExpression()
		args = append(args, arg)

		if p.current.Type == TokenComma {
			p.nextToken()
		} else {
			break
		}
	}

	if p.current.Type == TokenRightParen {
		p.nextToken() // consume ')'
	}

	return &FunctionCall{Name: name, Args: args}
}

// parseAggregation parses aggregation expressions: sum by (label) (metric)
func (p *Parser) parseAggregation() Expr {
	op := p.current.Literal
	p.nextToken()

	agg := &AggregateExpr{Op: op}

	// Parse optional grouping: by (label) or without (label)
	if p.current.Type == TokenBy || p.current.Type == TokenWithout {
		agg.Without = (p.current.Type == TokenWithout)
		p.nextToken()

		if p.current.Type == TokenLeftParen {
			agg.Grouping = p.parseGroupingLabels()
		}
	}

	// Parse the expression to aggregate
	if p.current.Type == TokenLeftParen {
		p.nextToken() // consume '('
		agg.Expr = p.parseExpression()
		if p.current.Type == TokenRightParen {
			p.nextToken() // consume ')'
		}
	}

	return agg
}

// parseGroupingLabels parses grouping labels: (label1, label2)
func (p *Parser) parseGroupingLabels() []string {
	labels := []string{}
	p.nextToken() // consume '('

	for p.current.Type == TokenIdentifier {
		labels = append(labels, p.current.Literal)
		p.nextToken()

		if p.current.Type == TokenComma {
			p.nextToken()
		} else {
			break
		}
	}

	if p.current.Type == TokenRightParen {
		p.nextToken() // consume ')'
	}

	return labels
}

// Helper functions

func (p *Parser) isComparisonOp(t TokenType) bool {
	return t == TokenEqualEqual || t == TokenNotEqual || t == TokenLess ||
		t == TokenLessEqual || t == TokenGreater || t == TokenGreaterEqual
}

func (p *Parser) isLabelMatchOp(t TokenType) bool {
	return t == TokenEqual || t == TokenNotEqual || t == TokenMatch || t == TokenNotMatch
}

// parseDuration converts duration string like "5m" to time.Duration
func (p *Parser) parseDuration(s string) time.Duration {
	// Simple duration parsing (can be enhanced)
	var value int64
	var unit string

	// Extract number and unit
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') {
		value = value*10 + int64(s[i]-'0')
		i++
	}
	unit = s[i:]

	// Convert to duration
	switch unit {
	case "s":
		return time.Duration(value) * time.Second
	case "m":
		return time.Duration(value) * time.Minute
	case "h":
		return time.Duration(value) * time.Hour
	case "d":
		return time.Duration(value) * 24 * time.Hour
	case "w":
		return time.Duration(value) * 7 * 24 * time.Hour
	case "y":
		return time.Duration(value) * 365 * 24 * time.Hour
	default:
		return 0
	}
}
