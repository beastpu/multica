package workflow

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// ExprScope is what a `when` expression is allowed to reference: which upstream
// nodes declare which output fields. Fields maps an unqualified key to the node
// keys declaring it — an unqualified reference is only legal when exactly one
// node declares the key. FieldTypes is keyed by "node.key" and carries the
// declaration so operators can be checked against the field type at parse time.
type ExprScope struct {
	Fields     map[string][]string
	FieldTypes map[string]OutputField
}

// ExprPool carries the runtime variable pool: node key → normalized outputs of
// that node's latest valid submission. A missing node or key fails closed.
type ExprPool map[string]map[string]any

// Expr is a parsed `when` expression. The source string is what templates
// store; parsing happens at validation time and again at evaluation time, and
// both go through the same scope so they cannot disagree.
type Expr struct {
	root exprNode
}

type exprNode interface {
	evaluate(pool ExprPool) bool
	collectNodes(into map[string]struct{})
}

type exprAnd struct{ left, right exprNode }
type exprOr struct{ left, right exprNode }
type exprNot struct{ child exprNode }

type exprComparison struct {
	node   string
	key    string
	op     string
	value  any   // for scalar operators
	values []any // for in / not in
}

func (e exprAnd) evaluate(pool ExprPool) bool {
	return e.left.evaluate(pool) && e.right.evaluate(pool)
}
func (e exprOr) evaluate(pool ExprPool) bool {
	return e.left.evaluate(pool) || e.right.evaluate(pool)
}
func (e exprNot) evaluate(pool ExprPool) bool { return !e.child.evaluate(pool) }

func (e exprAnd) collectNodes(into map[string]struct{}) {
	e.left.collectNodes(into)
	e.right.collectNodes(into)
}
func (e exprOr) collectNodes(into map[string]struct{}) {
	e.left.collectNodes(into)
	e.right.collectNodes(into)
}
func (e exprNot) collectNodes(into map[string]struct{}) { e.child.collectNodes(into) }
func (e exprComparison) collectNodes(into map[string]struct{}) {
	into[e.node] = struct{}{}
}

// evaluate fails closed: an absent node or field makes every operator false,
// including != and not in. An unanswered upstream must not satisfy anything —
// that is what routes the run to the gateway's else case.
func (e exprComparison) evaluate(pool ExprPool) bool {
	values, ok := pool[e.node]
	if !ok {
		return false
	}
	actual, ok := values[e.key]
	if !ok || actual == nil {
		return false
	}
	switch e.op {
	case "==", "!=":
		equal := exprValuesEqual(actual, e.value)
		if e.op == "!=" {
			return !equal
		}
		return equal
	case "in", "not_in":
		found := slices.ContainsFunc(e.values, func(candidate any) bool {
			return exprValuesEqual(actual, candidate)
		})
		if e.op == "not_in" {
			return !found
		}
		return found
	case ">", ">=", "<", "<=":
		left, leftOK := conditionNumber(actual)
		right, rightOK := conditionNumber(e.value)
		if !leftOK || !rightOK {
			return false
		}
		switch e.op {
		case ">":
			return left > right
		case ">=":
			return left >= right
		case "<":
			return left < right
		default:
			return left <= right
		}
	}
	return false
}

func exprValuesEqual(actual, expected any) bool {
	if leftNumber, ok := conditionNumber(actual); ok {
		if rightNumber, ok := conditionNumber(expected); ok {
			return leftNumber == rightNumber
		}
		return false
	}
	return actual == expected
}

// Evaluate reports whether the expression matches the pool.
func (e *Expr) Evaluate(pool ExprPool) (bool, error) {
	if e == nil || e.root == nil {
		return false, fmt.Errorf("expression is empty")
	}
	return e.root.evaluate(pool), nil
}

// Nodes lists the node keys the expression reads, for upstream-reachability
// validation.
func (e *Expr) Nodes() []string {
	seen := map[string]struct{}{}
	e.root.collectNodes(seen)
	nodes := make([]string, 0, len(seen))
	for node := range seen {
		nodes = append(nodes, node)
	}
	slices.Sort(nodes)
	return nodes
}

const maxExprLength = 2048

