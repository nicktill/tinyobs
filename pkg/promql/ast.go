// Package promql implements the subset of PromQL that TinyObs supports.
//
// Supported features match Prometheus's results; that is checked by running
// Prometheus's own test files against this engine (see conformance_test.go).
// Anything outside the subset is rejected with an error that names the
// feature, never approximated.
package promql

import (
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
)

// ValueType is the type an expression evaluates to.
type ValueType string

const (
	TypeScalar ValueType = "scalar"
	TypeVector ValueType = "vector"
	TypeMatrix ValueType = "matrix"
	TypeString ValueType = "string"
)

// Expr is a node of the expression tree.
type Expr interface {
	Type() ValueType
}

// NumberLiteral is a constant, e.g. 42 or 1e3.
type NumberLiteral struct{ Val float64 }

// StringLiteral is a quoted string, used as a function argument.
type StringLiteral struct{ Val string }

// VectorSelector selects series, e.g. http_requests_total{code="200"}.
type VectorSelector struct {
	Name     string
	Matchers []*labels.Matcher // always includes the __name__ matcher if Name is set
	Offset   time.Duration
}

// MatrixSelector selects a time range of samples, e.g. foo[5m].
type MatrixSelector struct {
	VectorSelector *VectorSelector
	Range          time.Duration
}

// SubqueryExpr evaluates an instant expression at regular steps over a range,
// producing a range vector, e.g. max_over_time(rate(x[5m])[1h:1m]).
type SubqueryExpr struct {
	Expr   Expr
	Range  time.Duration
	Step   time.Duration // 0 means the default step
	Offset time.Duration
}

// Call is a function call.
type Call struct {
	Func *function
	Args []Expr
}

// AggregateExpr is an aggregation, e.g. sum by (job) (x).
type AggregateExpr struct {
	Op       string
	Expr     Expr
	Param    Expr // topk, bottomk, quantile
	Grouping []string
	Without  bool
}

// BinaryExpr is a binary operation.
type BinaryExpr struct {
	Op         string
	LHS, RHS   Expr
	ReturnBool bool
	Matching   *VectorMatching // nil unless both sides are vectors
}

// Cardinality of a vector match.
type Cardinality int

const (
	CardOneToOne Cardinality = iota
	CardManyToOne
	CardOneToMany
	CardManyToMany // set operators
)

// VectorMatching describes how samples of two vectors are paired.
type VectorMatching struct {
	Card    Cardinality
	On      bool     // on(...) rather than ignoring(...)
	Labels  []string // the on/ignoring labels
	Include []string // group_left/group_right extra labels
}

// UnaryExpr is a unary minus or plus.
type UnaryExpr struct {
	Op   string
	Expr Expr
}

// ParenExpr is a parenthesized expression.
type ParenExpr struct{ Expr Expr }

func (e *NumberLiteral) Type() ValueType  { return TypeScalar }
func (e *StringLiteral) Type() ValueType  { return TypeString }
func (e *VectorSelector) Type() ValueType { return TypeVector }
func (e *MatrixSelector) Type() ValueType { return TypeMatrix }
func (e *SubqueryExpr) Type() ValueType   { return TypeMatrix }
func (e *Call) Type() ValueType           { return e.Func.ReturnType }
func (e *AggregateExpr) Type() ValueType  { return TypeVector }
func (e *UnaryExpr) Type() ValueType      { return e.Expr.Type() }
func (e *ParenExpr) Type() ValueType      { return e.Expr.Type() }
func (e *BinaryExpr) Type() ValueType {
	if e.LHS.Type() == TypeScalar && e.RHS.Type() == TypeScalar {
		return TypeScalar
	}
	return TypeVector
}

func isComparison(op string) bool {
	switch op {
	case "==", "!=", ">", "<", ">=", "<=":
		return true
	}
	return false
}

func isSetOp(op string) bool { return op == "and" || op == "or" || op == "unless" }
