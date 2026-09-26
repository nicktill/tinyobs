package promql

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
)

// ErrUnsupported is wrapped by errors for valid PromQL that TinyObs does not
// implement, so callers can tell "not supported" from "malformed".
var ErrUnsupported = fmt.Errorf("not supported by TinyObs")

// Parse parses a PromQL expression and checks its types.
func Parse(input string) (Expr, error) {
	toks, err := lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	expr, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEOF {
		return nil, p.errorf(t, "unexpected %s", t)
	}
	if err := checkTypes(expr); err != nil {
		return nil, err
	}
	return expr, nil
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token {
	t := p.toks[p.i]
	if t.kind != tokEOF {
		p.i++
	}
	return t
}

func (p *parser) errorf(t token, format string, args ...any) error {
	return &ParseError{Pos: t.pos, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) unsupported(t token, feature string) error {
	return &ParseError{Pos: t.pos, Msg: fmt.Sprintf("%s: %v", feature, ErrUnsupported), Err: ErrUnsupported}
}

func (p *parser) isOp(val string) bool {
	t := p.peek()
	return t.kind == tokOp && t.val == val
}

func (p *parser) expectOp(val string) (token, error) {
	t := p.next()
	if t.kind != tokOp || t.val != val {
		return t, p.errorf(t, "expected %q, got %s", val, t)
	}
	return t, nil
}

// Binary operator precedence, lowest first. ^ is right-associative.
func precedence(op string) int {
	switch op {
	case "or":
		return 1
	case "and", "unless":
		return 2
	case "==", "!=", "<", "<=", ">", ">=":
		return 3
	case "+", "-":
		return 4
	case "*", "/", "%", "atan2":
		return 5
	case "^":
		return 6
	}
	return 0
}

// binaryOp returns the operator at the current position, if any.
func (p *parser) binaryOp() string {
	t := p.peek()
	switch t.kind {
	case tokOp:
		if precedence(t.val) > 0 {
			return t.val
		}
	case tokIdent:
		if op := strings.ToLower(t.val); op == "and" || op == "or" || op == "unless" || op == "atan2" {
			return op
		}
	}
	return ""
}

// parseExpr parses a binary expression whose operators bind tighter than minPrec.
func (p *parser) parseExpr(minPrec int) (Expr, error) {
	lhs, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		op := p.binaryOp()
		prec := precedence(op)
		if op == "" || prec <= minPrec {
			return lhs, nil
		}
		opTok := p.next()
		bin := &BinaryExpr{Op: op, LHS: lhs}
		if err := p.parseModifiers(bin, opTok); err != nil {
			return nil, err
		}
		next := prec
		if op == "^" {
			next = prec - 1 // right-associative
		}
		rhs, err := p.parseExpr(next)
		if err != nil {
			return nil, err
		}
		bin.RHS = rhs
		lhs = bin
	}
}

// parseModifiers reads bool, on/ignoring and group_left/group_right after an operator.
func (p *parser) parseModifiers(bin *BinaryExpr, opTok token) error {
	if t := p.peek(); t.kind == tokIdent && strings.ToLower(t.val) == "bool" {
		if !isComparison(bin.Op) {
			return p.errorf(t, "bool modifier can only be used on comparison operators")
		}
		p.next()
		bin.ReturnBool = true
	}
	t := p.peek()
	if t.kind != tokIdent {
		return nil
	}
	kw := strings.ToLower(t.val)
	if kw != "on" && kw != "ignoring" {
		return nil
	}
	p.next()
	m := &VectorMatching{On: kw == "on"}
	var err error
	if m.Labels, err = p.parseLabelList(); err != nil {
		return err
	}
	if t := p.peek(); t.kind == tokIdent {
		switch strings.ToLower(t.val) {
		case "group_left", "group_right":
			if isSetOp(bin.Op) {
				return p.errorf(t, "no grouping allowed for %q operation", bin.Op)
			}
			p.next()
			m.Card = CardManyToOne
			if strings.ToLower(t.val) == "group_right" {
				m.Card = CardOneToMany
			}
			if p.isOp("(") {
				if m.Include, err = p.parseLabelList(); err != nil {
					return err
				}
			}
			for _, l := range m.Include {
				for _, on := range m.Labels {
					if m.On && l == on {
						return p.errorf(t, "label %q must not occur in ON and GROUP clause at once", l)
					}
				}
			}
		}
	}
	bin.Matching = m
	return nil
}

func (p *parser) parseUnary() (Expr, error) {
	if t := p.peek(); t.kind == tokOp && (t.val == "-" || t.val == "+") {
		p.next()
		// Unary minus binds looser than ^: -2^2 is -(2^2).
		e, err := p.parseExpr(precedence("*"))
		if err != nil {
			return nil, err
		}
		if n, ok := e.(*NumberLiteral); ok {
			if t.val == "-" {
				n.Val = -n.Val
			}
			return n, nil
		}
		return &UnaryExpr{Op: t.val, Expr: e}, nil
	}
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return p.parsePostfix(e)
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tokNumber:
		p.next()
		v, err := parseNumber(t.val)
		if err != nil {
			return nil, p.errorf(t, "%s", err)
		}
		return &NumberLiteral{v}, nil
	case tokDuration:
		return nil, p.errorf(t, "unexpected duration %q", t.val)
	case tokString:
		p.next()
		return &StringLiteral{t.val}, nil
	case tokOp:
		switch t.val {
		case "(":
			p.next()
			e, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if _, err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return &ParenExpr{e}, nil
		case "{":
			return p.parseSelector("", t)
		}
		return nil, p.errorf(t, "unexpected %s", t)
	case tokIdent:
		name := t.val
		lower := strings.ToLower(name)
		switch lower {
		case "inf", "nan":
			if !(p.i+1 < len(p.toks) && p.toks[p.i+1].kind == tokOp && (p.toks[p.i+1].val == "{" || p.toks[p.i+1].val == "(")) {
				p.next()
				if lower == "inf" {
					return &NumberLiteral{math.Inf(1)}, nil
				}
				return &NumberLiteral{math.NaN()}, nil
			}
		}
		if isAggregation(lower) && p.nextIsAggregationStart() {
			return p.parseAggregation()
		}
		if p.i+1 < len(p.toks) && p.toks[p.i+1].kind == tokOp && p.toks[p.i+1].val == "(" {
			return p.parseCall()
		}
		if isKeyword(name) {
			return nil, p.errorf(t, "unexpected %s", t)
		}
		p.next()
		return p.parseSelector(name, t)
	case tokEOF:
		return nil, p.errorf(t, "unexpected end of input")
	}
	return nil, p.errorf(t, "unexpected %s", t)
}

