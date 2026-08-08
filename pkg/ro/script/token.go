package script

import "fmt"

// Position represents a source code position (line + column).
//
// Line is 1-indexed; Column is 1-indexed (number of characters since the
// most recent line start). Column counts bytes, not runes — rAthena script
// sources are virtually always ASCII/EUC-KR which is single-byte.
type Position struct {
	Line   int
	Column int
}

// String formats the position as "line:col" for error messages.
func (p Position) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// TokenKind classifies a lexical token.
//
// The set covers what a hand-written recursive-descent parser for the
// rAthena dialect needs to see. Comments and (most) whitespace are
// silently dropped by the lexer; newlines are tracked implicitly via the
// Position carried on each Token rather than being emitted as separate
// tokens, except inside switch/case dispatch where they are significant
// only as statement separators (`;`).
type TokenKind int

const (
	// TokenEOF is the sentinel emitted after the last byte of input.
	TokenEOF TokenKind = iota
	// TokenIdent is an identifier: variable, function, label name,
	// builtin name. The first character may be a scope prefix
	// (`.`, `@`, `#`, `$`, `'`) and the last character may be `$`
	// to mark a string-typed variable. Examples: `mes`, `.@var`,
	// `callfunc`, `L_Open`, `#account$`.
	TokenIdent
	// TokenInt is a decimal or hexadecimal integer literal.
	// Unary minus is NOT part of the literal — it is emitted as a
	// separate TokenOperator applied as a prefix unary.
	TokenInt
	// TokenFloat is a floating-point literal. Rare in rAthena scripts
	// but defined for forward compatibility with the rAthena parser.
	TokenFloat
	// TokenString is a `"..."` string literal. The Value field holds
	// the *decoded* string with escape sequences resolved; the raw
	// text is not preserved.
	TokenString
	// TokenKeyword is a reserved word: if, else, while, for, do,
	// switch, case, default, break, continue, function, return, goto,
	// callsub, callfunc, end, close, close2, next, mes, menu, select,
	// input, set, setarray, etc.
	TokenKeyword
	// TokenOperator is a non-assignment operator: +, -, *, /, %,
	// ==, !=, <, >, <=, >=, &&, ||, !, &, |, ^, ~, <<, >>, ++, --.
	TokenOperator
	// TokenAssign is an assignment operator: =, +=, -=, *=, /=, %=,
	// &=, |=, ^=, <<=, >>=.
	TokenAssign
	// TokenDelim is a delimiter: ; , : ( ) { } [ ] ?.
	TokenDelim
	// TokenComment is a `// line` or `/* block */` comment. Comments
	// are typically suppressed by the scanner but the kind exists
	// for diagnostics/debugging output.
	TokenComment
	// TokenNewline is emitted when the scanner observes a `\n` that
	// is significant for downstream parsing (e.g. to count lines). By
	// default the lexer skips newlines silently and the Position on
	// subsequent tokens carries the line number.
	TokenNewline
)

// Token-kind debug mnemonics. Extracted as named constants so the
// goconst linter does not flag them as duplicated string literals.
const (
	tokEOFS      = "EOF"
	tokIdent     = "IDENT"
	tokInt       = "INT"
	tokFloat     = "FLOAT"
	tokString    = "STRING"
	tokKeyword   = "KEYWORD"
	tokOperator  = "OP"
	tokAssign    = "ASSIGN"
	tokDelim     = "DELIM"
	tokComment   = "COMMENT"
	tokNewline   = "NEWLINE"
	opcodeStrEOF = "EOF"
)

// String returns a human-readable name for the token kind.
func (k TokenKind) String() string {
	switch k {
	case TokenEOF:
		return tokEOFS
	case TokenIdent:
		return tokIdent
	case TokenInt:
		return tokInt
	case TokenFloat:
		return tokFloat
	case TokenString:
		return tokString
	case TokenKeyword:
		return tokKeyword
	case TokenOperator:
		return tokOperator
	case TokenAssign:
		return tokAssign
	case TokenDelim:
		return tokDelim
	case TokenComment:
		return tokComment
	case TokenNewline:
		return tokNewline
	default:
		return fmt.Sprintf("TokenKind(%d)", int(k))
	}
}

// Token is a single lexical unit produced by the scanner.
//
// Pos is the source location of the first character of Value. For
// TokenEOF the position is the line/column immediately after the last
// consumed byte.
type Token struct {
	Kind  TokenKind
	Value string
	Pos   Position
}

// String returns a debug-friendly representation like "IDENT(mes) @ 1:1".
// Useful for test failure messages and parser error contexts.
func (t Token) String() string {
	return fmt.Sprintf("%s(%q) @ %s", t.Kind, t.Value, t.Pos)
}
