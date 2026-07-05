package script

// Constructors for AST nodes. The struct fields are unexported to
// discourage downstream mutation of source positions after the parser
// has set them, but the parser lives in a different package and needs
// a way to build nodes. These constructors are the single sanctioned
// build path.

// NewFile builds a File with the given header and body. Header may be
// nil for floating function-script definitions.
func NewFile(header *NPCHeader, body []Stmt) *File {
	return &File{header: header, Body: body}
}

// Header returns the file's NPC header (or nil if floating).
func (f *File) Header() *NPCHeader { return f.header }

// ----- Statement constructors -----

// NewMesStmt builds a message statement (`mes "..."`).
func NewMesStmt(msg Expr, pos Position) *MesStmt {
	return &MesStmt{Msg: msg, pos: pos}
}

// NewNextStmt builds a `next;` statement.
func NewNextStmt(pos Position) *NextStmt { return &NextStmt{pos: pos} }

// NewCloseStmt builds a `close;` statement.
func NewCloseStmt(pos Position) *CloseStmt { return &CloseStmt{pos: pos} }

// NewClose2Stmt builds a `close2;` statement.
func NewClose2Stmt(pos Position) *Close2Stmt { return &Close2Stmt{pos: pos} }

// NewEndStmt builds an `end;` statement.
func NewEndStmt(pos Position) *EndStmt { return &EndStmt{pos: pos} }

// NewMenuStmt builds a `menu(...);` statement.
func NewMenuStmt(opts []MenuOption, pos Position) *MenuStmt {
	return &MenuStmt{Options: opts, pos: pos}
}

// NewMenuOption builds one prompt/label pair for menu/select.
func NewMenuOption(prompt Expr, label string, pos Position) MenuOption {
	return MenuOption{Prompt: prompt, Label: label, pos: pos}
}

// NewSelectStmt builds a `select(...);` statement.
func NewSelectStmt(opts []MenuOption, pos Position) *SelectStmt {
	return &SelectStmt{Options: opts, pos: pos}
}

// NewInputStmt builds an `input var;` statement.
func NewInputStmt(dst Expr, pos Position) *InputStmt { return &InputStmt{Dst: dst, pos: pos} }

// NewIfStmt builds an `if (cond) { ... } else { ... }` statement.
func NewIfStmt(cond Expr, then, els []Stmt, pos Position) *IfStmt {
	return &IfStmt{Cond: cond, Then: then, Else: els, pos: pos}
}

// NewWhileStmt builds a `while (cond) { ... }` statement.
func NewWhileStmt(cond Expr, body []Stmt, pos Position) *WhileStmt {
	return &WhileStmt{Cond: cond, Body: body, pos: pos}
}

// NewDoWhileStmt builds a `do { ... } while (cond);` statement.
func NewDoWhileStmt(body []Stmt, cond Expr, pos Position) *DoWhileStmt {
	return &DoWhileStmt{Body: body, Cond: cond, pos: pos}
}

// NewForStmt builds a `for (init; cond; post) { ... }` statement.
func NewForStmt(init []Stmt, cond Expr, post []Stmt, body []Stmt, pos Position) *ForStmt {
	return &ForStmt{Init: init, Cond: cond, Post: post, Body: body, pos: pos}
}

// NewSwitchStmt builds a `switch (val) { ... }` statement.
func NewSwitchStmt(val Expr, cases []SwitchCase, pos Position) *SwitchStmt {
	return &SwitchStmt{Value: val, Cases: cases, pos: pos}
}

// NewSwitchCase builds one case/default arm of a switch.
func NewSwitchCase(values []Expr, body []Stmt, pos Position) SwitchCase {
	return SwitchCase{Values: values, Body: body, pos: pos}
}

// NewBreakStmt builds a `break;` statement.
func NewBreakStmt(pos Position) *BreakStmt { return &BreakStmt{pos: pos} }

// NewContinueStmt builds a `continue;` statement.
func NewContinueStmt(pos Position) *ContinueStmt { return &ContinueStmt{pos: pos} }

