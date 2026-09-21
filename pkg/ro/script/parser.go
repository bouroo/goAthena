package script

import (
	"fmt"
	"strconv"
)

// Parse tokenizes src with Lex and parses every top-level NPC block it
// contains, returning one *File per block. A block is a placed NPC
// (`map,x,y,dir script Name sprite,{ ... }`), a floating script
// (`- script Name -1,{ ... }`), or a function script
// (`function script Name { ... }`). Whitespace and comments are already
// gone by the time the parser sees the token stream.
func Parse(src []byte) ([]*File, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	files, err := parseFiles(p)
	if err != nil {
		return nil, err
	}
	return files, nil
}

// parser is a cursor over a token slice. The token stream ends in a single
// TokenEOF that every read path treats as a hard stop.
type parser struct {
	toks []Token
	pos  int
}

// --- cursor helpers ---

func (p *parser) peek() Token {
	if p.pos >= len(p.toks) {
		return Token{Kind: TokenEOF}
	}
	return p.toks[p.pos]
}

// peekN returns the token n positions ahead without consuming (0 == peek).
// Lookahead past EOF returns EOF.
func (p *parser) peekN(n int) Token {
	i := p.pos + n
	if i >= len(p.toks) {
		return Token{Kind: TokenEOF}
	}
	return p.toks[i]
}

