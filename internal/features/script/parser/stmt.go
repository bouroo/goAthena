package parser

import (
	"fmt"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

// parseBlockUntil parses statements until it hits a delimiter with the
// given value (typically `}`) or EOF.
func (p *Parser) parseBlockUntil(stopDelim string) ([]script.Stmt, error) {
	var stmts []script.Stmt
	for {
		p.skipNewlines()
		t := p.peek()
		if t.Kind == script.TokenEOF {
			return nil, p.errorf(t, "unexpected end of file (expected `%s`)", stopDelim)
		}
		if t.Kind == script.TokenDelim && t.Value == stopDelim {
			return stmts, nil
		}
		s, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		if s != nil {
			stmts = append(stmts, s)
		}
		p.skipNewlines()
	}
}

func (p *Parser) parseStatement() (script.Stmt, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.exit()

	p.skipNewlines()
	t := p.peek()

	if t.Kind == script.TokenDelim && t.Value == ";" {
		p.advance()
		return nil, nil
	}

	if t.Kind == script.TokenKeyword {
		return p.parseKeywordStmt(t.Value)
	}

	if t.Kind == script.TokenIdent {
		if next := p.peekAt(1); next.Kind == script.TokenDelim && next.Value == ":" {
			return p.parseLabelDecl()
		}
	}

	if t.Kind == script.TokenDelim && t.Value == "(" {
		return p.parseParenExprStmt()
	}

	if t.Kind == script.TokenDelim && t.Value == "{" {
		return p.parseBlockStmt()
	}

	if t.Kind == script.TokenIdent && p.isAssignLike() {
		return p.parseAssign()
	}

	return p.parseCallOrExpr()
}

// parseKeywordStmt dispatches on a keyword token and returns the parsed
// statement. The keyword value must be one of the reserved words.
func (p *Parser) parseKeywordStmt(keyword string) (script.Stmt, error) {
	switch keyword {
	case "if":
		return p.parseIf()
	case "while":
		return p.parseWhile()
	case "do":
		return p.parseDoWhile()
	case "for":
		return p.parseFor()
	case "switch":
		return p.parseSwitch()
	case "break":
		return p.parseBreak()
	case "continue":
		return p.parseContinue()
	case "return":
		return p.parseReturn()
	case keywordFunction:
		return p.parseFunction()
	case "goto":
		return p.parseGoto()
	case "callsub":
		return p.parseCallSub()
	}
	return nil, p.errorf(p.peek(), "unexpected keyword `%s`", keyword)
}

// parseParenExprStmt parses a parenthesized expression used as a
// statement, e.g. `(a + b) * c;`.
func (p *Parser) parseParenExprStmt() (script.Stmt, error) {
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	return script.NewCallStmt("", []script.Expr{expr}, expr.Pos()), nil
}

// parseBlockStmt parses `{ stmts... }` and returns a BlockStmt.
func (p *Parser) parseBlockStmt() (script.Stmt, error) {
	open := p.advance()
	stmts, err := p.parseBlockUntil("}")
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim("}"); err != nil {
		return nil, err
	}
	pos := open.Pos
	if len(stmts) > 0 {
		pos = stmts[0].Pos()
	}
	return script.NewBlockStmt(stmts, pos), nil
}

// isAssignLike reports whether the current identifier token is followed
// by an assignment operator (or array-index assignment), so it should be
// parsed as an assignment statement.
func (p *Parser) isAssignLike() bool {
	if isAssignTok(p.peekAt(1)) {
		return true
	}
	if p.peekAt(1).Kind == script.TokenDelim && p.peekAt(1).Value == "[" {
		return p.isArrayAssign()
	}
	return false
}

func isAssignTok(t script.Token) bool {
	return t.Kind == script.TokenAssign
}

func (p *Parser) isArrayAssign() bool {
	if p.peekAt(1).Kind != script.TokenDelim || p.peekAt(1).Value != "[" {
		return false
	}
	depth := 0
	i := 1
	for {
		tk := p.peekAt(i)
		if tk.Kind == script.TokenEOF {
			return false
		}
		if tk.Kind == script.TokenDelim {
			switch tk.Value {
			case "[":
				depth++
			case "]":
				depth--
				if depth == 0 {
					next := p.peekAt(i + 1)
					return next.Kind == script.TokenAssign
				}
			}
		}
		i++
	}
}

func (p *Parser) parseAssign() (script.Stmt, error) {
	startPos := p.peek().Pos

	lhs, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	// If parseExpr consumed the assignment operator (because we made `=`
	// an infix operator for for-clauses), unwrap the BinExpr to produce
	// the equivalent AssignStmt.
	if bin, ok := lhs.(*script.BinExpr); ok && isAssignOp(bin.Op) {
		if err := p.expectDelim(";"); err != nil {
			return nil, err
		}
		return script.NewAssignStmt(bin.Lhs, bin.Op, bin.Rhs, startPos), nil
	}

	opTok := p.peek()
	if opTok.Kind != script.TokenAssign {
		return nil, p.errorf(opTok, "expected assignment operator, got %s", describeTok(opTok))
	}
	op := opTok.Value
	p.advance()

	rhs, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}

	return script.NewAssignStmt(lhs, op, rhs, startPos), nil
}

