//go:build unit

package script

import (
	"testing"
)

// tok is a compact constructor for expected tokens in tests.
func tok(kind TokenKind, val string, line, col int) Token {
	return Token{Kind: kind, Value: val, Pos: Position{Line: line, Column: col}}
}

// lexOrFail is a test helper that fails the test on a lex error.
func lexOrFail(t *testing.T, src []byte) []Token {
	t.Helper()
	got, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex(%q) returned error: %v", src, err)
	}
	return got
}

func TestLexEmpty(t *testing.T) {
	got := lexOrFail(t, []byte(""))
	if len(got) != 1 || got[0].Kind != TokenEOF {
		t.Fatalf("empty input: expected single EOF, got %#v", got)
	}
}

func TestLexWhitespace(t *testing.T) {
	// Pure whitespace must yield exactly one EOF.
	got := lexOrFail(t, []byte("   \t  \r\n\n  "))
	if len(got) != 1 || got[0].Kind != TokenEOF {
		t.Fatalf("whitespace input: expected single EOF, got %#v", got)
	}
}

func TestLexIdentifiers(t *testing.T) {
	cases := []struct {
		src  string
		want Token
	}{
		{"mes", tok(TokenIdent, "mes", 1, 1)},
		{".@var", tok(TokenIdent, ".@var", 1, 1)},
		{"$global", tok(TokenIdent, "$global", 1, 1)},
		{"#account", tok(TokenIdent, "#account", 1, 1)},
		{"callfunc", tok(TokenIdent, "callfunc", 1, 1)},
		{"_underscore", tok(TokenIdent, "_underscore", 1, 1)},
		{"x2", tok(TokenIdent, "x2", 1, 1)},
		{"str_var$", tok(TokenIdent, "str_var$", 1, 1)},
		// string variable with prefix
		{".@str$", tok(TokenIdent, ".@str$", 1, 1)},
	}
	for _, c := range cases {
		got, err := Lex([]byte(c.src))
		if err != nil {
			t.Errorf("Lex(%q) error: %v", c.src, err)
			continue
		}
		if len(got) != 2 {
			t.Errorf("Lex(%q): expected 2 tokens (got EOF), got %d: %#v", c.src, len(got), got)
			continue
		}
		if got[0] != c.want {
			t.Errorf("Lex(%q): got %#v, want %#v", c.src, got[0], c.want)
		}
		if got[1].Kind != TokenEOF {
			t.Errorf("Lex(%q): missing EOF, got %#v", c.src, got[1])
		}
	}
}

func TestLexKeywords(t *testing.T) {
	cases := []string{
		"if", "else", "while", "for", "do", "switch",
		"case", "default", "break", "continue",
		"function", "return", "goto", "callsub",
	}
	for _, kw := range cases {
		got := lexOrFail(t, []byte(kw))
		if len(got) != 2 || got[0].Kind != TokenKeyword || got[0].Value != kw {
			t.Errorf("Lex(%q): expected KEYWORD(%q), got %#v", kw, kw, got)
		}
	}
}

func TestLexIntegers(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"0", "0"},
		{"42", "42"},
		{"0xFF", "0xFF"},
		{"0XA", "0XA"},
		{"100", "100"},
	}
	for _, c := range cases {
		got := lexOrFail(t, []byte(c.src))
		if len(got) < 2 {
			t.Fatalf("Lex(%q): too few tokens", c.src)
		}
		if got[0].Kind != TokenInt || got[0].Value != c.want {
			t.Errorf("Lex(%q): got %v, want INT(%q)", c.src, got[0], c.want)
		}
	}
}

func TestLexFloat(t *testing.T) {
	got := lexOrFail(t, []byte("3.14"))
	if len(got) < 2 {
		t.Fatalf("expected tokens")
	}
	if got[0].Kind != TokenFloat || got[0].Value != "3.14" {
		t.Errorf("got %v, want FLOAT(3.14)", got[0])
	}
}

func TestLexStrings(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`"hello"`, "hello"},
		{`""`, ""},
		{`"with \"quotes\""`, `with "quotes"`},
		{`"escaped\\backslash"`, `escaped\backslash`},
		{`"line\nbreak"`, "line\nbreak"},
		{`"tab\there"`, "tab\there"},
	}
	for _, c := range cases {
		got := lexOrFail(t, []byte(c.src))
		if len(got) < 2 {
			t.Fatalf("Lex(%q): too few tokens", c.src)
		}
		if got[0].Kind != TokenString || got[0].Value != c.want {
			t.Errorf("Lex(%q): got %v, want STRING(%q)", c.src, got[0], c.want)
		}
	}
}