// ParseExpr parses a `when` expression against the scope. Grammar:
//
//	expr       := or
//	or         := and ("||" and)*
//	and        := unary ("&&" unary)*
//	unary      := "!" unary | "(" expr ")" | comparison
//	comparison := ref op literal | ref ("in" | "not in") "[" literal, ... "]"
//	ref        := ident | ident "." ident
//	op         := "==" | "!=" | ">" | ">=" | "<" | "<="
//
// That is the whole language: no arithmetic, no functions, no string
// concatenation. A judgment the grammar cannot express is a semantic judgment
// and belongs in a classifier activity, not in a second DSL.
func ParseExpr(source string, scope ExprScope) (*Expr, error) {
	if len(source) > maxExprLength {
		return nil, fmt.Errorf("expression exceeds %d characters", maxExprLength)
	}
	parser := &exprParser{source: source, scope: scope}
	parser.next()
	root, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.token.kind != tokenEOF {
		return nil, fmt.Errorf("unexpected %q after expression", parser.token.text)
	}
	return &Expr{root: root}, nil
}

type exprTokenKind int

const (
	tokenEOF exprTokenKind = iota
	tokenIdent
	tokenString
	tokenNumber
	tokenOperator // == != > >= < <= && || ! ( ) [ ] , .
)

type exprToken struct {
	kind exprTokenKind
	text string
}

type exprParser struct {
	source string
	offset int
	token  exprToken
	scope  ExprScope
	err    error
}

func (p *exprParser) next() {
	if p.err != nil {
		return
	}
	for p.offset < len(p.source) && unicode.IsSpace(rune(p.source[p.offset])) {
		p.offset++
	}
	if p.offset >= len(p.source) {
		p.token = exprToken{kind: tokenEOF}
		return
	}
	rest := p.source[p.offset:]
	for _, operator := range []string{"==", "!=", ">=", "<=", "&&", "||"} {
		if strings.HasPrefix(rest, operator) {
			p.token = exprToken{kind: tokenOperator, text: operator}
			p.offset += len(operator)
			return
		}
	}
	switch rest[0] {
	case '>', '<', '!', '(', ')', '[', ']', ',', '.', '=':
		p.token = exprToken{kind: tokenOperator, text: rest[:1]}
		p.offset++
		return
	case '"':
		end := 1
		for end < len(rest) && rest[end] != '"' {
			end++
		}
		if end >= len(rest) {
			p.err = fmt.Errorf("unterminated string literal")
			p.token = exprToken{kind: tokenEOF}
			return
		}
		p.token = exprToken{kind: tokenString, text: rest[1:end]}
		p.offset += end + 1
		return
	}
	if isExprIdentByte(rest[0]) || rest[0] == '-' {
		end := 1
		for end < len(rest) && (isExprIdentByte(rest[end]) || rest[end] == '.') {
			end++
		}
		text := rest[:end]
		p.offset += end
		if _, err := strconv.ParseFloat(text, 64); err == nil {
			p.token = exprToken{kind: tokenNumber, text: text}
			return
		}
		p.token = exprToken{kind: tokenIdent, text: text}
		return
	}
	p.err = fmt.Errorf("unexpected character %q", rest[0])
	p.token = exprToken{kind: tokenEOF}
}

func isExprIdentByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func (p *exprParser) parseOr() (exprNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.token.kind == tokenOperator && p.token.text == "||" {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = exprOr{left: left, right: right}
	}
	return left, nil
}

func (p *exprParser) parseAnd() (exprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.token.kind == tokenOperator && p.token.text == "&&" {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = exprAnd{left: left, right: right}
	}
	return left, nil
}

func (p *exprParser) parseUnary() (exprNode, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.token.kind == tokenOperator && p.token.text == "!" {
		p.next()
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return exprNot{child: child}, nil
	}
	if p.token.kind == tokenOperator && p.token.text == "(" {
		p.next()
		child, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.token.kind != tokenOperator || p.token.text != ")" {
			return nil, fmt.Errorf("expected closing parenthesis")
		}
		p.next()
		return child, nil
	}
	return p.parseComparison()
}

