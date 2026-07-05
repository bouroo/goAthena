package script

import "strings"

// Node is the root interface for all AST nodes. Every AST node knows
// its source position so that error messages and runtime diagnostics
// can point to the offending construct.
type Node interface {
	Pos() Position
	nodeString() string
}

// Stmt is a statement node. Statements never appear in expression
// position; they have side effects.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is an expression node. Expressions yield a Value when evaluated.
type Expr interface {
	Node
	exprNode()
}

// File represents a parsed script file: a tab-separated NPC header
// (possibly nil for floating `function script` definitions) followed
// by a list of body statements.
type File struct {
	header *NPCHeader
	Body   []Stmt
}

// Pos returns the position of the first body statement, or the header
// if the body is empty.
func (f *File) Pos() Position {
	if f.header != nil {
		return f.header.pos
	}
	if len(f.Body) > 0 {
		return f.Body[0].Pos()
	}
	return Position{Line: 1, Column: 1}
}

func (*File) nodeString() string { return "File" }

// NPCHeader is the tab-separated NPC definition header. It captures
// every column of rAthena's npc_parsename layout (npc.cpp:3668).
type NPCHeader struct {
	MapName    string
	X          int
	Y          int
	Facing     int
	Name       string
	SpriteName string
	SpriteID   int
	TriggerX   int
	TriggerY   int
	Type       string
	pos        Position
}

// Pos returns the header's source position.
func (h *NPCHeader) Pos() Position { return h.pos }

// String returns a compact representation used in error messages and
// disassembly.
func (h *NPCHeader) String() string {
	var b strings.Builder
	b.WriteString(h.MapName)
	b.WriteByte(',')
	b.WriteString(itoa(h.X))
	b.WriteByte(',')
	b.WriteString(itoa(h.Y))
	b.WriteByte(',')
	b.WriteString(itoa(h.Facing))
	b.WriteByte('\t')
	b.WriteString(h.Type)
	b.WriteByte('\t')
	b.WriteString(h.Name)
	return b.String()
}

func (*NPCHeader) nodeString() string { return "NPCHeader" }

// itoa is a no-allocation integer-to-string converter for the handful
// of small fields on NPCHeader. It avoids pulling strconv into hot
// formatting paths.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ----- Concrete statement types -----
//
// Each AST node carries its source position in a private field; the
// Pos() method exposes it. Field types are unexported because callers
// should not mutate positions after construction — the parser sets
// them once and the VM consumes them at debug time.

// MesStmt is `mes "..."` — push text to the dialog. Does not pause.
type MesStmt struct {
	Msg Expr
	pos Position
}

// Pos returns the statement's source position.
func (s *MesStmt) Pos() Position { return s.pos }

func (*MesStmt) stmtNode()          {}
func (*MesStmt) nodeString() string { return "MesStmt" }

// NextStmt is `next;` — pause and wait for the Next button.
type NextStmt struct{ pos Position }

// Pos returns the statement's source position.
func (s *NextStmt) Pos() Position { return s.pos }

func (*NextStmt) stmtNode()          {}
func (*NextStmt) nodeString() string { return "NextStmt" }

// CloseStmt is `close;` — close dialog and end script.
type CloseStmt struct{ pos Position }

// Pos returns the statement's source position.
func (s *CloseStmt) Pos() Position { return s.pos }

func (*CloseStmt) stmtNode()          {}
func (*CloseStmt) nodeString() string { return "CloseStmt" }

// Close2Stmt is `close2;` — close dialog but continue executing.
type Close2Stmt struct{ pos Position }

// Pos returns the statement's source position.
func (s *Close2Stmt) Pos() Position { return s.pos }

func (*Close2Stmt) stmtNode()          {}
func (*Close2Stmt) nodeString() string { return "Close2Stmt" }

// EndStmt is the implicit `}` or explicit `end;` terminator.
type EndStmt struct{ pos Position }

