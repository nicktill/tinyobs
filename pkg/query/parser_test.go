package query

import (
	"testing"
)

func TestLexer(t *testing.T) {
	tests := []struct {
		input    string
		expected []TokenType
	}{
		{
			input:    "http_requests_total",
			expected: []TokenType{TokenIdentifier, TokenEOF},
		},
		{
			input:    "sum(metric)",
			expected: []TokenType{TokenSum, TokenLeftParen, TokenIdentifier, TokenRightParen, TokenEOF},
		},
		{
			input:    "rate(metric[5m])",
			expected: []TokenType{TokenIdentifier, TokenLeftParen, TokenIdentifier, TokenLeftBracket, TokenDuration, TokenRightBracket, TokenRightParen, TokenEOF},
		},
		{
			input:    "metric{label=\"value\"}",
			expected: []TokenType{TokenIdentifier, TokenLeftBrace, TokenIdentifier, TokenEqual, TokenString, TokenRightBrace, TokenEOF},
		},
		{
			input:    "a + b",
			expected: []TokenType{TokenIdentifier, TokenPlus, TokenIdentifier, TokenEOF},
		},
		{
			input:    "a and b",
			expected: []TokenType{TokenIdentifier, TokenAnd, TokenIdentifier, TokenEOF},
		},
		{
			input:    "1.5e10",
			expected: []TokenType{TokenNumber, TokenEOF},
		},
	}

	for _, tt := range tests {
		lexer := NewLexer(tt.input)
		for i, expectedType := range tt.expected {
			tok := lexer.NextToken()
			if tok.Type != expectedType {
				t.Errorf("Test %q token[%d]: expected %v, got %v (literal: %q)", tt.input, i, expectedType, tok.Type, tok.Literal)
			}
		}
	}
}

func TestParserVectorSelector(t *testing.T) {
	input := "http_requests_total"
	parser := NewParser(input)
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	vec, ok := expr.(*VectorSelector)
	if !ok {
		t.Fatalf("Expected VectorSelector, got %T", expr)
	}

	if vec.Name != "http_requests_total" {
		t.Errorf("Expected metric name 'http_requests_total', got %q", vec.Name)
	}
}

func TestParserVectorSelectorWithLabels(t *testing.T) {
	input := `http_requests_total{method="GET",status="200"}`
	parser := NewParser(input)
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	vec, ok := expr.(*VectorSelector)
	if !ok {
		t.Fatalf("Expected VectorSelector, got %T", expr)
	}

	if len(vec.Matchers) != 2 {
		t.Fatalf("Expected 2 matchers, got %d", len(vec.Matchers))
	}

	if vec.Matchers[0].Name != "method" || vec.Matchers[0].Value != "GET" {
		t.Errorf("Expected method=GET, got %s=%s", vec.Matchers[0].Name, vec.Matchers[0].Value)
	}

	if vec.Matchers[1].Name != "status" || vec.Matchers[1].Value != "200" {
		t.Errorf("Expected status=200, got %s=%s", vec.Matchers[1].Name, vec.Matchers[1].Value)
	}
}

func TestParserRangeSelector(t *testing.T) {
	input := "http_requests_total[5m]"
	parser := NewParser(input)
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	rangeExpr, ok := expr.(*RangeSelector)
	if !ok {
		t.Fatalf("Expected RangeSelector, got %T", expr)
	}

	if rangeExpr.Vector.Name != "http_requests_total" {
		t.Errorf("Expected metric name 'http_requests_total', got %q", rangeExpr.Vector.Name)
	}

	expectedDuration := int64(5 * 60) // 5 minutes in seconds
	actualDuration := int64(rangeExpr.Duration.Seconds())
	if actualDuration != expectedDuration {
		t.Errorf("Expected duration %d seconds, got %d", expectedDuration, actualDuration)
	}
}

func TestParserFunctionCall(t *testing.T) {
	input := "rate(http_requests_total[5m])"
	parser := NewParser(input)
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	fn, ok := expr.(*FunctionCall)
	if !ok {
		t.Fatalf("Expected FunctionCall, got %T", expr)
	}

	if fn.Name != "rate" {
		t.Errorf("Expected function name 'rate', got %q", fn.Name)
	}

	if len(fn.Args) != 1 {
		t.Fatalf("Expected 1 argument, got %d", len(fn.Args))
	}

	rangeExpr, ok := fn.Args[0].(*RangeSelector)
	if !ok {
		t.Fatalf("Expected RangeSelector argument, got %T", fn.Args[0])
	}

	if rangeExpr.Vector.Name != "http_requests_total" {
		t.Errorf("Expected metric name 'http_requests_total', got %q", rangeExpr.Vector.Name)
	}
}

func TestParserBinaryExpression(t *testing.T) {
	input := "a + b"
	parser := NewParser(input)
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	binExpr, ok := expr.(*BinaryExpr)
	if !ok {
		t.Fatalf("Expected BinaryExpr, got %T", expr)
	}

	if binExpr.Op != TokenPlus {
		t.Errorf("Expected + operator, got %v", binExpr.Op)
	}

	leftVec, ok := binExpr.Left.(*VectorSelector)
	if !ok || leftVec.Name != "a" {
		t.Errorf("Expected left operand 'a', got %T", binExpr.Left)
	}

	rightVec, ok := binExpr.Right.(*VectorSelector)
	if !ok || rightVec.Name != "b" {
		t.Errorf("Expected right operand 'b', got %T", binExpr.Right)
	}
}

func TestParserAggregation(t *testing.T) {
	input := "sum by (instance) (http_requests_total)"
	parser := NewParser(input)
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	agg, ok := expr.(*AggregateExpr)
	if !ok {
		t.Fatalf("Expected AggregateExpr, got %T", expr)
	}

	if agg.Op != "sum" {
		t.Errorf("Expected aggregation 'sum', got %q", agg.Op)
	}

	if agg.Without {
		t.Error("Expected 'by' grouping, got 'without'")
	}

	if len(agg.Grouping) != 1 || agg.Grouping[0] != "instance" {
		t.Errorf("Expected grouping by 'instance', got %v", agg.Grouping)
	}

	vec, ok := agg.Expr.(*VectorSelector)
	if !ok || vec.Name != "http_requests_total" {
		t.Errorf("Expected metric 'http_requests_total', got %T", agg.Expr)
	}
}

func TestParserOperatorPrecedence(t *testing.T) {
	input := "a + b * c"
	parser := NewParser(input)
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Should parse as: a + (b * c)
	binExpr, ok := expr.(*BinaryExpr)
	if !ok || binExpr.Op != TokenPlus {
		t.Fatalf("Expected top-level + operator, got %T", expr)
	}

	// Right side should be b * c
	rightBin, ok := binExpr.Right.(*BinaryExpr)
	if !ok || rightBin.Op != TokenMultiply {
		t.Fatalf("Expected right side to be * operator, got %T", binExpr.Right)
	}
}

func TestParserUnaryExpression(t *testing.T) {
	input := "-metric"
	parser := NewParser(input)
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	unary, ok := expr.(*UnaryExpr)
	if !ok {
		t.Fatalf("Expected UnaryExpr, got %T", expr)
	}

	if unary.Op != TokenMinus {
		t.Errorf("Expected - operator, got %v", unary.Op)
	}

	vec, ok := unary.Expr.(*VectorSelector)
	if !ok || vec.Name != "metric" {
		t.Errorf("Expected metric selector, got %T", unary.Expr)
	}
}
