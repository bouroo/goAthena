//go:build unit

package script

import (
	"testing"
)

// parseOrFail is a test helper that parses src and fails on error.
func parseOrFail(t *testing.T, src string) []*File {
	t.Helper()
	files, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", src, err)
	}
	return files
}

func TestParseFloatingHeader(t *testing.T) {
	// The healer's floating header: `-	script	Healer	-1,{` (tabs between).
	files := parseOrFail(t, "-\tscript\tHealer\t-1,{\nend;\n}\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	h := files[0].Header()
	if h == nil {
		t.Fatal("expected non-nil header")
	}
	if h.Name != "Healer" {
		t.Errorf("header Name = %q, want Healer", h.Name)
	}
	if h.SpriteID != -1 {
		t.Errorf("header SpriteID = %d, want -1", h.SpriteID)
	}
	if h.Type != "float" {
		t.Errorf("header Type = %q, want float", h.Type)
	}
}

func TestParsePlacedHeader(t *testing.T) {
	// placed NPC with a trigger region: sprite 909, trigger 3x3.
	files := parseOrFail(t, "prontera,150,150,4\tscript\tHealer\t909,3,3,{\nend;\n}\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	h := files[0].Header()
	if h.MapName != "prontera" {
		t.Errorf("MapName = %q, want prontera", h.MapName)
	}
	if h.X != 150 || h.Y != 150 || h.Facing != 4 {
		t.Errorf("pos = (%d,%d,facing %d), want (150,150,4)", h.X, h.Y, h.Facing)
	}
	if h.SpriteID != 909 {
		t.Errorf("SpriteID = %d, want 909", h.SpriteID)
	}
	if h.TriggerX != 3 || h.TriggerY != 3 {
		t.Errorf("trigger = (%d,%d), want (3,3)", h.TriggerX, h.TriggerY)
	}
	if h.Type != "script" {
		t.Errorf("Type = %q, want script", h.Type)
	}
}

func TestParseFunctionScript(t *testing.T) {
	files := parseOrFail(t, "function script F_InsertComma {\nreturn;\n}\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	h := files[0].Header()
	if h.Name != "F_InsertComma" {
		t.Errorf("Name = %q", h.Name)
	}
	if h.Type != "func" {
		t.Errorf("Type = %q, want func", h.Type)
	}
}

func TestParseDialogStatements(t *testing.T) {
	// Dialog builtins (mes/next/close) are produced by the parser as *CallStmt
	// and resolved by name at runtime through the builtin map — there is no
	// dedicated node, so we assert the CallStmt's name and args.
	const src = "-\tscript\tN\t-1,{\n" +
		`mes "[Healer]";` + "\n" +
		`next;` + "\n" +
		`mes "Healed!";` + "\n" +
		`close;` + "\n" +
		"}\n"
	files := parseOrFail(t, src)
	if len(files[0].Body) != 4 {
		t.Fatalf("expected 4 body stmts, got %d", len(files[0].Body))
	}
	mes0 := wantCall(t, files[0].Body[0], "mes", 1)
	str, ok := mes0.Args[0].(*StrLit)
	if !ok {
		t.Fatalf("mes arg = %T, want *StrLit", mes0.Args[0])
	}
	if str.Value != "[Healer]" {
		t.Errorf("mes arg = %q, want [Healer]", str.Value)
	}
	wantCall(t, files[0].Body[1], "next", 0)
	wantCall(t, files[0].Body[3], "close", 0)
}

// wantCall asserts stmt is a *CallStmt with the given builtin name and arg
// count, returning the cast call for further inspection.
func wantCall(t *testing.T, stmt Stmt, name string, argc int) *CallStmt {
	t.Helper()
	c, ok := stmt.(*CallStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *CallStmt(%q)", stmt, name)
	}
	if c.Name != name {
		t.Errorf("call name = %q, want %q", c.Name, name)
	}
	if len(c.Args) != argc {
		t.Errorf("call %q args = %d, want %d", name, len(c.Args), argc)
	}
	return c
}

func TestParseAssignAndCompound(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		".@Price = 0;\n" +
		".@Price += 50;\n" +
		"set Zeny, 100;\n" +
		"}\n"
	files := parseOrFail(t, src)
	body := files[0].Body
	if len(body) != 3 {
		t.Fatalf("expected 3 stmts, got %d", len(body))
	}
	a0, ok := body[0].(*AssignStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *AssignStmt", body[0])
	}
	if a0.Op != "=" {
		t.Errorf("body[0] Op = %q, want =", a0.Op)
	}
	a1, ok := body[1].(*AssignStmt)
	if !ok {
		t.Fatalf("body[1] = %T, want *AssignStmt (compound)", body[1])
	}
	if a1.Op != "+=" {
		t.Errorf("body[1] Op = %q, want +=", a1.Op)
	}
	// `set Zeny, 100;` parses as a CallStmt, desugared to assignment in the
	// compiler.
	call, ok := body[2].(*CallStmt)
	if !ok {
		t.Fatalf("body[2] = %T, want *CallStmt (set)", body[2])
	}
	if call.Name != "set" || len(call.Args) != 2 {
		t.Errorf("set call = %+v", call)
	}
}

func TestParseIfElse(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		`if (.@Price > 0) mes "costs"; else mes "free";` + "\n" +
		"}\n"
	files := parseOrFail(t, src)
	iff, ok := files[0].Body[0].(*IfStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *IfStmt", files[0].Body[0])
	}
	if len(iff.Then) != 1 || len(iff.Else) != 1 {
		t.Fatalf("then=%d else=%d, want 1/1", len(iff.Then), len(iff.Else))
	}
	cond, ok := iff.Cond.(*BinExpr)
	if !ok {
		t.Fatalf("cond = %T, want *BinExpr", iff.Cond)
	}
	if cond.Op != ">" {
		t.Errorf("cond Op = %q, want >", cond.Op)
	}
}

func TestParseExprPrecedence(t *testing.T) {
	// 1 + 2 * 3 binds * tighter than +, so the AST is (1 + (2 * 3)). Asserted
	// via an assignment so it does not depend on the mes(...) paren-vs-call
	// ambiguity.
	files := parseOrFail(t, "-\tscript\tN\t-1,{\n.@r = 1 + 2 * 3;\n}\n")
	a, ok := files[0].Body[0].(*AssignStmt)
	if !ok || a.Op != "=" {
		t.Fatalf("body[0] = %T %+v, want *AssignStmt =", files[0].Body[0], files[0].Body[0])
	}
	sum, ok := a.Rhs.(*BinExpr)
	if !ok || sum.Op != "+" {
		t.Fatalf("rhs = %T %+v, want *BinExpr +", a.Rhs, a.Rhs)
	}
	mul, ok := sum.Rhs.(*BinExpr)
	if !ok || mul.Op != "*" {
		t.Fatalf("sum.Rhs = %T %+v, want *BinExpr *", sum.Rhs, sum.Rhs)
	}
}

func TestParseLabelGotoCallsub(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		"goto L_Skip;\n" +
		"mes \"unreached\";\n" +
		"L_Skip:\n" +
		"mes \"reached\";\n" +
		"}\n"
	files := parseOrFail(t, src)
	body := files[0].Body
	if _, ok := body[0].(*GotoStmt); !ok {
		t.Errorf("body[0] = %T, want *GotoStmt", body[0])
	}
	lbl, ok := body[2].(*LabelDecl)
	if !ok || lbl.Name != "L_Skip" {
		t.Errorf("body[2] = %+v, want LabelDecl L_Skip", body[2])
	}
}

func TestParseBuiltinCallForms(t *testing.T) {
	// Both command form `mes "x";` and function form `mes("x");` are valid.
	const src = "-\tscript\tN\t-1,{\nmes \"a\";\nmes(\"b\");\n}\n"
	files := parseOrFail(t, src)
	m0 := files[0].Body[0].(*CallStmt)
	m1 := files[0].Body[1].(*CallStmt)
	if m0.Name != "mes" || m1.Name != "mes" {
		t.Errorf("call names = %q %q, want mes mes", m0.Name, m1.Name)
	}
	if len(m0.Args) != 1 || len(m1.Args) != 1 {
		t.Errorf("arg counts = %d %d, want 1 1", len(m0.Args), len(m1.Args))
	}
}

func TestParseRealHealerExcerpt(t *testing.T) {
	// A representative slice of the real healer.txt exercising the constructs
	// M11a must parse: floating header, local var, if-with-call-expr, select,
	// compound assign, percentheal, end.
	const src = `-	script	Healer	-1,{
	.@Price = 0;
	if (@HD > gettimetick(2))
		end;
	if (.@Price) {
		message strcharinfo(0), "Healing costs " + callfunc("F_InsertComma",.@Price) + " Zeny.";
		if (Zeny < .@Price)
			end;
		if (select("Heal:Cancel") == 2)
			end;
		Zeny -= .@Price;
	}
	percentheal 100,100;
	close;
}
`
	files := parseOrFail(t, src)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Header().Name != "Healer" {
		t.Errorf("Name = %q", files[0].Header().Name)
	}
	// 7 top-level statements: .@Price=, if(end), if(price-block), percentheal, close.
	// (The price-block if counts as one stmt.)
	if len(files[0].Body) < 5 {
		t.Errorf("expected >=5 body stmts, got %d", len(files[0].Body))
	}
}