// NewAssignStmt builds an assignment statement (`lhs = rhs` or `lhs op= rhs`).
func NewAssignStmt(lhs Expr, op string, rhs Expr, pos Position) *AssignStmt {
	return &AssignStmt{Lhs: lhs, Op: op, Rhs: rhs, pos: pos}
}

// NewCallStmt builds a builtin call statement.
func NewCallStmt(name string, args []Expr, pos Position) *CallStmt {
	return &CallStmt{Name: name, Args: args, pos: pos}
}

// NewGotoStmt builds a `goto Label;` statement.
func NewGotoStmt(label string, pos Position) *GotoStmt { return &GotoStmt{Label: label, pos: pos} }

// NewCallSubStmt builds a `callsub Label, args...;` statement.
func NewCallSubStmt(label string, args []Expr, pos Position) *CallSubStmt {
	return &CallSubStmt{Label: label, Args: args, pos: pos}
}

// NewReturnStmt builds a `return [value];` statement.
func NewReturnStmt(value Expr, pos Position) *ReturnStmt { return &ReturnStmt{Value: value, pos: pos} }

// NewLabelDecl builds a label declaration (`L_Name:`).
func NewLabelDecl(name string, pos Position) *LabelDecl { return &LabelDecl{Name: name, pos: pos} }

// NewFuncDecl builds a `function script Name { ... }` declaration.
func NewFuncDecl(name string, body []Stmt, pos Position) *FuncDecl {
	return &FuncDecl{Name: name, Body: body, pos: pos}
}

// NewFuncRefStmt builds a `function Name;` reference statement.
func NewFuncRefStmt(name string, pos Position) *FuncRefStmt {
	return &FuncRefStmt{Name: name, pos: pos}
}

// ----- Expression constructors -----

// NewIntLit builds an integer literal expression.
func NewIntLit(v int64, pos Position) *IntLit { return &IntLit{Value: v, pos: pos} }

// NewFloatLit builds a floating-point literal expression.
func NewFloatLit(v float64, pos Position) *FloatLit { return &FloatLit{Value: v, pos: pos} }

// NewStrLit builds a string literal expression.
func NewStrLit(v string, pos Position) *StrLit { return &StrLit{Value: v, pos: pos} }

// NewIdentExpr builds an identifier/variable reference expression.
func NewIdentExpr(name string, pos Position) *IdentExpr { return &IdentExpr{Name: name, pos: pos} }

// NewBinExpr builds a binary operation expression.
func NewBinExpr(op string, lhs, rhs Expr, pos Position) *BinExpr {
	return &BinExpr{Op: op, Lhs: lhs, Rhs: rhs, pos: pos}
}

// NewUnaryExpr builds a prefix or postfix unary expression.
func NewUnaryExpr(op string, operand Expr, pos Position) *UnaryExpr {
	return &UnaryExpr{Op: op, Operand: operand, pos: pos}
}

// NewTernaryExpr builds a conditional expression (`cond ? then : else`).
func NewTernaryExpr(cond, then, els Expr, pos Position) *TernaryExpr {
	return &TernaryExpr{Cond: cond, Then: then, Else: els, pos: pos}
}

// NewCallExpr builds a function call expression.
func NewCallExpr(name string, args []Expr, pos Position) *CallExpr {
	return &CallExpr{Name: name, Args: args, pos: pos}
}

// NewIndexExpr builds an array index expression (`target[index]`).
func NewIndexExpr(target, index Expr, pos Position) *IndexExpr {
	return &IndexExpr{Target: target, Index: index, pos: pos}
}

// NewParenExpr builds a parenthesized expression (`(expr)`).
func NewParenExpr(inner Expr, pos Position) *ParenExpr { return &ParenExpr{Inner: inner, pos: pos} }

// NewNPCHeader builds an NPCHeader at the given source position.
func NewNPCHeader(mapName string, x, y, facing int, name, spriteName string,
	spriteID, triggerX, triggerY int, typ string, pos Position,
) *NPCHeader {
	return &NPCHeader{
		MapName: mapName, X: x, Y: y, Facing: facing,
		Name: name, SpriteName: spriteName,
		SpriteID: spriteID, TriggerX: triggerX, TriggerY: triggerY,
		Type: typ, pos: pos,
	}
}