// Pos returns the statement's source position.
func (s *EndStmt) Pos() Position { return s.pos }

func (*EndStmt) stmtNode()          {}
func (*EndStmt) nodeString() string { return "EndStmt" }

// MenuStmt is `menu("optA",L_a,"optB",L_b,...);` — pair-encoded options.
// Each option pairs a string prompt with a label or callsub target.
type MenuStmt struct {
	Options []MenuOption
	pos     Position
}

// Pos returns the statement's source position.
func (s *MenuStmt) Pos() Position { return s.pos }

func (*MenuStmt) stmtNode()          {}
func (*MenuStmt) nodeString() string { return "MenuStmt" }

// MenuOption is one entry of a menu/select/prompt. Label is the jump
// target; the parser accepts either a bare identifier or a `callsub L`
// form via CallExpr.Name.
type MenuOption struct {
	Prompt Expr
	Label  string
	pos    Position
}

// SelectStmt is `select("...");` — like menu but the index is returned
// rather than jumping to a label.
type SelectStmt struct {
	Options []MenuOption
	pos     Position
}

// Pos returns the statement's source position.
func (s *SelectStmt) Pos() Position { return s.pos }

func (*SelectStmt) stmtNode()          {}
func (*SelectStmt) nodeString() string { return "SelectStmt" }

// InputStmt is `input @var;` — read an integer from the player into a
// variable.
type InputStmt struct {
	Dst Expr
	pos Position
}

// Pos returns the statement's source position.
func (s *InputStmt) Pos() Position { return s.pos }

func (*InputStmt) stmtNode()          {}
func (*InputStmt) nodeString() string { return "InputStmt" }

// IfStmt is `if (cond) { ... } else { ... }`. Else may be nil.
type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
	pos  Position
}

// Pos returns the statement's source position.
func (s *IfStmt) Pos() Position { return s.pos }

func (*IfStmt) stmtNode()          {}
func (*IfStmt) nodeString() string { return "IfStmt" }

// WhileStmt is `while (cond) { ... }`.
type WhileStmt struct {
	Cond Expr
	Body []Stmt
	pos  Position
}

// Pos returns the statement's source position.
func (s *WhileStmt) Pos() Position { return s.pos }

func (*WhileStmt) stmtNode()          {}
func (*WhileStmt) nodeString() string { return "WhileStmt" }

// DoWhileStmt is `do { ... } while (cond);`.
type DoWhileStmt struct {
	Body []Stmt
	Cond Expr
	pos  Position
}

// Pos returns the statement's source position.
func (s *DoWhileStmt) Pos() Position { return s.pos }

func (*DoWhileStmt) stmtNode()          {}
func (*DoWhileStmt) nodeString() string { return "DoWhileStmt" }

// ForStmt is `for (init; cond; post) { ... }`. Each clause is a slice
// because the for-init grammar allows comma-separated statements.
type ForStmt struct {
	Init []Stmt
	Cond Expr
	Post []Stmt
	Body []Stmt
	pos  Position
}

// Pos returns the statement's source position.
func (s *ForStmt) Pos() Position { return s.pos }

func (*ForStmt) stmtNode()          {}
func (*ForStmt) nodeString() string { return "ForStmt" }

// SwitchStmt is `switch (val) { case X: ...; default: ...; }`. The
// parser lowers case labels to internal labels per rAthena.
type SwitchStmt struct {
	Value Expr
	Cases []SwitchCase
	pos   Position
}

// Pos returns the statement's source position.
func (s *SwitchStmt) Pos() Position { return s.pos }

func (*SwitchStmt) stmtNode()          {}
func (*SwitchStmt) nodeString() string { return "SwitchStmt" }

// SwitchCase is one arm of a switch. Values may be empty for the
// default case.
type SwitchCase struct {
	Values []Expr
	Body   []Stmt
	pos    Position
}