func (p *parser) next() Token {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

// at reports whether the current token matches the given kind and (if non-
// empty) value.
func (p *parser) at(k TokenKind, value string) bool {
	t := p.peek()
	if t.Kind != k {
		return false
	}
	return value == "" || t.Value == value
}

// atKeyword is at specialized to TokenKeyword, since the control words are
// the only keywords and the parser dispatches on their value.
func (p *parser) atKeyword(value string) bool {
	t := p.peek()
	return t.Kind == TokenKeyword && t.Value == value
}

// accept consumes the current token iff it matches; returns whether it did.
func (p *parser) accept(k TokenKind, value string) bool {
	if p.at(k, value) {
		p.next()
		return true
	}
	return false
}

// expect consumes the current token iff it matches, else returns a positioned
// error naming what was expected vs found.
func (p *parser) expect(k TokenKind, value string) (Token, error) {
	t := p.peek()
	if t.Kind != k || (value != "" && t.Value != value) {
		want := value
		if want == "" {
			want = k.String()
		}
		return t, p.errorf(t.Pos, "expected %s, got %s", want, t.String())
	}
	p.next()
	return t, nil
}

func (p *parser) errorf(pos Position, format string, args ...any) error {
	return &ParseError{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// ParseError carries the source position of a syntax failure. Its format
// mirrors LexError so callers render parse and lex errors uniformly.
type ParseError struct {
	Pos Position
	Msg string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Msg)
}

// --- top level ---

func parseFiles(p *parser) ([]*File, error) {
	var files []*File
	for p.peek().Kind != TokenEOF {
		// A stray `;` between blocks (common in hand-edited files) is tolerated.
		if p.accept(TokenDelim, ";") {
			continue
		}
		f, err := parseFile(p)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// parseFile parses one top-level NPC block. It distinguishes the three
// header shapes (placed / floating / function) by lookahead, then parses the
// `{ ... }` body they all share. For `shop` NPCs the body is empty and the
// item table follows directly after the header's trailing comma.
func parseFile(p *parser) (*File, error) {
	if p.atKeyword("function") {
		return parseFunctionScript(p)
	}
	hdr, err := parseHeader(p)
	if err != nil {
		return nil, err
	}
	// `shop` NPCs have no dialog body — the header's trailing comma is followed
	// by the item table instead of `{`. Route to the item parser and return.
	if hdr.Type == "shop" {
		items, err := parseShopItemTable(p)
		if err != nil {
			return nil, err
		}
		return NewFile(hdr, nil, items...), nil
	}
	body, err := parseBlock(p)
	if err != nil {
		return nil, err
	}
	return NewFile(hdr, body), nil
}

// parseShopItemTable parses the comma-separated item table that follows a
// `shop` NPC header. The header's trailing comma is the first token here.
// Each entry is `itemID : price` where price < 0 means "use item_db default
// buy price" (resolved at load time, not here).
func parseShopItemTable(p *parser) ([]ShopItem, error) {
	var items []ShopItem
	// Consume the item-table separator (header's trailing comma).
	if _, err := p.expect(TokenDelim, ","); err != nil {
		return nil, err
	}
	for {
		itemID, err := p.expectIntVal()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenDelim, ":"); err != nil {
			return nil, err
		}
		price, err := p.parseSignedInt()
		if err != nil {
			return nil, err
		}
		id, price32 := int32(itemID), int32(price) //nolint:gosec // item ids and zeny prices fit int32
		items = append(items, ShopItem{ItemID: id, Price: price32})
		if !p.accept(TokenDelim, ",") {
			break
		}
	}
	return items, nil
}

// parseFunctionScript parses `function script Name { ... }`. Function scripts
// have no map position; they are addressed by name for callfunc. They are
// represented as a File whose header carries only the name and Type="func".
func parseFunctionScript(p *parser) (*File, error) {
	start := p.peek().Pos
	p.next() // consume `function`
	if _, err := p.expectIdent("script"); err != nil {
		return nil, err
	}
	name, err := p.expectIdentVal()
	if err != nil {
		return nil, err
	}
	body, err := parseBlock(p)
	if err != nil {
		return nil, err
	}
	hdr := NewNPCHeader("", 0, 0, 0, name, "", 0, 0, 0, "func", start)
	return NewFile(hdr, body), nil
}

// parseHeader parses a placed or floating NPC header line up to (but not
// including) the opening brace.
//
//	placed:   mapname , x , y , facing  script  Name  sprite [ , tx , ty ] ,
//	floating: -  script  Name  -1 ,
//
// The trailing comma before the body brace is rAthena's separator; both
// forms share it. The sprite for a floating NPC is -1 (rendered by the lexer
// as `-` then `1`, since unary minus is not folded into the literal).
func parseHeader(p *parser) (*NPCHeader, error) {
	start := p.peek().Pos
	if p.at(TokenOperator, "-") {
		return parseFloatingHeader(p, start)
	}
	return parsePlacedHeader(p, start)
}

func parseFloatingHeader(p *parser, start Position) (*NPCHeader, error) {
	p.next() // consume leading `-`
	if _, err := p.expectIdent("script"); err != nil {
		return nil, err
	}
	name, err := p.expectIdentVal()
	if err != nil {
		return nil, err
	}
	sprite, err := p.parseSignedInt()
	if err != nil {
		return nil, err
	}
	tx, ty, err := p.parseOptionalTriggers()
	if err != nil {
		return nil, err
	}
	// When triggers were parsed, parseOptionalTriggers already consumed the
	// trailing comma; skip expectCommaBeforeBody or it will fail on `{`.
	if tx == 0 && ty == 0 {
		if err := p.expectCommaBeforeBody(); err != nil {
			return nil, err
		}
	}
	return NewNPCHeader("", 0, 0, 0, name, "", sprite, tx, ty, "float", start), nil
}

func parsePlacedHeader(p *parser, start Position) (*NPCHeader, error) {
	mapName, err := p.expectMapName()
	if err != nil {
		return nil, err
	}
	x, err := p.expectDelimInt(",")
	if err != nil {
		return nil, err
	}
	y, err := p.expectDelimInt(",")
	if err != nil {
		return nil, err
	}
	facing, err := p.expectDelimInt(",")
	if err != nil {
		return nil, err
	}
	// The type word is `script`, `warp`, `shop`, etc. It selects the NPC's
	// runtime shape; the parser carries it verbatim so downstream stages
	// (warp extraction, shop tables) can dispatch without re-lexing.
	typ, err := p.expectIdentVal()
	if err != nil {
		return nil, err
	}
	name, err := p.expectIdentVal()
	if err != nil {
		return nil, err
	}
	sprite, err := p.parseSignedInt()
	if err != nil {
		return nil, err
	}
	tx, ty, err := p.parseOptionalTriggers()
	if err != nil {
		return nil, err
	}
	// For `shop` NPCs the header comma is the item-table separator, not a
	// header/body delimiter. Do NOT consume it here; parseFile handles it by
	// routing to parseShopItemTable instead of parseBlock.
	if typ == "shop" {
		return NewNPCHeader(mapName, x, y, facing, name, "", sprite, tx, ty, typ, start), nil
	}
	// When triggers were parsed, parseOptionalTriggers already consumed the
	// trailing comma; skip expectCommaBeforeBody or it will fail on `{`.
	if tx == 0 && ty == 0 {
		if err := p.expectCommaBeforeBody(); err != nil {
			return nil, err
		}
	}
	return NewNPCHeader(mapName, x, y, facing, name, "", sprite, tx, ty, typ, start), nil
}

// parseSignedInt reads an integer that may carry a leading unary `-`. The
// lexer emits `-1` as two tokens (operator `-`, int `1`); this folds them
// back into the negative value rAthena means.
func (p *parser) parseSignedInt() (int, error) {
	neg := p.accept(TokenOperator, "-")
	t, err := p.expect(TokenInt, "")
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(t.Value)
	if err != nil {
		return 0, p.errorf(t.Pos, "bad integer %q: %v", t.Value, err)
	}
	if neg {
		v = -v
	}
	return v, nil
}

// parseOptionalTriggers reads `, tx , ty` if a warp trigger pair follows the
// sprite. Returns (0,0,nil) for plain NPCs that have no trigger region.
func (p *parser) parseOptionalTriggers() (int, int, error) {
	if !p.at(TokenDelim, ",") {
		return 0, 0, nil
	}
	// Distinguish warp's `, tx , ty , EOF` from a script's optional-trigger
	// region `, tx , ty , {`. Guards check BEFORE consuming the comma:
	//   warp:       peekN(1)=INT, peekN(2)=,, peekN(3)=INT, peekN(4)=, → EOF (past end)
	//   script opt: peekN(1)=INT, peekN(2)=,, peekN(3)=INT, peekN(4)={ (DELIM)
	//   bare:       peekN(1)={ (not INT) → handled by first guard
	//   shop item:  peekN(1)=INT, peekN(2)=: (not ,) → handled by warp-path check
	// After the guards pass, peekN(4)==EOF means this is warp. Otherwise skip.
	if p.peekN(1).Kind != TokenInt {
		return 0, 0, nil
	}
	// `, itemID :` is shop syntax — the colon at peekN(2) is not a comma.
	if p.peekN(2).Kind == TokenDelim && p.peekN(2).Value == ":" {
		return 0, 0, nil
	}
	p.next() // consume leading `,`
	tx, err := p.expectIntVal()
	if err != nil {
		return 0, 0, err
	}
	if _, err := p.expect(TokenDelim, ","); err != nil {
		return 0, 0, err
	}
	ty, err := p.expectIntVal()
	if err != nil {
		return 0, 0, err
	}
	// Split: warp ends at `, tx , ty` (peekN(1) is EOF). Script continues to `{`.
	if p.peekN(1).Kind == TokenEOF {
		return tx, ty, nil
	}
	// Script: consume the trailing comma and return triggers.
	if _, err := p.expect(TokenDelim, ","); err != nil {
		return 0, 0, err
	}
	return tx, ty, nil
}

// expectCommaBeforeBody consumes the trailing `,` that separates the header
// from the body brace. rAthena always emits it (`909,{`, `-1,{`).
func (p *parser) expectCommaBeforeBody() error {
	if _, err := p.expect(TokenDelim, ","); err != nil {
		return err
	}
	return nil
}

// --- header token shortcuts ---

// expectMapName reads a map name: identifiers (and digit runs) joined by
// dashes (new_1-1, prt_fild08, gef_fild13 — the corpus is full of them). The
// lexer emits `new_1` `-` `1` as IDENT OP INT; this folds the run back into
// one name. It stops at the first comma.
func (p *parser) expectMapName() (string, error) {
	name, err := p.expectIdentVal()
	if err != nil {
		return "", err
	}
	for p.at(TokenOperator, "-") {
		p.next()
		switch t := p.peek(); t.Kind {
		case TokenIdent:
			name += "-" + t.Value
			p.next()
		case TokenInt:
			name += "-" + t.Value
			p.next()
		default:
			return "", p.errorf(p.peek().Pos, "map name: expected IDENT or INT after -, got %s", t.Kind)
		}
	}
	return name, nil
}

func (p *parser) expectIdent(value string) (Token, error) {
	return p.expect(TokenIdent, value)
}

func (p *parser) expectIdentVal() (string, error) {
	t, err := p.expect(TokenIdent, "")
	if err != nil {
		return "", err
	}
	return t.Value, nil
}

func (p *parser) expectIntVal() (int, error) {
	t, err := p.expect(TokenInt, "")
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(t.Value)
	if err != nil {
		return 0, p.errorf(t.Pos, "bad integer %q: %v", t.Value, err)
	}
	return v, nil
}

// expectDelimInt consumes a delimiter (e.g. `,`) then an integer, the
// repeated `sep value` shape the header fields use.
func (p *parser) expectDelimInt(sep string) (int, error) {
	if _, err := p.expect(TokenDelim, sep); err != nil {
		return 0, err
	}
	return p.expectIntVal()
}

// --- body & statements ---

// parseBlock expects and consumes a `{`, parses statements until `}`, and
// consumes the closing brace.
func parseBlock(p *parser) ([]Stmt, error) {
	if _, err := p.expect(TokenDelim, "{"); err != nil {
		return nil, err
	}
	var stmts []Stmt
	for !p.at(TokenDelim, "}") && p.peek().Kind != TokenEOF {
		s, err := parseStmt(p)
		if err != nil {
			return nil, err
		}
		if s != nil {
			stmts = append(stmts, s)
		}
	}
	if _, err := p.expect(TokenDelim, "}"); err != nil {
		return nil, err
	}
	return stmts, nil
}

// parseStmtOrBlock parses a single statement (the if/while/for single-stmt
// body) or, if a `{` opens, a brace-delimited block.
func parseStmtOrBlock(p *parser) ([]Stmt, error) {
	if p.at(TokenDelim, "{") {
		return parseBlock(p)
	}
	s, err := parseStmt(p)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	return []Stmt{s}, nil
}

// parseStmt dispatches on the current token to the matching statement form.
// A leading stray `;` (empty statement) returns nil and is dropped by callers.
func parseStmt(p *parser) (Stmt, error) {
	t := p.peek()
	if t.Kind == TokenDelim && t.Value == ";" {
		p.next()
		return nil, nil
	}
	switch { //nolint:staticcheck // QF1002: if-else chain clearer than tagged switch on t.Kind
	case t.Kind == TokenKeyword:
		return parseKeywordStmt(p)
	case t.Kind == TokenIdent:
		// Label declaration: `Name :` at statement position. A ternary `:` only
		// appears inside expressions, so an ident directly followed by `:` here
		// is unambiguously a label.
		if p.peekN(1).Kind == TokenDelim && p.peekN(1).Value == ":" {
			return parseLabel(p)
		}
		return parseIdentStmt(p)
	default:
		return nil, p.errorf(t.Pos, "unexpected %s at statement start", t.String())
	}
}

// parseKeywordStmt handles the 13 reserved control words.
func parseKeywordStmt(p *parser) (Stmt, error) {
	switch p.peek().Value {
	case "if":
		return parseIf(p)
	case "while":
		return parseWhile(p)
	case "for":
		return parseFor(p)
	case "do":
		return parseDoWhile(p)
	case "switch":
		return parseSwitch(p)
	case "break":
		pos := p.next().Pos
		_, _ = p.expect(TokenDelim, ";")
		return NewBreakStmt(pos), nil
	case "continue":
		pos := p.next().Pos
		_, _ = p.expect(TokenDelim, ";")
		return NewContinueStmt(pos), nil
	case "return":
		return parseReturn(p)
	case "goto":
		pos := p.next().Pos
		label, err := p.expectIdentVal()
		if err != nil {
			return nil, err
		}
		_, _ = p.expect(TokenDelim, ";")
		return NewGotoStmt(label, pos), nil
	case "callsub":
		pos := p.next().Pos
		label, err := p.expectIdentVal()
		if err != nil {
			return nil, err
		}
		args, err := parseCallArgs(p)
		if err != nil {
			return nil, err
		}
		_, _ = p.expect(TokenDelim, ";")
		return NewCallSubStmt(label, args, pos), nil
	default: // function/case/default handled elsewhere
		return nil, p.errorf(p.peek().Pos, "unexpected keyword %q", p.peek().Value)
	}
}

func parseLabel(p *parser) (Stmt, error) {
	name, err := p.expectIdentVal()
	if err != nil {
		return nil, err
	}
	pos := p.peek().Pos
	if _, err := p.expect(TokenDelim, ":"); err != nil {
		return nil, err
	}
	return NewLabelDecl(name, pos), nil
}

// parseIf handles `if (cond) stmt`, `if (cond) { block }`, and the optional
// `else` continuation. The condition is always parenthesized in rAthena.
func parseIf(p *parser) (Stmt, error) {
	pos := p.next().Pos // consume `if`
	if _, err := p.expect(TokenDelim, "("); err != nil {
		return nil, err
	}
	cond, err := parseExpr(p)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenDelim, ")"); err != nil {
		return nil, err
	}
	thenStmts, err := parseStmtOrBlock(p)
	if err != nil {
		return nil, err
	}
	var elseStmts []Stmt
	if p.atKeyword("else") { //nolint:nestif
		p.next()
		// `else if` chains: parse the else as a single if-statement body.
		if p.atKeyword("if") {
			elif, err := parseIf(p)
			if err != nil {
				return nil, err
			}
			elseStmts = []Stmt{elif}
		} else {
			elseStmts, err = parseStmtOrBlock(p)
			if err != nil {
				return nil, err
			}
		}
	}
	return NewIfStmt(cond, thenStmts, elseStmts, pos), nil
}

func parseWhile(p *parser) (Stmt, error) {
	pos := p.next().Pos
	if _, err := p.expect(TokenDelim, "("); err != nil {
		return nil, err
	}
	cond, err := parseExpr(p)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenDelim, ")"); err != nil {
		return nil, err
	}
	body, err := parseStmtOrBlock(p)
	if err != nil {
		return nil, err
	}
	return NewWhileStmt(cond, body, pos), nil
}

func parseFor(p *parser) (Stmt, error) {
	pos := p.next().Pos
	if _, err := p.expect(TokenDelim, "("); err != nil {
		return nil, err
	}
	// Init clause: one statement whose own trailing `;` is consumed by parseStmt.
	var init []Stmt
	if !p.at(TokenDelim, ";") {
		s, err := parseStmt(p)
		if err != nil {
			return nil, err
		}
		if s != nil {
			init = append(init, s)
		}
	} else {
		p.next() // empty init: consume bare `;`
	}
	// Condition: an expression followed by `;`.
	var cond Expr
	if !p.at(TokenDelim, ";") {
		c, err := parseExpr(p)
		if err != nil {
			return nil, err
		}
		cond = c
	}
	if _, err := p.expect(TokenDelim, ";"); err != nil {
		return nil, err
	}
	// Post clause: one statement. parseStmt's `;` expect is non-fatal, so it
	// stops at the `)` when the trailing `;` is omitted (the usual form).
	var post []Stmt
	if !p.at(TokenDelim, ")") {
		s, err := parseStmt(p)
		if err != nil {
			return nil, err
		}
		if s != nil {
			post = append(post, s)
		}
	}
	if _, err := p.expect(TokenDelim, ")"); err != nil {
		return nil, err
	}
	body, err := parseStmtOrBlock(p)
	if err != nil {
		return nil, err
	}
	return NewForStmt(init, cond, post, body, pos), nil
}

func parseDoWhile(p *parser) (Stmt, error) {
	pos := p.next().Pos
	body, err := parseStmtOrBlock(p)
	if err != nil {
		return nil, err
	}
	if _, err := p.expectIdent("while"); err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenDelim, "("); err != nil {
		return nil, err
	}
	cond, err := parseExpr(p)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenDelim, ")"); err != nil {
		return nil, err
	}
	_, _ = p.expect(TokenDelim, ";")
	return NewDoWhileStmt(body, cond, pos), nil
}

