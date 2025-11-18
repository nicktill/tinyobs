package query

import "time"

// TokenType represents the type of token in a query
type TokenType int

const (
	// Literals
	TokenIdentifier TokenType = iota // metric_name, label_name
	TokenNumber                       // 123, 45.67
	TokenString                       // "value"
	TokenDuration                     // 5m, 1h, 7d

	// Operators
	TokenPlus     // +
	TokenMinus    // -
	TokenMultiply // *
	TokenDivide   // /
	TokenPower    // ^
	TokenMod      // %

	// Comparisons
	TokenEqual        // =
	TokenNotEqual     // !=
	TokenEqualEqual   // == (comparison, not label matcher)
	TokenLess         // <
	TokenLessEqual    // <=
	TokenGreater      // >
	TokenGreaterEqual // >=
	TokenMatch        // =~
	TokenNotMatch     // !~

	// Set Operators
	TokenAnd    // and
	TokenOr     // or
	TokenUnless // unless

	// Aggregation Functions (as tokens for better parsing)
	TokenSum        // sum
	TokenAvg        // avg
	TokenMax        // max
	TokenMin        // min
	TokenCount      // count
	TokenStddev     // stddev
	TokenStdvar     // stdvar
	TokenTopK       // topk
	TokenBottomK    // bottomk
	TokenQuantile   // quantile
	TokenCountValues // count_values

	// Delimiters
	TokenLeftParen    // (
	TokenRightParen   // )
	TokenLeftBrace    // {
	TokenRightBrace   // }
	TokenLeftBracket  // [
	TokenRightBracket // ]
	TokenComma        // ,
	TokenColon        // :

	// Keywords
	TokenBy         // by
	TokenWithout    // without
	TokenOn         // on
	TokenIgnoring   // ignoring
	TokenGroupLeft  // group_left
	TokenGroupRight // group_right
	TokenBool       // bool
	TokenOffset     // offset

	// Special
	TokenEOF
	TokenIllegal
)

// Token represents a single token in the query
type Token struct {
	Type    TokenType
	Literal string
	Pos     int // Position in input string
}

// Expr represents a query expression node
type Expr interface {
	expr()
}

// VectorSelector represents a metric selector: metric_name{labels}
type VectorSelector struct {
	Name     string
	Matchers []*LabelMatcher
}

func (v *VectorSelector) expr() {}

// LabelMatcher represents a label matching condition
type LabelMatcher struct {
	Name  string
	Op    TokenType // =, !=, =~, !~
	Value string
}

// RangeSelector represents a range query: metric[5m]
type RangeSelector struct {
	Vector   *VectorSelector
	Duration time.Duration
}

func (r *RangeSelector) expr() {}

// FunctionCall represents a function call: rate(metric[5m])
type FunctionCall struct {
	Name string
	Args []Expr
}

func (f *FunctionCall) expr() {}

// UnaryExpr represents a unary operation: -metric
type UnaryExpr struct {
	Op   TokenType // + or -
	Expr Expr
}

func (u *UnaryExpr) expr() {}

// BinaryExpr represents a binary operation: a + b
type BinaryExpr struct {
	Left     Expr
	Op       TokenType
	Right    Expr
	Matching *VectorMatching // Optional vector matching rules
}

func (b *BinaryExpr) expr() {}

// VectorMatching describes how to match vectors in binary operations
type VectorMatching struct {
	On         bool     // true for "on", false for "ignoring"
	Labels     []string // Labels to match on/ignore
	GroupLeft  bool     // true for group_left
	GroupRight bool     // true for group_right
}

// AggregateExpr represents an aggregation: sum by (label) (metric)
type AggregateExpr struct {
	Op       string   // sum, avg, max, min, count
	Grouping []string // labels to group by
	Expr     Expr
	Without  bool // true for "without", false for "by"
}

func (a *AggregateExpr) expr() {}

// NumberLiteral represents a numeric literal
type NumberLiteral struct {
	Value float64
}

func (n *NumberLiteral) expr() {}

// ParenExpr represents a parenthesized expression
type ParenExpr struct {
	Expr Expr
}

func (p *ParenExpr) expr() {}

// SubqueryExpr represents a subquery: metric[5m:1m]
type SubqueryExpr struct {
	Expr   Expr
	Range  time.Duration // 5m
	Step   time.Duration // 1m (optional)
	Offset time.Duration // offset (optional)
}

func (s *SubqueryExpr) expr() {}

// Query represents a parsed query ready for execution
type Query struct {
	Expr  Expr
	Start time.Time
	End   time.Time
	Step  time.Duration
}