// BreakStmt is `break;` — exit the innermost loop or switch.
type BreakStmt struct{ pos Position }

// Pos returns the statement's source position.
func (s *BreakStmt) Pos() Position { return s.pos }

func (*BreakStmt) stmtNode()          {}
func (*BreakStmt) nodeString() string { return "BreakStmt" }

// ContinueStmt is `continue;` — restart the innermost loop.
type ContinueStmt struct{ pos Position }

// Pos returns the statement's source position.
func (s *ContinueStmt) Pos() Position { return s.pos }

func (*ContinueStmt) stmtNode()          {}
func (*ContinueStmt) nodeString() string { return "ContinueStmt" }

// AssignStmt is `lhs = rhs` or `lhs <op>= rhs`. Op is the source-text
// operator (`=`, `+=`, etc.) for diagnostic fidelity; the compiler
// selects the matching VM opcode.
type AssignStmt struct {
	//nolint:revive // Required by task spec.
	Lhs Expr
	Op  string
	//nolint:revive // Required by task spec.
	Rhs Expr
	pos Position
}

// Pos returns the statement's source position.
func (s *AssignStmt) Pos() Position { return s.pos }

func (*AssignStmt) stmtNode()          {}
func (*AssignStmt) nodeString() string { return "AssignStmt" }

// CallStmt is a builtin invocation (`set`, `mes`, `warp`, ...) emitted
// as a statement. Args holds the parsed argument expressions; the Name
// is resolved to a builtin at compile time.
type CallStmt struct {
	Name string
	Args []Expr
	pos  Position
}

// Pos returns the statement's source position.
func (s *CallStmt) Pos() Position { return s.pos }

func (*CallStmt) stmtNode()          {}
func (*CallStmt) nodeString() string { return "CallStmt" }

// GotoStmt is `goto Label;`.
type GotoStmt struct {
	Label string
	pos   Position
}

// Pos returns the statement's source position.
func (s *GotoStmt) Pos() Position { return s.pos }

func (*GotoStmt) stmtNode()          {}
func (*GotoStmt) nodeString() string { return "GotoStmt" }

// CallSubStmt is `callsub Label, args...;`.
type CallSubStmt struct {
	Label string
	Args  []Expr
	pos   Position
}

// Pos returns the statement's source position.
func (s *CallSubStmt) Pos() Position { return s.pos }

func (*CallSubStmt) stmtNode()          {}
func (*CallSubStmt) nodeString() string { return "CallSubStmt" }

// ReturnStmt is `return [value];`.
type ReturnStmt struct {
	Value Expr
	pos   Position
}

// Pos returns the statement's source position.
func (s *ReturnStmt) Pos() Position { return s.pos }

func (*ReturnStmt) stmtNode()          {}
func (*ReturnStmt) nodeString() string { return "ReturnStmt" }

// BlockStmt is `{ stmts... }` used as a statement. rAthena scripts do
// not commonly nest blocks as standalone statements, but the parser
// accepts them so braced bodies can appear anywhere a statement is
// expected (e.g. inside a case or as a macro-style wrapper).
type BlockStmt struct {
	Body []Stmt
	pos  Position
}

// Pos returns the statement's source position.
func (s *BlockStmt) Pos() Position { return s.pos }

func (*BlockStmt) stmtNode()          {}
func (*BlockStmt) nodeString() string { return "BlockStmt" }

// NewBlockStmt creates a block statement.
func NewBlockStmt(body []Stmt, pos Position) *BlockStmt {
	return &BlockStmt{Body: body, pos: pos}
}

// LabelDecl is `L_Name:` — a label declaration. Standalone (not part
// of a `case`) and recognized at parse time.
type LabelDecl struct {
	Name string
	pos  Position
}

// Pos returns the statement's source position.
func (s *LabelDecl) Pos() Position { return s.pos }

func (*LabelDecl) stmtNode()          {}
func (*LabelDecl) nodeString() string { return "LabelDecl" }