func (p *Parser) parseCallOrExpr() (script.Stmt, error) {
	start := p.peek()
	if start.Kind != script.TokenIdent {
		return nil, p.errorf(start, "expected statement, got %s", describeTok(start))
	}
	name := start.Value
	namePos := start.Pos
	p.advance()

	// Built-in calls with space-separated arguments can start with a
	// unary minus (`percentheal -99,0;`, `mes -1,2;`). In that case the
	// next token is an operator; if we treat it as an infix expression
	// statement we build an illegal binary expression and consume the
	// first argument. Detect this by attempting to parse a call argument
	// list first when the next token is a unary-minus/plus operator.
	//
	// We must not do this for `--`/`++` (postfix inc/dec) or for binary
	// expression statements such as `a == b;` and `.@i++;`, which are
	// handled by tryExprStmt. A minus as the first argument of a builtin
	// is only valid when it is followed by a literal or parenthesized
	// expression; in that case the whole call should be parsed as a call
	// statement.
	if p.peek().Kind == script.TokenOperator && isUnaryMinusLike(p.peek().Value) && isUnaryArgStart(p.peekAt(1)) {
		args, err := p.parseSpaceDelimitedArgs(nil)
		if err != nil {
			return nil, err
		}
		if err := p.expectDelim(";"); err != nil {
			return nil, err
		}
		return script.NewCallStmt(name, args, namePos), nil
	}

	exprStmt, ok, err := p.tryExprStmt(name, namePos)
	if err != nil {
		return nil, err
	}
	if ok {
		return exprStmt, nil
	}

	args, err := p.parseCallArgs(name)
	if err != nil {
		return nil, err
	}

	// menu/select options are parsed as normal space-delimited calls.
	if name == "menu" || name == "select" {
		return p.parseMenuOrSelectStmt(name, namePos, args)
	}

	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}

	return script.NewCallStmt(name, args, namePos), nil
}

// parseMenuOrSelectStmt lowers `menu "A",L_A,"B",L_B;` or
// `select("A","B");` into the dedicated MenuStmt/SelectStmt AST nodes.
// It consumes the trailing semicolon.
func (p *Parser) parseMenuOrSelectStmt(name string, namePos script.Position, args []script.Expr) (script.Stmt, error) {
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	opts, err := exprsToMenuOptions(args)
	if err != nil {
		return nil, fmt.Errorf("parse %s options at %s: %w", name, namePos, err)
	}
	if name == "menu" {
		return script.NewMenuStmt(opts, namePos), nil
	}
	return script.NewSelectStmt(opts, namePos), nil
}

// exprsToMenuOptions pairs consecutive expressions as (prompt, label).
// Labels may be a bare identifier expression or a literal `-` (used by
// menu to mean "no jump"). The result stores the label as a string.
func exprsToMenuOptions(args []script.Expr) ([]script.MenuOption, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("expected even number of menu/select arguments, got %d", len(args))
	}
	var opts []script.MenuOption
	for i := 0; i < len(args); i += 2 {
		prompt := args[i]
		labelExpr := args[i+1]
		var label string
		switch l := labelExpr.(type) {
		case *script.IdentExpr:
			label = l.Name
		case *script.IntLit:
			if l.Value == 0 {
				label = "-"
				break
			}
			return nil, fmt.Errorf("menu label must be identifier or `-`, got integer %d", l.Value)
		default:
			return nil, fmt.Errorf("menu label must be identifier or `-`, got %T", labelExpr)
		}
		opts = append(opts, script.NewMenuOption(prompt, label, labelExpr.Pos()))
	}
	return opts, nil
}

