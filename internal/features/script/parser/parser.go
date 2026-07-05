// Package parser converts a flat token stream into a rAthena-script AST.
//
// The parser is a hand-written recursive-descent implementation that
// mirrors the structure of rAthena's `src/map/script.cpp` parser
// (script.cpp:866-2230). It deliberately avoids goyacc — the dialect
// has LALR(1) conflicts (see .agents/handoff/p3.0-scout-findings.md §2.3)
// that a generator would force into awkward workarounds.
//
// Public entry points:
//
//	parser.NewWithSource(src, tokens).ParseFile()    // header + body
//	parser.New(tokens).ParseBody()                   // body only
//	parser.New(tokens).ParseFunctionScript()         // `function script F { ... }`
//
// All entry points return an AST plus a positioned error on failure.
package parser

import (
	"fmt"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

// maxDepth bounds recursive nesting to keep the goroutine stack safe.
// Real scripts rarely exceed depth ~20; 100 is generous.
const maxDepth = 100

// keywordFunction is the literal `function` keyword that introduces a
// function declaration (both floating `function Name { ... }` and the
// tab-separated `function script Name { ... }` form). Used by the
// parser to recognize function-shaped statements and headers.
const keywordFunction = "function"

// Parser converts a flat token slice into a script AST.
//
// A single Parser value is single-use: New constructs one per parse
// call. The parser is not safe for concurrent use.
//
// The optional `src` field holds the original source bytes; it is only
// consulted by ParseFile to extract the tab-separated NPC header
// (which the lexer strips). Callers that only need ParseBody /
// ParseFunctionScript can use New and leave src nil.
type Parser struct {
	tokens []script.Token
	src    []byte
	pos    int
	depth  int
}

// New constructs a Parser for the given token stream. The slice must
// end with a TokenEOF sentinel (the lexer guarantees this). The
// resulting parser can be used for ParseBody and ParseFunctionScript
// but ParseFile will return an error because the tab-separated header
// is not recoverable from the token stream alone.
func New(tokens []script.Token) *Parser {
	return &Parser{tokens: tokens}
}

// NewWithSource constructs a Parser with both the token stream and the
// original source. ParseFile uses src to recover the tab-separated NPC
// header that the lexer would otherwise strip.
func NewWithSource(src []byte, tokens []script.Token) *Parser {
	return &Parser{tokens: tokens, src: src}
}

// ParseFile parses a complete NPC definition: an optional tab-delimited
// header followed by a `{ ... }` body. Returns *script.File with the
// header populated for NPC spawn lines, or nil header for floating
// `function script` definitions.
//
// Example inputs:
//
//	dewata,202,184,6\tscript\tKafra::kaf\t4_F_KAFRA1,{ ... }
//	function\tscript\tF_Foo\t{ ... }
//	-\tscript\tF_Bar\t{ ... }
func (p *Parser) ParseFile() (*script.File, error) {
	if p.src == nil {
		return nil, fmt.Errorf("parse error: ParseFile requires source bytes; use NewWithSource")
	}

	headerLine, hasHeader := detectHeaderLine(p.src)

	var header *script.NPCHeader
	if hasHeader {
		h, err := parseHeaderLine(headerLine)
		if err != nil {
			return nil, err
		}
		header = h
	}

	if err := p.advanceToBodyOpen(); err != nil {
		return nil, err
	}

	body, err := p.ParseBody()
	if err != nil {
		return nil, err
	}

	return script.NewFile(header, body), nil
}

// ParseBody parses the statements inside a `{ ... }` block. The opening
// `{` must be the current token; the closing `}` is consumed but not
// returned.
func (p *Parser) ParseBody() ([]script.Stmt, error) {
	p.skipNewlines()
	if err := p.expectDelim("{"); err != nil {
		return nil, err
	}
	stmts, err := p.parseBlockUntil("}")
	if err != nil {
		return nil, err
	}
	if err := p.expectDelim("}"); err != nil {
		return nil, err
	}
	return stmts, nil
}

// ParseStmts parses a flat sequence of statements until EOF. It is
// useful for unit tests that want to exercise statement-level parsing
// without wrapping every snippet in braces. The caller is responsible
// for ensuring the input is a complete statement sequence; trailing
// tokens after the final semicolon are ignored.
func (p *Parser) ParseStmts() ([]script.Stmt, error) {
	var stmts []script.Stmt
	for {
		p.skipNewlines()
		t := p.peek()
		if t.Kind == script.TokenEOF {
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

// ParseFunctionScript parses a `function script Name { ... }` definition.
// Returns *script.FuncDecl with the body and function name.
func (p *Parser) ParseFunctionScript() (*script.FuncDecl, error) {
	start := p.peek()
	if start.Kind != script.TokenKeyword || start.Value != keywordFunction {
		return nil, p.errorf(start, "expected `function` keyword, got %s", describeTok(start))
	}
	p.advance()

	scriptTok := p.peek()
	if scriptTok.Kind != script.TokenIdent || scriptTok.Value != "script" {
		return nil, p.errorf(scriptTok, "expected `script` after `function`, got %s", describeTok(scriptTok))
	}
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

// advanceToBodyOpen scans tokens and positions p.pos at the first `{`.
func (p *Parser) advanceToBodyOpen() error {
	for {
		t := p.peek()
		if t.Kind == script.TokenEOF {
			return p.errorf(t, "expected `{` to start script body")
		}
		if t.Kind == script.TokenDelim && t.Value == "{" {
			return nil
		}
		p.advance()
	}
}

// ----- Cursor helpers -----

func (p *Parser) peek() script.Token {
	if p.pos >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.pos]
}

func (p *Parser) peekAt(n int) script.Token {
	i := p.pos + n
	if i >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[i]
}

func (p *Parser) advance() script.Token {
	t := p.peek()
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

func (p *Parser) matchDelim(v string) bool {
	if p.peek().Kind == script.TokenDelim && p.peek().Value == v {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) matchKeyword(v string) bool {
	if p.peek().Kind == script.TokenKeyword && p.peek().Value == v {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) expectDelim(v string) error {
	t := p.peek()
	if t.Kind == script.TokenDelim && t.Value == v {
		p.advance()
		return nil
	}
	return p.errorf(t, "expected `%s`, got %s", v, describeTok(t))
}

func (p *Parser) expectKeyword(v string) error {
	t := p.peek()
	if t.Kind == script.TokenKeyword && t.Value == v {
		p.advance()
		return nil
	}
	return p.errorf(t, "expected keyword `%s`, got %s", v, describeTok(t))
}

func (p *Parser) expectIdent() (script.Token, error) {
	t := p.peek()
	if t.Kind != script.TokenIdent {
		return t, p.errorf(t, "expected identifier, got %s", describeTok(t))
	}
	p.advance()
	return t, nil
}

func (p *Parser) skipNewlines() {
	for p.peek().Kind == script.TokenNewline {
		p.advance()
	}
}

func (p *Parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return p.errorf(p.peek(), "maximum nesting depth (%d) exceeded", maxDepth)
	}
	return nil
}

func (p *Parser) exit() {
	p.depth--
}

func (p *Parser) errorf(t script.Token, format string, args ...any) error {
	return fmt.Errorf("parse error at %s: %s", t.Pos, fmt.Sprintf(format, args...))
}

func describeTok(t script.Token) string {
	switch t.Kind {
	case script.TokenEOF:
		return "end of file"
	case script.TokenIdent, script.TokenKeyword:
		return fmt.Sprintf("`%s`", t.Value)
	case script.TokenInt, script.TokenFloat, script.TokenString, script.TokenOperator,
		script.TokenAssign, script.TokenDelim, script.TokenComment, script.TokenNewline:
		return fmt.Sprintf("%s(%q)", t.Kind, t.Value)
	default:
		return fmt.Sprintf("%s(%q)", t.Kind, t.Value)
	}
}