// FuncDecl is `function script F_Name { ... }` — a global function
// definition. Stored in CompiledScriptSet.Funcs.
type FuncDecl struct {
	Name string
	Body []Stmt
	pos  Position
}

// Pos returns the statement's source position.
func (s *FuncDecl) Pos() Position { return s.pos }

func (*FuncDecl) stmtNode()          {}
func (*FuncDecl) nodeString() string { return "FuncDecl" }

// FuncRefStmt is `function F_Name;` — a forward or backward reference to
// a function defined elsewhere. It has no body and is compiled as a
// no-op; it merely marks that the name is reachable.
type FuncRefStmt struct {
	Name string
	pos  Position
}

// Pos returns the statement's source position.
func (s *FuncRefStmt) Pos() Position { return s.pos }

func (*FuncRefStmt) stmtNode()          {}
func (*FuncRefStmt) nodeString() string { return "FuncRefStmt" }

// ----- Concrete expression types -----

// IntLit is an integer literal. Value is the parsed int64.
type IntLit struct {
	Value int64
	pos   Position
}

// Pos returns the expression's source position.
func (e *IntLit) Pos() Position { return e.pos }

func (*IntLit) exprNode()          {}
func (*IntLit) nodeString() string { return "IntLit" }

// FloatLit is a float literal. Rare in rAthena scripts.
type FloatLit struct {
	Value float64
	pos   Position
}

// Pos returns the expression's source position.
func (e *FloatLit) Pos() Position { return e.pos }

func (*FloatLit) exprNode()          {}
func (*FloatLit) nodeString() string { return "FloatLit" }

// StrLit is a string literal. Value is the decoded string with escape
// sequences resolved.
type StrLit struct {
	Value string
	pos   Position
}

// Pos returns the expression's source position.
func (e *StrLit) Pos() Position { return e.pos }

func (*StrLit) exprNode()          {}
func (*StrLit) nodeString() string { return "StrLit" }

// IdentExpr is a variable reference, possibly prefixed (`.@`, `@`, `#`,
// `$`, `'`) and possibly suffixed with `$` for string variables. Name
// preserves the source text exactly.
type IdentExpr struct {
	Name string
	pos  Position
}

// Pos returns the expression's source position.
func (e *IdentExpr) Pos() Position { return e.pos }

func (*IdentExpr) exprNode()          {}
func (*IdentExpr) nodeString() string { return "IdentExpr" }

// BinExpr is a binary operation: `Lhs Op Rhs`. Op is the source-text
// operator (`+`, `-`, `==`, etc.).
type BinExpr struct {
	Op string
	//nolint:revive // Required by task spec.
	Lhs Expr
	//nolint:revive // Required by task spec.
	Rhs Expr
	pos Position
}

// Pos returns the expression's source position.
func (e *BinExpr) Pos() Position { return e.pos }

func (*BinExpr) exprNode()          {}
func (*BinExpr) nodeString() string { return "BinExpr" }

// UnaryExpr is a prefix unary operation: `Op Operand`. Op is one of
// `-`, `!`, `~`, `++`, `--`.
type UnaryExpr struct {
	Op      string
	Operand Expr
	pos     Position
}

// Pos returns the expression's source position.
func (e *UnaryExpr) Pos() Position { return e.pos }

func (*UnaryExpr) exprNode()          {}
func (*UnaryExpr) nodeString() string { return "UnaryExpr" }

// TernaryExpr is `Cond ? Then : Else`. Lowered to OpExpr2 / OpLOr
// branch at compile time, but kept as a first-class AST node so error
// messages name it directly.
type TernaryExpr struct {
	Cond Expr
	Then Expr
	Else Expr
	pos  Position
}

// Pos returns the expression's source position.
func (e *TernaryExpr) Pos() Position { return e.pos }

func (*TernaryExpr) exprNode()          {}
func (*TernaryExpr) nodeString() string { return "TernaryExpr" }