// tryExprStmt attempts to parse a bare expression statement that starts
// with an identifier followed by an operator. It returns ok=true when
// the identifier was consumed as part of an expression statement.
func (p *Parser) tryExprStmt(name string, namePos script.Position) (script.Stmt, bool, error) {
	if !p.hasInfixNext() {
		if p.peek().Kind == script.TokenOperator && (p.peek().Value == opInc || p.peek().Value == opDec) {
			expr, err := p.parsePostfixExprStmt(name, namePos)
			return expr, true, err
		}
		return nil, false, nil
	}

	// Assignment (`=`, `+=`) is not an expression statement; it was
	// already handled by parseAssign above. Any other infix operator
	// following the identifier means this is a bare expression
	// statement such as `a == b;`.
	if p.peek().Kind == script.TokenAssign {
		return nil, false, nil
	}
	lhs := script.NewIdentExpr(name, namePos)
	expr, err := p.parseBinaryRest(lhs, 0)
	if err != nil {
		return nil, true, err
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, true, err
	}
	return script.NewCallStmt("", []script.Expr{expr}, namePos), true, nil
}

// parsePostfixExprStmt parses `ident++;` and `ident--;` as expression
// statements and consumes the trailing semicolon.
func (p *Parser) parsePostfixExprStmt(name string, namePos script.Position) (script.Stmt, error) {
	atom := script.NewIdentExpr(name, namePos)
	expr, err := p.parsePostfixFrom(atom)
	if err != nil {
		return nil, err
	}
	expr, err = p.parseBinaryRest(expr, 0)
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	return script.NewCallStmt("", []script.Expr{expr}, namePos), nil
}

func (p *Parser) parseCallArgs(name string) ([]script.Expr, error) {
	if !p.matchDelim("(") {
		return p.parseSpaceDelimitedArgs(nil)
	}
	return p.parseParenArgs()
}

// parseParenArgs parses a parenthesized argument list that has already
// consumed the opening `(`.
func (p *Parser) parseParenArgs() ([]script.Expr, error) {
	var args []script.Expr
	if p.matchDelim(")") {
		return args, nil
	}
	for {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if !p.matchDelim(",") {
			break
		}
	}
	if err := p.expectDelim(")"); err != nil {
		return nil, err
	}
	return args, nil
}

// parseSpaceDelimitedArgs parses the remaining call arguments for
// rAthena's space-separated call syntax (`mes "a";`, `set @x,1;`).
// It stops at statement or block boundaries, EOF, or when no comma
// follows an argument.
func (p *Parser) parseSpaceDelimitedArgs(args []script.Expr) ([]script.Expr, error) {
	for {
		if p.isArgTerminator() {
			return args, nil
		}
		// rAthena uses a bare `-` as a placeholder / no-op argument,
		// most commonly in menu/select labels: `menu "A",-,"B",L_B;`.
		// Since `-` is otherwise a unary prefix operator, parseExpr would
		// consume it and then fail on the following comma. Detect the
		// standalone case and emit a zero literal instead.
		if t := p.peek(); t.Kind == script.TokenOperator && t.Value == "-" {
			next := p.peekAt(1)
			if next.Kind == script.TokenDelim && (next.Value == "," || next.Value == ";" || next.Value == "}" || next.Value == ":") {
				p.advance()
				args = append(args, script.NewIntLit(0, t.Pos))
				if !p.matchDelim(",") {
					return args, nil
				}
				continue
			}
		}
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if !p.matchDelim(",") {
			return args, nil
		}
	}
}

// isArgTerminator reports whether the current token ends a space-
// delimited argument list.
func (p *Parser) isArgTerminator() bool {
	t := p.peek()
	if t.Kind == script.TokenEOF {
		return true
	}
	return t.Kind == script.TokenDelim && (t.Value == ";" || t.Value == "{" || t.Value == "}")
}