func parseSwitch(p *parser) (Stmt, error) {
	pos := p.next().Pos
	if _, err := p.expect(TokenDelim, "("); err != nil {
		return nil, err
	}
	val, err := parseExpr(p)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenDelim, ")"); err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenDelim, "{"); err != nil {
		return nil, err
	}
	var cases []SwitchCase
	for !p.at(TokenDelim, "}") && p.peek().Kind != TokenEOF {
		c, err := parseSwitchCase(p)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	if _, err := p.expect(TokenDelim, "}"); err != nil {
		return nil, err
	}
	return NewSwitchStmt(val, cases, pos), nil
}

func parseSwitchCase(p *parser) (SwitchCase, error) {
	pos := p.peek().Pos
	var values []Expr
	if p.atKeyword("default") {
		p.next()
		_, _ = p.expect(TokenDelim, ":")
	} else {
		if _, err := p.expectIdent("case"); err != nil {
			return SwitchCase{}, err
		}
		v, err := parseExpr(p)
		if err != nil {
			return SwitchCase{}, err
		}
		values = append(values, v)
		// `case a : b :` falls through; rAthena uses `:` to list values.
		for p.at(TokenDelim, ":") {
			p.next()
			v2, err := parseExpr(p)
			if err != nil {
				return SwitchCase{}, err
			}
			values = append(values, v2)
		}
	}
	var body []Stmt
	for !p.atKeyword("case") && !p.atKeyword("default") && !p.at(TokenDelim, "}") && p.peek().Kind != TokenEOF {
		s, err := parseStmt(p)
		if err != nil {
			return SwitchCase{}, err
		}
		if s != nil {
			body = append(body, s)
		}
	}
	return NewSwitchCase(values, body, pos), nil
}

