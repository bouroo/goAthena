package parser

import (
	"strconv"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

// Operator precedence table (Pratt parser). Lower numbers bind
// looser (lower precedence); higher numbers bind tighter.
//
// Mirrors rAthena's `parse_variable` expression precedence
// (script.cpp:1290-1340).
const (
	precAssign = iota
	precTernary
	precOr
	precAnd
	precBOr
	precBXor
	precBAnd
	precEq
	precCmp
	precShift
	precAdd
	precMul
	precUnary
)

// Operator strings reused by the parser. Declared as constants so the
// goconst linter does not flag repeated string literals.
const (
	opInc = "++"
	opDec = "--"
)

// infixBindingPower returns (leftBP, rightBP) for a binary operator
// at the current position. Returns ok=false when the current token is
// not a binary operator.
func (p *Parser) infixBindingPower() (leftBP, rightBP int, ok bool) {
	t := p.peek()
	switch t.Kind {
	case script.TokenOperator:
		return opBindingPower(t.Value)
	case script.TokenDelim:
		if t.Value == "?" {
			return precTernary, precTernary + 1, true
		}
	case script.TokenAssign:
		// Assignment inside for-clauses (`for (.@i = 0; ...)`) must be
		// parsed as a binary expression because parseForClause uses
		// parseExpr. At top-level statement positions parseAssign handles
		// `lhs = rhs` directly, so the only callers that see `=` here are
		// for-init/post and expression statements like `a == b;` (which
		// uses `==`, never `=`). Returning a binding power lets those
		// clauses work while keeping the resulting BinExpr available for
		// lowering in parseForClause.
		return precAssign, precAssign + 1, true
	case script.TokenEOF, script.TokenIdent, script.TokenInt, script.TokenFloat,
		script.TokenString, script.TokenKeyword, script.TokenComment, script.TokenNewline:
		return 0, 0, false
	}
	return 0, 0, false
}

// opBindingPower returns the precedence for a binary operator string.
func opBindingPower(op string) (leftBP, rightBP int, ok bool) {
	switch op {
	case "*", "/", "%":
		return precMul, precMul + 1, true
	case "+", "-":
		return precAdd, precAdd + 1, true
	case "<<", ">>":
		return precShift, precShift + 1, true
	case "<", ">", "<=", ">=":
		return precCmp, precCmp + 1, true
	case "==", "!=":
		return precEq, precEq + 1, true
	case "&":
		return precBAnd, precBAnd + 1, true
	case "^":
		return precBXor, precBXor + 1, true
	case "|":
		return precBOr, precBOr + 1, true
	case "&&":
		return precAnd, precAnd + 1, true
	case "||":
		return precOr, precOr + 1, true
	}
	return 0, 0, false
}

func (p *Parser) parseExpr() (script.Expr, error) {
	return p.parseBinary(0)
}

func (p *Parser) hasInfixNext() bool {
	_, _, ok := p.infixBindingPower()
	return ok
}

func (p *Parser) parseBinary(minPrec int) (script.Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.exit()

	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	return p.parseBinaryRest(left, minPrec)
}

func (p *Parser) parseBinaryRest(left script.Expr, minPrec int) (script.Expr, error) {
	for {
		leftBP, rightBP, ok := p.infixBindingPower()
		if !ok || leftBP < minPrec {
			break
		}

		opTok := p.advance()
		op := opTok.Value

		right, err := p.parseBinary(rightBP)
		if err != nil {
			return nil, err
		}

		if op == "?" {
			thenExpr := right
			if err := p.expectDelim(":"); err != nil {
				return nil, err
			}
			elseExpr, err := p.parseBinary(precTernary + 1)
			if err != nil {
				return nil, err
			}
			left = script.NewTernaryExpr(left, thenExpr, elseExpr, opTok.Pos)
			continue
		}

		left = script.NewBinExpr(op, left, right, opTok.Pos)
	}

	return left, nil
}

func (p *Parser) parseUnary() (script.Expr, error) {
	t := p.peek()
	if t.Kind == script.TokenOperator {
		switch t.Value {
		case "!", "-", "~", "+":
			op := t.Value
			pos := t.Pos
			p.advance()
			operand, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			if op == "+" {
				return operand, nil
			}
			return script.NewUnaryExpr(op, operand, pos), nil
		case opInc, opDec:
			op := t.Value
			pos := t.Pos
			p.advance()
			operand, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			return script.NewUnaryExpr(op, operand, pos), nil
		}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() (script.Expr, error) {
	atom, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return p.parsePostfixFrom(atom)
}

// parsePostfixFrom parses the postfix suffixes (call, index, increment,
// decrement) starting from an already-parsed primary expression.
func (p *Parser) parsePostfixFrom(atom script.Expr) (script.Expr, error) {
	for {
		t := p.peek()
		if t.Kind == script.TokenDelim && t.Value == "(" {
			ident, ok := atom.(*script.IdentExpr)
			if !ok {
				return nil, p.errorf(t, "cannot call non-identifier expression")
			}
			args, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			atom = script.NewCallExpr(ident.Name, args, ident.Pos())
			continue
		}
		if t.Kind == script.TokenDelim && t.Value == "[" {
			open := p.advance()
			idx, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectDelim("]"); err != nil {
				return nil, err
			}
			atom = script.NewIndexExpr(atom, idx, open.Pos)
			continue
		}
		if t.Kind == script.TokenOperator && (t.Value == opInc || t.Value == opDec) {
			op := t.Value
			pos := t.Pos
			p.advance()
			atom = script.NewUnaryExpr("post"+op, atom, pos)
			continue
		}
		break
	}
	return atom, nil
}

func (p *Parser) parsePrimary() (script.Expr, error) {
	t := p.peek()

	switch t.Kind {
	case script.TokenInt:
		p.advance()
		v, err := strconv.ParseInt(t.Value, 0, 64)
		if err != nil {
			return nil, p.errorf(t, "invalid integer literal %q: %v", t.Value, err)
		}
		return script.NewIntLit(v, t.Pos), nil

	case script.TokenFloat:
		p.advance()
		v, err := strconv.ParseFloat(t.Value, 64)
		if err != nil {
			return nil, p.errorf(t, "invalid float literal %q: %v", t.Value, err)
		}
		return script.NewFloatLit(v, t.Pos), nil

	case script.TokenString:
		p.advance()
		return script.NewStrLit(t.Value, t.Pos), nil

	case script.TokenIdent:
		p.advance()
		return script.NewIdentExpr(t.Value, t.Pos), nil

	case script.TokenDelim:
		if t.Value == "(" {
			p.advance()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectDelim(")"); err != nil {
				return nil, err
			}
			return script.NewParenExpr(e, t.Pos), nil
		}
		return nil, p.errorf(t, "expected expression, got %s", describeTok(t))

	case script.TokenEOF, script.TokenKeyword, script.TokenOperator, script.TokenAssign,
		script.TokenComment, script.TokenNewline:
		return nil, p.errorf(t, "expected expression, got %s", describeTok(t))
	}

	return nil, p.errorf(t, "expected expression, got %s", describeTok(t))
}

func (p *Parser) parseArgList() ([]script.Expr, error) {
	if err := p.expectDelim("("); err != nil {
		return nil, err
	}
	if p.matchDelim(")") {
		return nil, nil
	}
	var args []script.Expr
	for {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if !p.matchDelim(",") && !p.matchDelim(":") {
			break
		}
		if p.peek().Kind == script.TokenDelim && p.peek().Value == ")" {
			break
		}
	}
	if err := p.expectDelim(")"); err != nil {
		return nil, err
	}
	return args, nil
}