func (p *Parser) parseIf() (script.Stmt, error) {
	start := p.advance()
	if err := p.expectDelim("("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(")"); err != nil {
		return nil, err
	}
	then, err := p.parseBranchStmt()
	if err != nil {
		return nil, err
	}
	var els []script.Stmt
	if p.matchKeyword("else") {
		els, err = p.parseBranchStmt()
		if err != nil {
			return nil, err
		}
	}
	return script.NewIfStmt(cond, then, els, start.Pos), nil
}

func (p *Parser) parseBranchStmt() ([]script.Stmt, error) {
	p.skipNewlines()
	if p.matchDelim("{") {
		stmts, err := p.parseBlockUntil("}")
		if err != nil {
			return nil, err
		}
		if err := p.expectDelim("}"); err != nil {
			return nil, err
		}
		return stmts, nil
	}
	s, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	return []script.Stmt{s}, nil
}

func (p *Parser) parseWhile() (script.Stmt, error) {
	start := p.advance()
	if err := p.expectDelim("("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(")"); err != nil {
		return nil, err
	}
	body, err := p.parseBranchStmt()
	if err != nil {
		return nil, err
	}
	return script.NewWhileStmt(cond, body, start.Pos), nil
}

func (p *Parser) parseDoWhile() (script.Stmt, error) {
	start := p.advance()
	body, err := p.parseBranchStmt()
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("while"); err != nil {
		return nil, err
	}
	if err := p.expectDelim("("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(")"); err != nil {
		return nil, err
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	return script.NewDoWhileStmt(body, cond, start.Pos), nil
}

func (p *Parser) parseFor() (script.Stmt, error) {
	start := p.advance()
	if err := p.expectDelim("("); err != nil {
		return nil, err
	}
	init, err := p.parseForClause()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	var cond script.Expr
	if p.peek().Kind != script.TokenDelim || p.peek().Value != ";" {
		cond, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	post, err := p.parseForClause()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(")"); err != nil {
		return nil, err
	}
	body, err := p.parseBranchStmt()
	if err != nil {
		return nil, err
	}
	return script.NewForStmt(init, cond, post, body, start.Pos), nil
}

func (p *Parser) parseForClause() ([]script.Stmt, error) {
	var stmts []script.Stmt
	if p.peek().Kind == script.TokenDelim && (p.peek().Value == ";" || p.peek().Value == ")") {
		return stmts, nil
	}
	for {
		stmt, err := p.parseForClauseItem()
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			stmts = append(stmts, stmt)
		}

		if !p.matchDelim(",") {
			return stmts, nil
		}
		if p.peek().Kind == script.TokenDelim && (p.peek().Value == ";" || p.peek().Value == ")") {
			return stmts, nil
		}
	}
}

// parseForClauseItem parses a single comma-separated item of a for-clause
// (init or post slot). For-clauses are expression-or-assignment positions
// rather than full statements, so the trailing semicolon is not consumed
// here. rAthena also allows `set`/`setarray` builtins to appear in for-
// clauses as space-separated calls; we detect that case explicitly.
func (p *Parser) parseForClauseItem() (script.Stmt, error) {
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if expr == nil {
		return nil, p.errorf(p.peek(), "expected expression in for clause")
	}

	// `set`/`setarray`-style builtin in a for clause: parseExpr consumed
	// only the name, so collect its space-delimited arguments and emit a
	// CallStmt.
	if ident, ok := expr.(*script.IdentExpr); ok &&
		p.peek().Kind != script.TokenDelim && p.peek().Kind != script.TokenEOF {
		args, argErr := p.parseSpaceDelimitedArgs(nil)
		if argErr != nil {
			return nil, argErr
		}
		return script.NewCallStmt(ident.Name, args, ident.Pos()), nil
	}

	// Assignment operator (e.g. `a = b`, `a += b`): lower to AssignStmt.
	if bin, ok := expr.(*script.BinExpr); ok && isAssignOp(bin.Op) {
		return script.NewAssignStmt(bin.Lhs, bin.Op, bin.Rhs, bin.Pos()), nil
	}

	// Bare expression (e.g. `i++` or `foo()`).
	return exprStmtOrCall(expr), nil
}

func isAssignOp(op string) bool {
	switch op {
	case "=", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=":
		return true
	}
	return false
}

// isUnaryMinusLike reports whether op is a unary prefix operator that can
// introduce a signed literal argument.
func isUnaryMinusLike(op string) bool {
	switch op {
	case "-", "+", "!", "~":
		return true
	}
	return false
}

// isUnaryArgStart reports whether the token following a unary prefix operator
// can begin a primary expression (literal, identifier, parenthesized expr).
func isUnaryArgStart(t script.Token) bool {
	switch t.Kind {
	case script.TokenInt, script.TokenFloat, script.TokenString, script.TokenIdent:
		return true
	case script.TokenDelim:
		return t.Value == "("
	case script.TokenEOF, script.TokenKeyword, script.TokenOperator,
		script.TokenAssign, script.TokenComment, script.TokenNewline:
		return false
	}
	return false
}

func exprStmtOrCall(e script.Expr) script.Stmt {
	if call, ok := e.(*script.CallExpr); ok {
		return script.NewCallStmt(call.Name, call.Args, call.Pos())
	}
	return script.NewCallStmt("", []script.Expr{e}, e.Pos())
}

func (p *Parser) parseSwitch() (script.Stmt, error) {
	start := p.advance()
	if err := p.expectDelim("("); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(")"); err != nil {
		return nil, err
	}
	if err := p.expectDelim("{"); err != nil {
		return nil, err
	}
	cases, err := p.parseSwitchBody()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim("}"); err != nil {
		return nil, err
	}
	return script.NewSwitchStmt(val, cases, start.Pos), nil
}

func (p *Parser) parseSwitchBody() ([]script.SwitchCase, error) {
	var cases []script.SwitchCase
	for {
		p.skipNewlines()
		t := p.peek()
		if t.Kind == script.TokenDelim && t.Value == "}" {
			return cases, nil
		}
		if t.Kind == script.TokenEOF {
			return nil, p.errorf(t, "unexpected end of file in switch body")
		}

		var values []script.Expr
		casePos := t.Pos
		if t.Kind != script.TokenKeyword {
			return nil, p.errorf(t, "expected `case` or `default`, got %s", describeTok(t))
		}
		switch t.Value {
		case "case":
			p.advance()
			for {
				v, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				values = append(values, v)
				if !p.matchDelim(",") {
					break
				}
			}
		case "default":
			p.advance()
		default:
			return nil, p.errorf(t, "expected `case` or `default`, got %s", describeTok(t))
		}
		if err := p.expectDelim(":"); err != nil {
			return nil, err
		}
		body, err := p.parseCaseBody()
		if err != nil {
			return nil, err
		}
		cases = append(cases, script.NewSwitchCase(values, body, casePos))
	}
}

func (p *Parser) parseCaseBody() ([]script.Stmt, error) {
	var stmts []script.Stmt
	for {
		p.skipNewlines()
		t := p.peek()
		if t.Kind == script.TokenEOF {
			return nil, p.errorf(t, "unexpected end of file in switch case")
		}
		if t.Kind == script.TokenDelim && t.Value == "}" {
			return stmts, nil
		}
		if t.Kind == script.TokenKeyword && (t.Value == "case" || t.Value == "default") {
			return stmts, nil
		}
		s, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		if s != nil {
			stmts = append(stmts, s)
		}
	}
}

func (p *Parser) parseBreak() (script.Stmt, error) {
	start := p.advance()
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	return script.NewBreakStmt(start.Pos), nil
}

func (p *Parser) parseContinue() (script.Stmt, error) {
	start := p.advance()
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	return script.NewContinueStmt(start.Pos), nil
}

func (p *Parser) parseReturn() (script.Stmt, error) {
	start := p.advance()
	if p.matchDelim(";") {
		return script.NewReturnStmt(nil, start.Pos), nil
	}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	return script.NewReturnStmt(v, start.Pos), nil
}

func (p *Parser) parseFunction() (script.Stmt, error) {
	start := p.advance()
	scriptTok := p.peek()
	if scriptTok.Kind == script.TokenIdent && scriptTok.Value == "script" {
		// Named nested function declaration: function script Name { ... }
		p.advance()
		nameTok, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		body, err := p.ParseBody()
		if err != nil {
			return nil, err
		}
		return script.NewFuncDecl(nameTok.Value, body, start.Pos), nil
	}
	// Forward/backward function reference: function Name;
	nameTok, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if p.matchDelim(";") {
		return script.NewFuncRefStmt(nameTok.Value, start.Pos), nil
	}
	// Bare floating function definition inside another script body:
	// `function Name { ... }` (no `script` keyword, no semicolon).
	if err := p.expectDelim("{"); err != nil {
		return nil, err
	}
	body, err := p.parseBlockUntil("}")
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim("}"); err != nil {
		return nil, err
	}
	return script.NewFuncDecl(nameTok.Value, body, start.Pos), nil
}

func (p *Parser) parseGoto() (script.Stmt, error) {
	start := p.advance()
	lbl, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	return script.NewGotoStmt(lbl.Value, start.Pos), nil
}

func (p *Parser) parseCallSub() (script.Stmt, error) {
	start := p.advance()
	lbl, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	var args []script.Expr
	if p.matchDelim(",") {
		for {
			a, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if !p.matchDelim(",") {
				break
			}
		}
	}
	if err := p.expectDelim(";"); err != nil {
		return nil, err
	}
	return script.NewCallSubStmt(lbl.Value, args, start.Pos), nil
}

func (p *Parser) parseLabelDecl() (script.Stmt, error) {
	t := p.advance()
	p.advance()
	return script.NewLabelDecl(t.Value, t.Pos), nil
}