func parseReturn(p *parser) (Stmt, error) {
	pos := p.next().Pos
	var val Expr
	if !p.at(TokenDelim, ";") {
		v, err := parseExpr(p)
		if err != nil {
			return nil, err
		}
		val = v
	}
	_, _ = p.expect(TokenDelim, ";")
	return NewReturnStmt(val, pos), nil
}

// parseIdentStmt handles the non-keyword statement forms whose head is an
// identifier: assignment, builtin/command call, and the `set` builtin.
//
//	ident = expr ;           | ident += expr ;        (assignment)
//	ident [ idx ] = expr ;                            (array assignment)
//	ident arg , arg , ... ;  | ident ( args ) ;       (command / function call)
func parseIdentStmt(p *parser) (Stmt, error) {
	pos := p.peek().Pos

	// Assignment (incl. compound and array-target). The lookahead must peek
	// past a possible index suffix `ident[...]` to reach the `=`.
	if lhs, ok, err := p.tryAssignTarget(); err != nil {
		return nil, err
	} else if ok {
		op := p.next().Value // the `=`/`+=`/... TokenAssign
		rhs, err := parseExpr(p)
		if err != nil {
			return nil, err
		}
		_, _ = p.expect(TokenDelim, ";")
		return NewAssignStmt(lhs, op, rhs, pos), nil
	}

	name, err := p.expectIdentVal()
	if err != nil {
		return nil, err
	}
	args, err := parseCallArgs(p)
	if err != nil {
		return nil, err
	}
	_, _ = p.expect(TokenDelim, ";")
	return NewCallStmt(name, args, pos), nil
}

