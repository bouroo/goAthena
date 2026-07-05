//go:build unit

package script

import (
	"testing"
)

func TestPositionString(t *testing.T) {
	p := Position{Line: 12, Column: 7}
	if got := p.String(); got != "12:7" {
		t.Errorf("Position.String = %q, want %q", got, "12:7")
	}
}

func TestTokenKindString(t *testing.T) {
	cases := []struct {
		k    TokenKind
		want string
	}{
		{TokenEOF, "EOF"},
		{TokenIdent, "IDENT"},
		{TokenInt, "INT"},
		{TokenFloat, "FLOAT"},
		{TokenString, "STRING"},
		{TokenKeyword, "KEYWORD"},
		{TokenOperator, "OP"},
		{TokenAssign, "ASSIGN"},
		{TokenDelim, "DELIM"},
		{TokenComment, "COMMENT"},
		{TokenNewline, "NEWLINE"},
		{TokenKind(999), "TokenKind(999)"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("TokenKind(%d).String = %q, want %q", c.k, got, c.want)
		}
	}
}

func TestOpcodeString(t *testing.T) {
	// Spot-check a representative opcode.
	if got := OpInt.String(); got != "INT" {
		t.Errorf("OpInt.String = %q, want %q", got, "INT")
	}
	if got := OpFunc.String(); got != "FUNC" {
		t.Errorf("OpFunc.String = %q, want %q", got, "FUNC")
	}
	// Out-of-range opcode returns the fallback.
	if got := Opcode(255).String(); got == "" {
		t.Error("Opcode(255).String returned empty")
	}
}

func TestInstructionString(t *testing.T) {
	cases := []struct {
		ins  Instruction
		want string
	}{
		{Instruction{Op: OpInt, Operand: 42}, "INT 42"},
		{Instruction{Op: OpLine, Operand: 7}, "LINE 7"},
		{Instruction{Op: OpVar, Str: "x"}, "VAR x"},
		{Instruction{Op: OpGoto, Str: "L1"}, "GOTO L1"},
		{Instruction{Op: OpEOF}, "EOF"},
	}
	for _, c := range cases {
		if got := c.ins.String(); got != c.want {
			t.Errorf("Instruction(%+v).String = %q, want %q", c.ins, got, c.want)
		}
	}
}

func TestCompiledScriptConstruction(t *testing.T) {
	cs := NewCompiledScript("kaf_dewata")
	if cs.Name != "kaf_dewata" {
		t.Errorf("Name = %q", cs.Name)
	}
	if cs.Labels == nil {
		t.Error("Labels map should be non-nil after NewCompiledScript")
	}
	cs.Labels["L_Start"] = 0
	if idx, ok := cs.LookupLabel("L_Start"); !ok || idx != 0 {
		t.Errorf("LookupLabel: idx=%d ok=%v", idx, ok)
	}
	// LookupLabel on nil receiver is safe.
	var nilCS *CompiledScript
	if _, ok := nilCS.LookupLabel("x"); ok {
		t.Error("nil LookupLabel should miss")
	}
}

func TestCompiledScriptSetConstruction(t *testing.T) {
	s := NewCompiledScriptSet()
	if s.Scripts == nil || s.Funcs == nil {
		t.Error("Maps must be initialized")
	}
	s.Scripts["kaf_dewata"] = NewCompiledScript("kaf_dewata")
	s.Funcs["F_Kafra"] = NewCompiledScript("F_Kafra")
	if got, ok := s.Scripts["kaf_dewata"]; !ok || got.Name != "kaf_dewata" {
		t.Errorf("Scripts lookup failed")
	}
}

func TestWarpDefKey(t *testing.T) {
	w := WarpDef{
		MapName: "prontera", X: 100, Y: 200, TriggerX: 100, TriggerY: 200,
		DestMap: "payon", DestX: 50, DestY: 75,
	}
	k := w.Key()
	if k.MapName != "prontera" || k.TriggerX != 100 || k.TriggerY != 200 {
		t.Errorf("Key = %+v", k)
	}
}

func TestASTNodeInterfaceAssertions(t *testing.T) {
	// Compile-time assertions are already guarded by `var _ Stmt = ...`,
	// but exercising a couple of them at runtime guards against future
	// accidental removal.
	var pos Position = Position{Line: 1, Column: 1}
	if (&IntLit{Value: 1, pos: pos}).Pos() != pos {
		t.Error("IntLit.Pos")
	}
	if (&StrLit{Value: "x", pos: pos}).Pos() != pos {
		t.Error("StrLit.Pos")
	}
	if (&IdentExpr{Name: "x", pos: pos}).Pos() != pos {
		t.Error("IdentExpr.Pos")
	}
	if (&IfStmt{pos: pos}).Pos() != pos {
		t.Error("IfStmt.Pos")
	}
	if (&MenuOption{pos: pos}).Pos() != pos {
		t.Error("MenuOption.Pos")
	}
	if (&SwitchCase{pos: pos}).Pos() != pos {
		t.Error("SwitchCase.Pos")
	}
}

func TestFilePos(t *testing.T) {
	// Empty file falls back to 1:1.
	empty := &File{}
	if empty.Pos() != (Position{Line: 1, Column: 1}) {
		t.Errorf("empty File.Pos = %v", empty.Pos())
	}
	// File with body uses first body's pos.
	body := &File{Body: []Stmt{&NextStmt{pos: Position{Line: 5, Column: 3}}}}
	if body.Pos() != (Position{Line: 5, Column: 3}) {
		t.Errorf("File.Pos from body = %v", body.Pos())
	}
	// File with header uses header pos.
	hdr := &File{header: &NPCHeader{MapName: "x", pos: Position{Line: 9, Column: 1}}}
	if hdr.Pos() != (Position{Line: 9, Column: 1}) {
		t.Errorf("File.Pos from header = %v", hdr.Pos())
	}
}

func TestNPCHeaderString(t *testing.T) {
	h := &NPCHeader{
		MapName: "dewata", X: 202, Y: 184, Facing: 6,
		Name: "Kafra Employee", Type: "script",
	}
	if h.String() == "" {
		t.Error("NPCHeader.String returned empty")
	}
}

func TestLexErrorError(t *testing.T) {
	e := &LexError{Pos: Position{Line: 3, Column: 5}, Msg: "bad"}
	if e.Error() == "" {
		t.Error("LexError.Error returned empty")
	}
}