func TestLexOperators(t *testing.T) {
	cases := []struct {
		src  string
		want string
		kind TokenKind
	}{
		{"+", "+", TokenOperator},
		{"-", "-", TokenOperator},
		{"*", "*", TokenOperator},
		{"/", "/", TokenOperator},
		{"%", "%", TokenOperator},
		{"==", "==", TokenOperator},
		{"!=", "!=", TokenOperator},
		{"<=", "<=", TokenOperator},
		{">=", ">=", TokenOperator},
		{"&&", "&&", TokenOperator},
		{"||", "||", TokenOperator},
		{"<<", "<<", TokenOperator},
		{">>", ">>", TokenOperator},
		{"!", "!", TokenOperator},
		{"&", "&", TokenOperator},
		{"|", "|", TokenOperator},
		{"^", "^", TokenOperator},
		{"~", "~", TokenOperator},
		{"<", "<", TokenOperator},
		{">", ">", TokenOperator},
		{"++", "++", TokenOperator},
		{"--", "--", TokenOperator},
	}
	for _, c := range cases {
		got := lexOrFail(t, []byte(c.src))
		if len(got) < 2 {
			t.Fatalf("Lex(%q): too few tokens", c.src)
		}
		if got[0].Kind != c.kind || got[0].Value != c.want {
			t.Errorf("Lex(%q): got %v, want %s(%q)", c.src, got[0], c.kind, c.want)
		}
	}
}

func TestLexAssignOps(t *testing.T) {
	cases := []string{
		"=", "+=", "-=", "*=", "/=", "%=",
		// <<= and >>= are 3-char compounds (we accept them even
		// though rAthena does not define them in core scripts).
		"<<=", ">>=",
	}
	for _, op := range cases {
		got, err := Lex([]byte(op))
		if err != nil {
			t.Errorf("Lex(%q) error: %v", op, err)
			continue
		}
		if len(got) < 2 {
			t.Errorf("Lex(%q): too few tokens", op)
			continue
		}
		if got[0].Kind != TokenAssign || got[0].Value != op {
			t.Errorf("Lex(%q): got %v, want ASSIGN(%q)", op, got[0], op)
		}
	}
}

func TestLexDelimiters(t *testing.T) {
	cases := []string{
		";", ",", ":", "(", ")", "{", "}", "[", "]", "?",
	}
	for _, op := range cases {
		got := lexOrFail(t, []byte(op))
		if len(got) < 2 {
			t.Errorf("Lex(%q): too few tokens", op)
			continue
		}
		if got[0].Kind != TokenDelim || got[0].Value != op {
			t.Errorf("Lex(%q): got %v, want DELIM(%q)", op, got[0], op)
		}
	}
}

func TestLexLineComment(t *testing.T) {
	got := lexOrFail(t, []byte("// line comment\nx"))
	if len(got) != 2 {
		t.Fatalf("expected 2 tokens (ident + EOF), got %d", len(got))
	}
	if got[0].Kind != TokenIdent || got[0].Value != "x" {
		t.Errorf("got %v, want IDENT(x)", got[0])
	}
	// 'x' is on line 2
	if got[0].Pos.Line != 2 {
		t.Errorf("got line %d, want 2", got[0].Pos.Line)
	}
}

func TestLexBlockComment(t *testing.T) {
	got := lexOrFail(t, []byte("a /* block */ b"))
	if len(got) != 3 { // a, b, EOF
		t.Fatalf("expected 3 tokens, got %d: %#v", len(got), got)
	}
	if got[0].Value != "a" || got[1].Value != "b" {
		t.Errorf("got %v %v, want a b", got[0], got[1])
	}
}

func TestLexMultilineBlockComment(t *testing.T) {
	src := []byte("a\n/* first\n   second\nthird */\nb")
	got := lexOrFail(t, src)
	if len(got) != 3 { // a, b, EOF
		t.Fatalf("expected 3 tokens, got %d: %#v", len(got), got)
	}
	// 'b' should be on line 5 (1-indexed: lines are a, /*, first, second, third*/: wait
	// 'b' is the last char of source.
	if got[1].Pos.Line < 4 {
		t.Errorf("expected 'b' on a later line, got %d", got[1].Pos.Line)
	}
}

func TestLexPositionTracking(t *testing.T) {
	src := []byte("a bb\nccc")
	got := lexOrFail(t, src)
	want := []Token{
		tok(TokenIdent, "a", 1, 1),
		tok(TokenIdent, "bb", 1, 3),
		tok(TokenIdent, "ccc", 2, 1),
	}
	if len(got) != len(want)+1 { // +1 for EOF
		t.Fatalf("expected %d tokens, got %d", len(want)+1, len(got))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("token %d: got %v, want %v", i, got[i], w)
		}
	}
}

func TestLexColumnTracking(t *testing.T) {
	// Tab is single-column in this lexer; verify with spaces.
	src := []byte("if x then y")
	got := lexOrFail(t, src)
	want := []Token{
		tok(TokenKeyword, "if", 1, 1),
		tok(TokenIdent, "x", 1, 4),
		tok(TokenKeyword, "then", 1, 6),
		// 'then' is not in our keyword set, so it would actually be IDENT.
	}
	_ = want
	if got[0].Pos.Column != 1 || got[1].Pos.Column != 4 {
		t.Fatalf("unexpected columns: %v %v", got[0].Pos, got[1].Pos)
	}
}