// tryAssignTarget peeks ahead to decide whether this ident statement is an
// assignment. It returns the assignment target (an IdentExpr or IndexExpr)
// and consumes it plus the trailing TokenAssign if so; otherwise it rewinds
// the cursor and returns ok=false so the caller parses it as a call.
func (p *parser) tryAssignTarget() (Expr, bool, error) {
	mark := p.pos
	t := p.peek()
	if t.Kind != TokenIdent {
		return nil, false, nil
	}
	// `set` is the explicit assignment builtin, never an assign target itself.
	if t.Value == "set" { //nolint:goconst
		return nil, false, nil
	}
	var target Expr = NewIdentExpr(t.Value, t.Pos)
	p.next()
	// Array element target: `name [ idx ]`.
	if p.at(TokenDelim, "[") {
		p.next()
		idx, err := parseExpr(p)
		if err != nil {
			p.pos = mark
			return nil, false, nil //nolint:nilerr // backtracking: parse failure means "not an assignment"; rewind and retry as call
		}
		if _, err := p.expect(TokenDelim, "]"); err != nil {
			p.pos = mark
			return nil, false, nil //nolint:nilerr // backtracking: parse failure means "not an assignment"; rewind and retry as call
		}
		target = NewIndexExpr(target, idx, t.Pos)
	}
	if p.peek().Kind == TokenAssign {
		return target, true, nil
	}
	// Not an assignment: rewind so parseIdentStmt re-reads it as a call.
	p.pos = mark
	return nil, false, nil
}

