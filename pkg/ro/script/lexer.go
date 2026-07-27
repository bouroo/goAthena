package script

import (
	"fmt"
	"strings"
)

// Keywords reserved by the parser. Identifiers matching one of these
// are emitted as TokenKeyword rather than TokenIdent so the parser can
// dispatch on them with a single kind check.
//
// The set covers the structural control-flow words used by rAthena's
// recursive descent (script.cpp:1608-1690). Builtin names like `mes`,
// `close`, `next`, `menu`, `select`, `set`, `setarray`, `input`,
// `getitem`, `warp`, `callfunc`, etc. are NOT keywords — they are
// ordinary identifiers resolved through the BuiltinRegistry at compile
// time. The parser dispatches on them by name, not by token kind.
var keywords = map[string]struct{}{
	"if":       {},
	"else":     {},
	"while":    {},
	"for":      {},
	"do":       {},
	"switch":   {},
	"case":     {},
	"default":  {},
	"break":    {},
	"continue": {},
	"function": {},
	"return":   {},
	"goto":     {},
	"callsub":  {},
}

// Lex tokenizes a rAthena script source buffer and returns the
// resulting token stream, terminated by TokenEOF. It returns an error
// only for unrecoverable lexing errors (unterminated string literal,
// unterminated block comment, or an unknown byte the scanner cannot
// classify). Whitespace and comments are silently skipped.
//
// The lexer is a pure function: it does not retain references to the
// input buffer after returning. Tests can Lex a buffer, capture the
// tokens, and discard the buffer.
func Lex(src []byte) ([]Token, error) {
	l := newLexer(src)
	var out []Token
	for {
		tok, err := l.nextToken()
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
		if tok.Kind == TokenEOF {
			return out, nil
		}
	}
}

// isKeyword reports whether name is a reserved word. Exposed package-
// internally so the parser can use the same set without duplicating it.
func isKeyword(name string) bool {
	_, ok := keywords[name]
	return ok
}

// lexer is a single-pass state-machine scanner over a []byte buffer.
type lexer struct {
	src  []byte
	pos  int // byte offset of next unread byte
	line int // line number of `pos` (1-indexed)
	col  int // column number of `pos` (1-indexed)
}

func newLexer(src []byte) *lexer {
	return &lexer{src: src, pos: 0, line: 1, col: 1}
}

// nextToken consumes and returns the next token. EOF is returned
// exactly once when the end of input is reached.
func (l *lexer) nextToken() (Token, error) {
	for {
		if l.pos >= len(l.src) {
			return Token{Kind: TokenEOF, Pos: Position{Line: l.line, Column: l.col}}, nil
		}
		c := l.src[l.pos]

		// Skip non-significant whitespace. Newlines are tracked via
		// the line counter and emitted as separate TokenNewline tokens
		// only when they are significant statement separators; the
		// Position carried on each subsequent token preserves the
		// accurate source location.
		if c == '\n' {
			l.line++
			l.col = 1
			l.pos++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' {
			l.pos++
			l.col++
			continue
		}
		// Comment skipping.
		if c == '/' && l.pos+1 < len(l.src) {
			next := l.src[l.pos+1]
			if next == '/' {
				l.skipLineComment()
				continue
			}
			if next == '*' {
				if err := l.skipBlockComment(); err != nil {
					return Token{}, err
				}
				continue
			}
		}

		startPos := Position{Line: l.line, Column: l.col}
		switch {
		case isIdentStart(c):
			return l.readIdent(startPos)
		case isDigit(c):
			return l.readNumber(startPos)
		case c == '"':
			return l.readString(startPos)
		default:
			return l.readOperatorOrDelim(startPos)
		}
	}
}

func (l *lexer) skipLineComment() {
	// Consume `//` and everything up to (but not including) the next
	// newline. The newline itself is left for the outer skip loop so
	// line counting stays correct.
	l.pos += 2
	l.col += 2
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
		l.col++
	}
}