func (p *parser) nextIsAggregationStart() bool {
	if p.i+1 >= len(p.toks) {
		return false
	}
	n := p.toks[p.i+1]
	if n.kind == tokOp && n.val == "(" {
		return true
	}
	return n.kind == tokIdent && (strings.ToLower(n.val) == "by" || strings.ToLower(n.val) == "without")
}

func parseNumber(s string) (float64, error) {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err := strconv.ParseInt(s[2:], 16, 64)
		return float64(v), err
	}
	return strconv.ParseFloat(s, 64)
}

// parseSelector parses the rest of a vector selector after an optional name.
func (p *parser) parseSelector(name string, start token) (Expr, error) {
	vs := &VectorSelector{Name: name}
	if p.isOp("{") {
		p.next()
		for !p.isOp("}") {
			m, err := p.parseMatcher()
			if err != nil {
				return nil, err
			}
			if m.Name == labels.MetricName && name != "" {
				return nil, p.errorf(start, "metric name must not be set twice: %q or %q", name, m.Value)
			}
			vs.Matchers = append(vs.Matchers, m)
			if p.isOp(",") {
				p.next()
				continue
			}
			if !p.isOp("}") {
				return nil, p.errorf(p.peek(), "unexpected %s in label matching, expected \",\" or \"}\"", p.peek())
			}
		}
		p.next()
	}
	if name != "" {
		vs.Matchers = append(vs.Matchers, labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, name))
	}
	nonEmpty := false
	for _, m := range vs.Matchers {
		if !m.MatchesEmpty() {
			nonEmpty = true
		}
	}
	if !nonEmpty {
		return nil, p.errorf(start, "vector selector must contain at least one non-empty matcher")
	}
	return vs, nil
}

func (p *parser) parseMatcher() (*labels.Matcher, error) {
	t := p.next()
	if t.kind != tokIdent && t.kind != tokString {
		return nil, p.errorf(t, "unexpected %s in label matching, expected label name", t)
	}
	opTok := p.next()
	var mt labels.MatchType
	switch {
	case opTok.kind == tokOp && opTok.val == "=":
		mt = labels.MatchEqual
	case opTok.kind == tokOp && opTok.val == "!=":
		mt = labels.MatchNotEqual
	case opTok.kind == tokOp && opTok.val == "=~":
		mt = labels.MatchRegexp
	case opTok.kind == tokOp && opTok.val == "!~":
		mt = labels.MatchNotRegexp
	default:
		return nil, p.errorf(opTok, "unexpected %s in label matching, expected one of \"=\", \"!=\", \"=~\", \"!~\"", opTok)
	}
	v := p.next()
	if v.kind != tokString {
		return nil, p.errorf(v, "unexpected %s in label matching, expected string", v)
	}
	m, err := labels.NewMatcher(mt, t.val, v.val)
	if err != nil {
		return nil, p.errorf(v, "%s", err)
	}
	return m, nil
}