// parseCallArgs reads a builtin's argument list in either rAthena form:
//
//	name a, b, c ;   (command form — bare, comma-separated, `;`-terminated)
//	name(a, b, c);   (function form — parenthesized)
//
// Both are lowered to the same CallStmt/CallExpr. The function form is
// detected by a `(` immediately following the name.
func parseCallArgs(p *parser) ([]Expr, error) {
	if p.at(TokenDelim, "(") {
		p.next()
		var args []Expr
		for !p.at(TokenDelim, ")") {
			a, err := parseExpr(p)
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if !p.accept(TokenDelim, ",") {
				break
			}
		}
		if _, err := p.expect(TokenDelim, ")"); err != nil {
			return nil, err
		}
		return args, nil
	}
	// Command form: bare comma-separated until `;`.
	var args []Expr
	for !p.at(TokenDelim, ";") && p.peek().Kind != TokenEOF {
		a, err := parseExpr(p)
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if !p.accept(TokenDelim, ",") {
			break
		}
	}
	return args, nil
}

// --- expressions (precedence climbing) ---
//
// Each level delegates to the next-tighter one and folds matching operators.
// Precedence mirrors rAthena/C: || < && < equality < relational < additive <
// multiplicative < bitwise < shift < unary < postfix < primary.

const (
	precNone = iota
	precAssign
	precTernary
	precOr
	precAnd
	precEquality
	precRelational
	precAdditive
	precMultiplicative
	precShift
	precUnary
	precPostfix
)