func (l *lexer) skipBlockComment() error {
	// Consume `/*` and everything up to (and including) the matching
	// `*/`. Block comments do NOT nest in rAthena scripts (C-style
	// single-nesting is the historical behavior we mirror).
	startLine := l.line
	startCol := l.col
	l.pos += 2
	l.col += 2
	for l.pos < len(l.src)-1 {
		if l.src[l.pos] == '*' && l.src[l.pos+1] == '/' {
			l.pos += 2
			l.col += 2
			return nil
		}
		if l.src[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
	return &LexError{
		Pos: Position{Line: startLine, Column: startCol},
		Msg: "unterminated block comment",
	}
}

// readIdent consumes an identifier or keyword. Identifier grammar:
//
//	identStart     -> letter | '_' | scopePrefix
//	scopePrefix    -> '.' | '@' | '#' | '$' | '\''
//	identContinue  -> letter | '_' | '@' | digit
//
// Scope prefixes that are not letter-like (`.`, `#`, `$`, `'`) are
// only valid as the first character of an identifier; subsequent
// characters must be letter/underscore/`@`/digit, with an optional
// trailing `$` for string variables.
func (l *lexer) readIdent(start Position) (Token, error) {
	startOff := l.pos
	// Always advance past the start byte — important for inputs like
	// `.@var`, `$global`, `#account` whose first byte is not in the
	// identContinue set and would otherwise leave pos unchanged.
	l.advance()
	for l.pos < len(l.src) && isIdentContinue(l.src[l.pos]) {
		l.advance()
	}
	// rAthena identifiers may contain embedded scope prefixes after the
	// first byte when written consecutively, e.g. `.@x.y` is really
	// `.@x.y` in legacy scripts, but `.@var` is the common form. We keep
	// swallowing contiguous identifier-like bytes (including scope
	// prefixes) so that compound identifiers stay as one token where
	// the script author intended.
	for l.pos < len(l.src) && isIdentByte(l.src[l.pos]) {
		// But don't consume a trailing `$` here; that's handled below.
		if l.src[l.pos] == '$' {
			break
		}
		// Once we see a dot, any following ident bytes belong to the
		// same token only if they are also valid identifier starts. In
		// `.@var` the second byte `@` is a valid start, so consume it
		// and continue normally. For `.x.y` the second byte `x` is
		// valid, so this also keeps `.x.y` as one token (matching rAthena
		// historical behavior).
		l.advance()
		for l.pos < len(l.src) && isIdentContinue(l.src[l.pos]) {
			l.advance()
		}
	}
	// Optional trailing `$` for string variables.
	if l.pos < len(l.src) && l.src[l.pos] == '$' {
		l.advance()
	}
	val := string(l.src[startOff:l.pos])
	kind := TokenIdent
	if isKeyword(val) {
		kind = TokenKeyword
	}
	return Token{Kind: kind, Value: val, Pos: start}, nil
}

// readNumber consumes decimal or hexadecimal integer (or float) literal.
// rAthena accepts `0x`/`0X` prefix for hex and `.` mid-number for
// floats. The lexer does not perform range validation — the compiler
// will catch overflow.
func (l *lexer) readNumber(start Position) (Token, error) {
	startOff := l.pos
	if l.src[l.pos] == '0' && l.pos+1 < len(l.src) && isHexStart(l.src[l.pos+1]) {
		return l.readHex(startOff, start)
	}
	isFloat := l.readDecimalDigits()
	if isFloat {
		return Token{Kind: TokenFloat, Value: string(l.src[startOff:l.pos]), Pos: start}, nil
	}
	return Token{Kind: TokenInt, Value: string(l.src[startOff:l.pos]), Pos: start}, nil
}

// readHex consumes the `0x` prefix and any number of hex digits.
func (l *lexer) readHex(startOff int, start Position) (Token, error) {
	l.advance()
	l.advance()
	for l.pos < len(l.src) && isHexDigit(l.src[l.pos]) {
		l.advance()
	}
	return Token{Kind: TokenInt, Value: string(l.src[startOff:l.pos]), Pos: start}, nil
}

// readDecimalDigits consumes a run of decimal digits and, if followed
// by a `.digit` run, also the fractional part. Returns whether a
// fractional part was consumed.
func (l *lexer) readDecimalDigits() bool {
	for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
		l.advance()
	}
	if l.pos < len(l.src) && l.src[l.pos] == '.' && l.pos+1 < len(l.src) && isDigit(l.src[l.pos+1]) {
		l.advance()
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.advance()
		}
		return true
	}
	return false
}

// readString consumes a `"..."` literal with escape sequences
// (`\"`, `\\`, `\n`, `\t`, `\r`). The returned token carries the decoded
// string in Value; the raw source text is not preserved.
func (l *lexer) readString(start Position) (Token, error) {
	// Skip the opening quote.
	l.advance()
	var buf strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '"' {
			l.advance()
			return Token{Kind: TokenString, Value: buf.String(), Pos: start}, nil
		}
		if c == '\\' && l.pos+1 < len(l.src) {
			esc := l.src[l.pos+1]
			switch esc {
			case '"':
				buf.WriteByte('"')
			case '\\':
				buf.WriteByte('\\')
			case 'n':
				buf.WriteByte('\n')
			case 't':
				buf.WriteByte('\t')
			case 'r':
				buf.WriteByte('\r')
			default:
				// rAthena is permissive — pass unknown escapes through
				// verbatim (e.g. `\'`, `\xFF`) so the compiler sees the
				// original byte instead of swallowing it.
				buf.WriteByte('\\')
				buf.WriteByte(esc)
			}
			l.advance()
			l.advance()
			continue
		}
		if c == '\n' {
			// rAthena disallows unescaped newlines inside string
			// literals. Report the position of the opening quote.
			return Token{}, &LexError{
				Pos: start,
				Msg: "unterminated string literal",
			}
		}
		buf.WriteByte(c)
		l.advance()
	}
	return Token{}, &LexError{
		Pos: start,
		Msg: "unterminated string literal",
	}
}