// parsePostfix handles [range], offset and @ after an expression.
func (p *parser) parsePostfix(e Expr) (Expr, error) {
	for {
		t := p.peek()
		switch {
		case t.kind == tokOp && t.val == "[":
			p.next()
			d, err := p.parseDuration()
			if err != nil {
				return nil, err
			}
			if p.isOp(":") {
				p.next()
				var step time.Duration
				if !p.isOp("]") {
					if step, err = p.parseDuration(); err != nil {
						return nil, err
					}
				}
				if _, err := p.expectOp("]"); err != nil {
					return nil, err
				}
				if e.Type() != TypeVector {
					return nil, p.errorf(t, "subquery is only allowed on instant vector, got %s instead", typeName(e.Type()))
				}
				if d <= 0 {
					return nil, p.errorf(t, "subquery range must be positive")
				}
				e = &SubqueryExpr{Expr: e, Range: d, Step: step}
				continue
			}
			if nt := p.peek(); nt.kind == tokOp && nt.val != "]" {
				return nil, p.unsupported(nt, "duration expressions")
			}
			if _, err := p.expectOp("]"); err != nil {
				return nil, err
			}
			vs, ok := e.(*VectorSelector)
			if !ok {
				return nil, p.errorf(t, "ranges only allowed for vector selectors")
			}
			if vs.Offset != 0 {
				return nil, p.errorf(t, "offset must follow the range selector")
			}
			if d <= 0 {
				return nil, p.errorf(t, "range must be positive")
			}
			e = &MatrixSelector{VectorSelector: vs, Range: d}
		case t.kind == tokIdent && strings.ToLower(t.val) == "offset":
			p.next()
			neg := false
			if p.isOp("-") {
				p.next()
				neg = true
			} else if p.isOp("+") {
				p.next()
			}
			d, err := p.parseDuration()
			if err != nil {
				return nil, err
			}
			if neg {
				d = -d
			}
			var vs *VectorSelector
			switch s := e.(type) {
			case *VectorSelector:
				vs = s
			case *MatrixSelector:
				vs = s.VectorSelector
			case *SubqueryExpr:
				if s.Offset != 0 {
					return nil, p.errorf(t, "offset may not be set multiple times")
				}
				s.Offset = d
				continue
			default:
				return nil, p.errorf(t, "offset modifier must be preceded by an instant vector selector or range vector selector")
			}
			if vs.Offset != 0 {
				return nil, p.errorf(t, "offset may not be set multiple times")
			}
			vs.Offset = d
		case t.kind == tokOp && t.val == "@":
			return nil, p.unsupported(t, "the @ modifier")
		default:
			return e, nil
		}
	}
}

// parseDuration reads a duration literal, or a number of seconds. Arithmetic
// on durations (Prometheus's experimental duration expressions) is rejected
// as unsupported.
func (p *parser) parseDuration() (time.Duration, error) {
	t := p.next()
	if t.kind == tokIdent || (t.kind == tokOp && (t.val == "(" || t.val == "-" || t.val == "+")) {
		return 0, p.unsupported(t, "duration expressions")
	}
	switch t.kind {
	case tokDuration:
		d, err := ParseDuration(t.val)
		if err != nil {
			return 0, p.errorf(t, "%s", err)
		}
		return d, nil
	case tokNumber:
		v, err := parseNumber(t.val)
		if err != nil || v > float64(math.MaxInt64)/1e9 {
			return 0, p.errorf(t, "invalid duration %q", t.val)
		}
		return time.Duration(v * float64(time.Second)), nil
	}
	return 0, p.errorf(t, "unexpected %s, expected duration", t)
}