// binPrec maps each infix operator to its precedence level. The operator token
// text is its own AST operator string, so opInfo returns the key unchanged.
//
//nolint:goconst // operator tokens read clearest as literals in this table
var binPrec = map[string]int{
	"||": precOr,
	"&&": precAnd,
	"==": precEquality,
	"!=": precEquality,
	"<":  precRelational,
	">":  precRelational,
	"<=": precRelational,
	">=": precRelational,
	"+":  precAdditive,
	"-":  precAdditive,
	"*":  precMultiplicative,
	"/":  precMultiplicative,
	"%":  precMultiplicative,
	"&":  precShift,
	"|":  precShift,
	"^":  precShift,
	"<<": precShift,
	">>": precShift,
}

func opInfo(value string) (op string, prec int, binary bool) {
	if prec, ok := binPrec[value]; ok {
		return value, prec, true
	}
	return "", precNone, false
}

func parseExpr(p *parser) (Expr, error) {
	return parseBinary(p, precNone)
}

// parseBinary is the precedence-climbing core. It parses a sub-expression at
// the given minimum precedence, then folds any infix operator at >= that
// precedence into a BinExpr. Ternary and assignment are folded here too so a
// single recursive entry point serves the whole expression grammar.
func parseBinary(p *parser, minPrec int) (Expr, error) {
	left, err := parseUnary(p)
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		// Ternary binds tighter than assignment, looser than ||.
		if t.Kind == TokenDelim && t.Value == "?" && precTernary >= minPrec {
			p.next()
			thenE, err := parseBinary(p, precNone)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TokenDelim, ":"); err != nil {
				return nil, err
			}
			elseE, err := parseBinary(p, precTernary)
			if err != nil {
				return nil, err
			}
			left = NewTernaryExpr(left, thenE, elseE, t.Pos)
			continue
		}
		if t.Kind != TokenOperator {
			break
		}
		op, prec, _ := opInfo(t.Value)
		if prec == precNone || prec < minPrec {
			break
		}
		p.next()
		right, err := parseBinary(p, prec+1)
		if err != nil {
			return nil, err
		}
		left = NewBinExpr(op, left, right, t.Pos)
	}
	return left, nil
}