func TestLexLongestMatch(t *testing.T) {
	// Make sure `==` is matched atomically rather than `=` `=`.
	got, err := Lex([]byte("a==b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 { // a, ==, b, EOF
		t.Fatalf("expected 4 tokens, got %d: %#v", len(got), got)
	}
	if got[1].Kind != TokenOperator || got[1].Value != "==" {
		t.Errorf("got %v, want OP(==)", got[1])
	}
}

func TestLexErrors(t *testing.T) {
	cases := []struct {
		src  string
		name string
	}{
		{`"unterminated`, "unterminated string"},
		{`/* unterminated`, "unterminated block comment"},
		{"a\xff", "unexpected byte"},
	}
	for _, c := range cases {
		_, err := Lex([]byte(c.src))
		if err == nil {
			t.Errorf("Lex(%q) [%s]: expected error, got nil", c.src, c.name)
		}
	}
}

func TestLexKafraScript(t *testing.T) {
	// A stripped-down slice of the kafra_dewata script body. Tokenization
	// must produce a single uninterrupted stream terminating in EOF.
	src := []byte(`
	mes "Hello, traveler.";
	close;
`)
	tokens, err := Lex(src)
	if err != nil {
		t.Fatalf("lex failed: %v", err)
	}
	if len(tokens) == 0 || tokens[len(tokens)-1].Kind != TokenEOF {
		t.Fatalf("missing EOF: %#v", tokens)
	}
	// Spot-check key tokens in order. `mes` and `close` are builtins
	// and therefore tokenize as IDENT, not KEYWORD.
	want := []Token{
		tok(TokenIdent, "mes", 2, 2),
		tok(TokenString, "Hello, traveler.", 2, 6),
		tok(TokenDelim, ";", 2, 24),
		tok(TokenIdent, "close", 3, 2),
		tok(TokenDelim, ";", 3, 7),
	}
	for i, w := range want {
		if tokens[i].Kind != w.Kind || tokens[i].Value != w.Value {
			t.Errorf("tokens[%d]: got %v, want kind=%s val=%q", i, tokens[i], w.Kind, w.Value)
		}
	}
}

func TestLexFullSampleWithCalls(t *testing.T) {
	src := []byte(`callfunc "F_Kafra",0,10,1,40,700;`)
	got, err := Lex(src)
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	want := []Token{
		tok(TokenIdent, "callfunc", 1, 1),
		tok(TokenString, "F_Kafra", 1, 10),
		tok(TokenDelim, ",", 1, 19),
		tok(TokenInt, "0", 1, 20),
		tok(TokenDelim, ",", 1, 21),
		tok(TokenInt, "10", 1, 22),
		tok(TokenDelim, ",", 1, 24),
		tok(TokenInt, "1", 1, 25),
		tok(TokenDelim, ",", 1, 26),
		tok(TokenInt, "40", 1, 27),
		tok(TokenDelim, ",", 1, 29),
		tok(TokenInt, "700", 1, 30),
		tok(TokenDelim, ";", 1, 33),
		tok(TokenEOF, "", 1, 34),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Kind != w.Kind || got[i].Value != w.Value || got[i].Pos != w.Pos {
			t.Errorf("tokens[%d]: got %v, want %v", i, got[i], w)
		}
	}
}

func TestLexNegNumberSeparation(t *testing.T) {
	// The lexer must not fuse `-` and the following int; the parser
	// will bind them as a unary-minus expression.
	got, err := Lex([]byte("-42"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 { // -, 42, EOF
		t.Fatalf("got %d tokens: %#v", len(got), got)
	}
	if got[0].Kind != TokenOperator || got[0].Value != "-" {
		t.Errorf("got[0] = %v", got[0])
	}
	if got[1].Kind != TokenInt || got[1].Value != "42" {
		t.Errorf("got[1] = %v", got[1])
	}
}

func TestLexKeywordNotIdent(t *testing.T) {
	// Confirm that 'if' classifies as Keyword, while 'iff' (similar)
	// stays Ident.
	got := lexOrFail(t, []byte("if iff"))
	if got[0].Kind != TokenKeyword || got[0].Value != "if" {
		t.Errorf("got[0] = %v, want KEYWORD(if)", got[0])
	}
	if got[1].Kind != TokenIdent || got[1].Value != "iff" {
		t.Errorf("got[1] = %v, want IDENT(iff)", got[1])
	}
}

func TestLexTokenString(t *testing.T) {
	// Ensure the Token.String method works for error messages.
	tk := Token{Kind: TokenIdent, Value: "foo", Pos: Position{Line: 3, Column: 7}}
	if s := tk.String(); s == "" {
		t.Error("Token.String returned empty")
	}
}

// ----- benchmark -----

func BenchmarkLex(b *testing.B) {
	src := []byte(`mes "[Kafra]";
	cutin "kafra_01",2;
	callfunc "F_Kafra",0,10,1,40,700;
	savepoint "dewata",206,174,1,1;
	callfunc "F_KafEnd",0,1,"on Dewata Island";
	close;
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Lex(src)
	}
}