// readOperatorOrDelim dispatches on a single byte to either an
// operator (with longest-match lookahead) or a delimiter. Returns
// TokenErr on an unrecognized byte.
func (l *lexer) readOperatorOrDelim(start Position) (Token, error) {
	c := l.src[l.pos]
	rest := ""
	if l.pos+1 < len(l.src) {
		rest = string(l.src[l.pos : l.pos+2])
	}
	rest3 := ""
	if l.pos+2 < len(l.src) {
		rest3 = string(l.src[l.pos : l.pos+3])
	}

	// 3-char assignment compound operators (e.g. <<=, >>= — none of the
	// 3-char vanilla operators exist, but in case future dialects add
	// them, longest-match wins).
	switch rest3 {
	case "<<=", ">>=":
		l.advance()
		l.advance()
		l.advance()
		return Token{Kind: TokenAssign, Value: rest3, Pos: start}, nil
	}

	// 2-char operators and assignment compounds.
	switch rest {
	case "==", "!=", "<=", ">=", "&&", "||", "<<", ">>",
		"+=", "-=", "*=", "/=", "%=",
		"++", "--":
		l.advance()
		l.advance()
		return Token{Kind: classAssignOrOperator(rest), Value: rest, Pos: start}, nil
	}

	// 1-char operators.
	switch c {
	case '+', '-', '*', '/', '%', '<', '>', '!', '&', '|', '^', '~':
		l.advance()
		return Token{Kind: TokenOperator, Value: string(c), Pos: start}, nil
	case '=':
		l.advance()
		return Token{Kind: TokenAssign, Value: "=", Pos: start}, nil
	}

	// 1-char delimiters.
	switch c {
	case ';', ',', ':', '(', ')', '{', '}', '[', ']', '?':
		l.advance()
		return Token{Kind: TokenDelim, Value: string(c), Pos: start}, nil
	}

	// Unknown byte — surface a useful error.
	return Token{}, &LexError{
		Pos: start,
		Msg: fmt.Sprintf("unexpected character %q", rune(c)),
	}
}

// classAssignOrOperator picks TokenAssign for compound assignments
// and TokenOperator for relational/logical operators that happen to be
// two characters long.
func classAssignOrOperator(s string) TokenKind {
	switch s {
	case "+=", "-=", "*=", "/=", "%=":
		return TokenAssign
	}
	return TokenOperator
}

// advance advances past one byte and updates the column counter. It
// does not update the line counter — newlines are detected and
// accounted for in the outer skip loop, not in advance, so that
// multi-byte escapes inside string literals don't desync the line
// count.
func (l *lexer) advance() {
	l.pos++
	l.col++
}

// ----- helpers -----

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isHexDigit(b byte) bool { return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') }
func isHexStart(b byte) bool { return b == 'x' || b == 'X' }

// isIdentStart returns true for characters that may begin an
// identifier: ASCII letter, underscore, or one of the rAthena scope
// prefixes. We accept the raw byte rather than decoding UTF-8 because
// rAthena script sources are virtually always ASCII; non-ASCII bytes
// surface as "unexpected character" errors rather than being silently
// miscategorized.
func isIdentStart(b byte) bool {
	switch b {
	case '_', '.', '@', '#', '$', '\'':
		return true
	}
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isIdentContinue returns true for characters that may appear after
// the first byte of an identifier: letters, underscore, digits, and
// the `@` scope prefix (the last to permit constructs like `.@x.y`
// that some scripts occasionally emit). Dots are intentionally NOT
// allowed mid-identifier; `.@var` is parsed as a single token because
// the dot starts the identifier, but `.@x.y` must tokenize as
// `.@x`, `.`, `y` and be assembled by the parser.
func isIdentContinue(b byte) bool {
	switch b {
	case '_', '@':
		return true
	}
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || isDigit(b)
}

// isIdentByte is true for characters that may appear anywhere in an
// identifier, including the first byte, so scope prefixes are valid.
func isIdentByte(b byte) bool {
	switch b {
	case '_', '.', '@', '#', '$', '\'':
		return true
	}
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || isDigit(b)
}

// LexError is returned by Lex when the source contains a tokenization
// error (unterminated string, unexpected byte, etc.). It carries the
// source position of the failure so errors can be surfaced with line
// + column context.
type LexError struct {
	Pos Position
	Msg string
}

// Error implements the error interface.
func (e *LexError) Error() string {
	return fmt.Sprintf("lex error at %s: %s", e.Pos, e.Msg)
}