// parseUnary folds a prefix `!`, `-`, or `~`, then descends to postfix.
func parseUnary(p *parser) (Expr, error) {
	t := p.peek()
	if t.Kind == TokenOperator && (t.Value == "!" || t.Value == "-" || t.Value == "~") {
		p.next()
		operand, err := parseUnary(p)
		if err != nil {
			return nil, err
		}
		return NewUnaryExpr(t.Value, operand, t.Pos), nil
	}
	return parsePostfix(p, nil)
}

// parsePostfix folds trailing call `(args)` and index `[idx]` suffixes onto a
// primary. A call suffix turns the primary into a CallExpr (the callee is the
// identifier name; rAthena has no first-class function values at this layer).
// prev is a partially-built expression (nil on entry) threaded so the suffix
// loop composes left-to-right.
func parsePostfix(p *parser, prev Expr) (Expr, error) {
	if prev == nil {
		e, err := parsePrimary(p)
		if err != nil {
			return nil, err
		}
		prev = e
	}
	for {
		t := p.peek()
		switch {
		case t.Kind == TokenDelim && t.Value == "(":
			// Function-form call: callee must be an identifier.
			id, ok := prev.(*IdentExpr)
			if !ok {
				return prev, nil
			}
			p.next()
			args, err := parseExprListUntil(p, ")")
			if err != nil {
				return nil, err
			}
			prev = NewCallExpr(id.Name, args, t.Pos)
		case t.Kind == TokenDelim && t.Value == "[":
			p.next()
			idx, err := parseExpr(p)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TokenDelim, "]"); err != nil {
				return nil, err
			}
			prev = NewIndexExpr(prev, idx, t.Pos)
		default:
			return prev, nil
		}
	}
}

// parseExprListUntil parses comma-separated expressions until the closing
// delimiter, consuming it.
func parseExprListUntil(p *parser, closeDelim string) ([]Expr, error) {
	var args []Expr
	for !p.at(TokenDelim, closeDelim) {
		a, err := parseExpr(p)
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if !p.accept(TokenDelim, ",") {
			break
		}
	}
	if _, err := p.expect(TokenDelim, closeDelim); err != nil {
		return nil, err
	}
	return args, nil
}

func parsePrimary(p *parser) (Expr, error) {
	t := p.peek()
	switch {
	case t.Kind == TokenInt:
		p.next()
		v, err := strconv.ParseInt(t.Value, 0, 64)
		if err != nil {
			return nil, p.errorf(t.Pos, "bad integer %q: %v", t.Value, err)
		}
		return NewIntLit(v, t.Pos), nil
	case t.Kind == TokenFloat:
		p.next()
		v, err := strconv.ParseFloat(t.Value, 64)
		if err != nil {
			return nil, p.errorf(t.Pos, "bad float %q: %v", t.Value, err)
		}
		return NewFloatLit(v, t.Pos), nil
	case t.Kind == TokenString:
		p.next()
		return NewStrLit(t.Value, t.Pos), nil
	case t.Kind == TokenIdent || t.Kind == TokenKeyword:
		// In expression position every name (keyword or not) is a variable/
		// builtin reference; e.g. `Zeny`, `gettimetick(2)`, `.@Price`.
		p.next()
		return NewIdentExpr(t.Value, t.Pos), nil
	case t.Kind == TokenDelim && t.Value == "(":
		p.next()
		inner, err := parseExpr(p)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenDelim, ")"); err != nil {
			return nil, err
		}
		return NewParenExpr(inner, t.Pos), nil
	default:
		return nil, p.errorf(t.Pos, "unexpected %s in expression", t.String())
	}
}