// ParseDuration parses a Prometheus duration such as 5m, 1h30m or 2d.
// Units: ms, s, m, h, d, w, y (365 days).
func ParseDuration(s string) (time.Duration, error) {
	if s == "" || durationLen(s) != len(s) {
		return 0, fmt.Errorf("not a valid duration string: %q", s)
	}
	units := map[string]time.Duration{
		"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour,
		"d": 24 * time.Hour, "w": 7 * 24 * time.Hour, "y": 365 * 24 * time.Hour,
	}
	var total time.Duration
	i := 0
	for i < len(s) {
		j := i
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		n, err := strconv.ParseInt(s[i:j], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("not a valid duration string: %q", s)
		}
		unit := ""
		for _, u := range durationUnits {
			if strings.HasPrefix(s[j:], u) {
				unit = u
				break
			}
		}
		if n > int64(math.MaxInt64/units[unit]) {
			return 0, fmt.Errorf("duration out of range: %q", s)
		}
		total += time.Duration(n) * units[unit]
		if total < 0 {
			return 0, fmt.Errorf("duration out of range: %q", s)
		}
		i = j + len(unit)
	}
	return total, nil
}

func (p *parser) parseLabelList() ([]string, error) {
	if _, err := p.expectOp("("); err != nil {
		return nil, err
	}
	var out []string
	for !p.isOp(")") {
		t := p.next()
		if t.kind != tokIdent && t.kind != tokString {
			return nil, p.errorf(t, "unexpected %s in grouping opts, expected label", t)
		}
		out = append(out, t.val)
		if p.isOp(",") {
			p.next()
		} else if !p.isOp(")") {
			return nil, p.errorf(p.peek(), "unexpected %s in grouping opts, expected \")\" or \",\"", p.peek())
		}
	}
	p.next()
	return out, nil
}

func (p *parser) parseCall() (Expr, error) {
	nameTok := p.next()
	fn, ok := functions[nameTok.val]
	if !ok {
		if unsupportedFunctions[nameTok.val] {
			return nil, p.unsupported(nameTok, fmt.Sprintf("function %q", nameTok.val))
		}
		return nil, p.errorf(nameTok, "unknown function with name %q", nameTok.val)
	}
	p.next() // (
	var args []Expr
	for !p.isOp(")") {
		a, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if p.isOp(",") {
			p.next()
			if p.isOp(")") {
				return nil, p.errorf(p.peek(), "trailing comma in function call")
			}
		} else if !p.isOp(")") {
			return nil, p.errorf(p.peek(), "unexpected %s in function call, expected \",\" or \")\"", p.peek())
		}
	}
	p.next()
	return &Call{Func: fn, Args: args}, nil
}

var aggregations = map[string]bool{
	"sum": true, "avg": true, "min": true, "max": true, "count": true, "group": true,
	"stddev": true, "stdvar": true, "topk": true, "bottomk": true, "quantile": true,
	"count_values": true, "limitk": true, "limit_ratio": true,
}

func isAggregation(s string) bool { return aggregations[s] }

func (p *parser) parseAggregation() (Expr, error) {
	opTok := p.next()
	op := strings.ToLower(opTok.val)
	switch op {
	case "count_values", "limitk", "limit_ratio":
		return nil, p.unsupported(opTok, fmt.Sprintf("aggregation %q", op))
	}
	agg := &AggregateExpr{Op: op}
	parseGrouping := func() error {
		t := p.peek()
		if t.kind != tokIdent {
			return nil
		}
		switch strings.ToLower(t.val) {
		case "by", "without":
			if agg.Grouping != nil || agg.Without {
				return p.errorf(t, "grouping may only be given once")
			}
			p.next()
			agg.Without = strings.ToLower(t.val) == "without"
			g, err := p.parseLabelList()
			if err != nil {
				return err
			}
			if g == nil {
				g = []string{}
			}
			agg.Grouping = g
		}
		return nil
	}
	if err := parseGrouping(); err != nil {
		return nil, err
	}
	if _, err := p.expectOp("("); err != nil {
		return nil, err
	}
	var args []Expr
	for !p.isOp(")") {
		a, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if p.isOp(",") {
			p.next()
		} else if !p.isOp(")") {
			return nil, p.errorf(p.peek(), "unexpected %s in aggregation, expected \",\" or \")\"", p.peek())
		}
	}
	p.next()
	if agg.Grouping == nil && !agg.Without {
		if err := parseGrouping(); err != nil {
			return nil, err
		}
	}
	wantArgs := 1
	if op == "topk" || op == "bottomk" || op == "quantile" {
		wantArgs = 2
	}
	if len(args) != wantArgs {
		return nil, p.errorf(opTok, "wrong number of arguments for aggregate expression provided, expected %d, got %d", wantArgs, len(args))
	}
	if wantArgs == 2 {
		agg.Param, agg.Expr = args[0], args[1]
	} else {
		agg.Expr = args[0]
	}
	return agg, nil
}
