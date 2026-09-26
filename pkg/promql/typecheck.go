package promql

import "fmt"

func typeName(t ValueType) string {
	switch t {
	case TypeVector:
		return "instant vector"
	case TypeMatrix:
		return "range vector"
	}
	return string(t)
}

// checkTypes verifies operand types throughout the tree, the way Prometheus's
// parser does, and fills in default vector matching for binary operators.
func checkTypes(e Expr) error {
	fail := func(format string, args ...any) error {
		return &ParseError{Msg: fmt.Sprintf(format, args...)}
	}
	switch n := e.(type) {
	case *ParenExpr:
		return checkTypes(n.Expr)
	case *UnaryExpr:
		if err := checkTypes(n.Expr); err != nil {
			return err
		}
		if t := n.Expr.Type(); t != TypeScalar && t != TypeVector {
			return fail("unary expression only allowed on expressions of type scalar or instant vector, got %q", typeName(t))
		}
	case *SubqueryExpr:
		if err := checkTypes(n.Expr); err != nil {
			return err
		}
		if t := n.Expr.Type(); t != TypeVector {
			return fail("subquery is only allowed on instant vector, got %s instead", typeName(t))
		}
	case *MatrixSelector, *VectorSelector, *NumberLiteral, *StringLiteral:
	case *AggregateExpr:
		if err := checkTypes(n.Expr); err != nil {
			return err
		}
		if t := n.Expr.Type(); t != TypeVector {
			return fail("expected type instant vector in aggregation expression, got %s", typeName(t))
		}
		if n.Param != nil {
			if err := checkTypes(n.Param); err != nil {
				return err
			}
			if t := n.Param.Type(); t != TypeScalar {
				return fail("expected type scalar in aggregation parameter, got %s", typeName(t))
			}
		}
	case *Call:
		f := n.Func
		min, max := len(f.ArgTypes)-f.Optional, len(f.ArgTypes)
		if f.Variadic {
			max = -1
		}
		if len(n.Args) < min || (max >= 0 && len(n.Args) > max) {
			return fail("expected %d argument(s) in call to %q, got %d", min, f.Name, len(n.Args))
		}
		for i, a := range n.Args {
			if err := checkTypes(a); err != nil {
				return err
			}
			want := f.ArgTypes[len(f.ArgTypes)-1]
			if i < len(f.ArgTypes) {
				want = f.ArgTypes[i]
			}
			if got := a.Type(); got != want {
				return fail("expected type %s in call to function %q, got %s", typeName(want), f.Name, typeName(got))
			}
		}
	case *BinaryExpr:
		if err := checkTypes(n.LHS); err != nil {
			return err
		}
		if err := checkTypes(n.RHS); err != nil {
			return err
		}
		lt, rt := n.LHS.Type(), n.RHS.Type()
		for _, t := range []ValueType{lt, rt} {
			if t != TypeScalar && t != TypeVector {
				return fail("binary expression must contain only scalar and instant vector types")
			}
		}
		bothVectors := lt == TypeVector && rt == TypeVector
		if isComparison(n.Op) && !n.ReturnBool && lt == TypeScalar && rt == TypeScalar {
			return fail("comparisons between scalars must use BOOL modifier")
		}
		if isSetOp(n.Op) && !bothVectors {
			return fail("set operator %q not allowed in binary scalar expression", n.Op)
		}
		if n.Matching != nil && !bothVectors {
			return fail("vector matching only allowed between instant vectors")
		}
		if bothVectors {
			if n.Matching == nil {
				n.Matching = &VectorMatching{}
			}
			if isSetOp(n.Op) {
				n.Matching.Card = CardManyToMany
			}
		}
	default:
		return fail("unknown expression type %T", e)
	}
	return nil
}