// CallExpr is a function call expression. Distinguishes between builtin
// and user-defined functions only by name lookup at compile time.
type CallExpr struct {
	Name string
	Args []Expr
	pos  Position
}

// Pos returns the expression's source position.
func (e *CallExpr) Pos() Position { return e.pos }

func (*CallExpr) exprNode()          {}
func (*CallExpr) nodeString() string { return "CallExpr" }

// IndexExpr is `target[index]` — array element access. High 32 bits of
// the variable id encode the index (per rAthena reference_uid).
type IndexExpr struct {
	Target Expr
	Index  Expr
	pos    Position
}

// Pos returns the expression's source position.
func (e *IndexExpr) Pos() Position { return e.pos }

func (*IndexExpr) exprNode()          {}
func (*IndexExpr) nodeString() string { return "IndexExpr" }

// ParenExpr is `(expr)` — preserved for source-fidelity in error
// messages but usually collapsed by the parser.
type ParenExpr struct {
	Inner Expr
	pos   Position
}

// Pos returns the expression's source position.
func (e *ParenExpr) Pos() Position { return e.pos }

func (*ParenExpr) exprNode()          {}
func (*ParenExpr) nodeString() string { return "ParenExpr" }

// MenuOption and SwitchCase are passive data carriers stored inside
// their parent statements; they implement Node for completeness so
// debug printers can traverse them uniformly.
func (*MenuOption) nodeString() string { return "MenuOption" }
func (*SwitchCase) nodeString() string { return "SwitchCase" }

// Pos returns the option's source position. Implements Node.
func (m *MenuOption) Pos() Position { return m.pos }

// Pos returns the case's source position. Implements Node.
func (sc *SwitchCase) Pos() Position { return sc.pos }

// Compile-time interface assertions: every concrete type above must
// satisfy the appropriate interface or compilation will fail.
var (
	_ Stmt = (*MesStmt)(nil)
	_ Stmt = (*NextStmt)(nil)
	_ Stmt = (*CloseStmt)(nil)
	_ Stmt = (*Close2Stmt)(nil)
	_ Stmt = (*EndStmt)(nil)
	_ Stmt = (*MenuStmt)(nil)
	_ Stmt = (*SelectStmt)(nil)
	_ Stmt = (*InputStmt)(nil)
	_ Stmt = (*IfStmt)(nil)
	_ Stmt = (*WhileStmt)(nil)
	_ Stmt = (*DoWhileStmt)(nil)
	_ Stmt = (*ForStmt)(nil)
	_ Stmt = (*SwitchStmt)(nil)
	_ Stmt = (*BreakStmt)(nil)
	_ Stmt = (*ContinueStmt)(nil)
	_ Stmt = (*AssignStmt)(nil)
	_ Stmt = (*CallStmt)(nil)
	_ Stmt = (*BlockStmt)(nil)
	_ Stmt = (*GotoStmt)(nil)
	_ Stmt = (*CallSubStmt)(nil)
	_ Stmt = (*ReturnStmt)(nil)
	_ Stmt = (*LabelDecl)(nil)
	_ Stmt = (*FuncDecl)(nil)

	_ Expr = (*IntLit)(nil)
	_ Expr = (*FloatLit)(nil)
	_ Expr = (*StrLit)(nil)
	_ Expr = (*IdentExpr)(nil)
	_ Expr = (*BinExpr)(nil)
	_ Expr = (*UnaryExpr)(nil)
	_ Expr = (*TernaryExpr)(nil)
	_ Expr = (*CallExpr)(nil)
	_ Expr = (*IndexExpr)(nil)
	_ Expr = (*ParenExpr)(nil)

	_ Node = (*File)(nil)
	_ Node = (*NPCHeader)(nil)
	_ Node = (*MenuOption)(nil)
	_ Node = (*SwitchCase)(nil)
)