func (p *exprParser) parseComparison() (exprNode, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.token.kind != tokenIdent {
		return nil, fmt.Errorf("expected a field reference, got %q", p.token.text)
	}
	node, key, field, err := p.resolveReference(p.token.text)
	if err != nil {
		return nil, err
	}
	p.next()

	if p.token.kind == tokenIdent && (p.token.text == "in" || p.token.text == "not") {
		operator := "in"
		if p.token.text == "not" {
			p.next()
			if p.token.kind != tokenIdent || p.token.text != "in" {
				return nil, fmt.Errorf("expected \"in\" after \"not\"")
			}
			operator = "not_in"
		}
		p.next()
		values, err := p.parseList(field)
		if err != nil {
			return nil, err
		}
		return exprComparison{node: node, key: key, op: operator, values: values}, nil
	}

	if p.token.kind != tokenOperator {
		return nil, fmt.Errorf("expected an operator after %s.%s", node, key)
	}
	operator := p.token.text
	switch operator {
	case "==", "!=":
	case ">", ">=", "<", "<=":
		if field.Type != "number" {
			return nil, fmt.Errorf(
				"operator %s requires a number field; %s.%s is %s",
				operator, node, key, field.Type,
			)
		}
	case "=":
		return nil, fmt.Errorf("use == for comparison")
	default:
		return nil, fmt.Errorf("unknown operator %q", operator)
	}
	p.next()
	value, err := p.parseLiteral(field)
	if err != nil {
		return nil, err
	}
	return exprComparison{node: node, key: key, op: operator, value: value}, nil
}

func (p *exprParser) parseList(field OutputField) ([]any, error) {
	if p.token.kind != tokenOperator || p.token.text != "[" {
		return nil, fmt.Errorf("operator in requires a list like [\"a\", \"b\"]")
	}
	p.next()
	values := make([]any, 0, 2)
	for {
		value, err := p.parseLiteral(field)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		if p.token.kind == tokenOperator && p.token.text == "," {
			p.next()
			continue
		}
		break
	}
	if p.token.kind != tokenOperator || p.token.text != "]" {
		return nil, fmt.Errorf("expected closing bracket in list")
	}
	p.next()
	return values, nil
}

// parseLiteral reads one literal and checks it against the field declaration,
// so a condition comparing an enum against a value the enum can never hold is
// a save-time error instead of a branch that silently never fires.
func (p *exprParser) parseLiteral(field OutputField) (any, error) {
	switch p.token.kind {
	case tokenString:
		value := p.token.text
		if field.Type == "enum" && !slices.Contains(field.Values, value) {
			return nil, fmt.Errorf(
				"%q is not among enum values %s", value, strings.Join(field.Values, ", "),
			)
		}
		if field.Type == "bool" || field.Type == "number" {
			return nil, fmt.Errorf("field %q compares against %s, not a string", field.Key, field.Type)
		}
		p.next()
		return value, nil
	case tokenNumber:
		if field.Type != "number" {
			return nil, fmt.Errorf("field %q compares against %s, not a number", field.Key, field.Type)
		}
		value, err := strconv.ParseFloat(p.token.text, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", p.token.text)
		}
		p.next()
		return value, nil
	case tokenIdent:
		if p.token.text == "true" || p.token.text == "false" {
			if field.Type != "bool" {
				return nil, fmt.Errorf("field %q compares against %s, not a bool", field.Key, field.Type)
			}
			value := p.token.text == "true"
			p.next()
			return value, nil
		}
	}
	return nil, fmt.Errorf("expected a literal, got %q", p.token.text)
}

// resolveReference binds an identifier to a declared node output. An
// unqualified key is legal only when exactly one node in scope declares it;
// once two nodes share the key the template author must qualify, which is
// what keeps a later edit from silently rebinding an existing condition.
func (p *exprParser) resolveReference(text string) (node, key string, field OutputField, err error) {
	// Split on the first dot only: the owner is one segment, and everything
	// after it is the field's own name. Host issue properties use a second
	// segment (issue.property.severity) so a custom property can never be
	// mistaken for a built-in issue field.
	if before, after, qualified := strings.Cut(text, "."); qualified {
		declaration, ok := p.scope.FieldTypes[text]
		if !ok {
			return "", "", OutputField{}, fmt.Errorf(
				"unknown field %q: nothing upstream declares it", text,
			)
		}
		return before, after, declaration, nil
	}
	owners := p.scope.Fields[text]
	switch len(owners) {
	case 0:
		return "", "", OutputField{}, fmt.Errorf(
			"unknown field %q: no upstream node declares it", text,
		)
	case 1:
		declaration := p.scope.FieldTypes[owners[0]+"."+text]
		return owners[0], text, declaration, nil
	default:
		return "", "", OutputField{}, fmt.Errorf(
			"field %q is declared by %s; qualify it as node.field",
			text, strings.Join(owners, " and "),
		)
	}
}
