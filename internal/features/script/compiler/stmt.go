package compiler

import (
	"fmt"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

// compileStmt emits bytecode for a single statement.
//
// It delegates to category-specific helpers to keep the dispatch surface
// narrow (the script dialect has many statement kinds). Each helper does
// its own type-switch on a smaller subset.
func (c *Compiler) compileStmt(s script.Stmt) error {
	switch s := s.(type) {
	case *script.MesStmt,
		*script.NextStmt, *script.CloseStmt, *script.Close2Stmt, *script.EndStmt,
		*script.MenuStmt, *script.SelectStmt, *script.InputStmt:
		return c.compileSideEffectStmt(s)
	case *script.IfStmt, *script.WhileStmt, *script.DoWhileStmt, *script.ForStmt, *script.SwitchStmt:
		return c.compileControlFlow(s)
	case *script.AssignStmt, *script.CallStmt, *script.CallSubStmt,
		*script.BreakStmt, *script.ContinueStmt, *script.ReturnStmt,
		*script.BlockStmt:
		return c.compileMidStmt(s)
	case *script.GotoStmt:
		c.emitGoto(s.Label, s.Pos())
		return nil
	case *script.LabelDecl:
		c.emitLabel(s.Name)
		return nil
	case *script.FuncDecl:
		return c.compileFuncDecl(s)
	case *script.FuncRefStmt:
		// Forward/backward function reference: no-op at compile time.
		return nil
	default:
		return fmt.Errorf("compile error: unsupported statement %T", s)
	}
}

// compileSideEffectStmt handles statements that produce UI-side effects:
// message dialogs (mes, menu, select, input) and trivial no-arg calls
// (next, close, close2, end).
func (c *Compiler) compileSideEffectStmt(s script.Stmt) error {
	switch x := s.(type) {
	case *script.MesStmt:
		return c.compileMesStmt(x)
	case *script.NextStmt, *script.CloseStmt, *script.Close2Stmt, *script.EndStmt:
		c.compileSimpleStmt(x)
		return nil
	case *script.MenuStmt, *script.SelectStmt, *script.InputStmt:
		return c.compileDialogStmt(x)
	}
	return nil
}

// compileMidStmt handles value-producing / control-transfer statements
// that do not interact with UI: assignments, calls, jump-style flow
// control, and compound block statements.
func (c *Compiler) compileMidStmt(s script.Stmt) error {
	switch x := s.(type) {
	case *script.AssignStmt:
		return c.compileAssignStmt(x)
	case *script.CallStmt:
		return c.compileCallStmt(x)
	case *script.CallSubStmt:
		return c.compileCallSubStmt(x)
	case *script.BreakStmt:
		return c.compileBreakStmt(x)
	case *script.ContinueStmt:
		return c.compileContinueStmt(x)
	case *script.ReturnStmt:
		return c.compileReturnStmt(x)
	case *script.BlockStmt:
		return c.compileBlockStmt(x)
	}
	return nil
}

// compileSimpleStmt handles trivial statements that emit a single opcode.
func (c *Compiler) compileSimpleStmt(s script.Stmt) {
	switch x := s.(type) {
	case *script.NextStmt:
		c.emit(script.OpFunc, 0, "next")
	case *script.CloseStmt:
		c.emit(script.OpClose, 0, "")
	case *script.Close2Stmt:
		c.emit(script.OpFunc, 0, "close2")
	case *script.EndStmt:
		c.emit(script.OpEnd, 0, "")
	default:
		_ = x
	}
}

// compileControlFlow handles all looping and branching statements.
func (c *Compiler) compileControlFlow(s script.Stmt) error {
	switch x := s.(type) {
	case *script.IfStmt:
		return c.compileIfStmt(x)
	case *script.WhileStmt:
		return c.compileWhileStmt(x)
	case *script.DoWhileStmt:
		return c.compileDoWhileStmt(x)
	case *script.ForStmt:
		return c.compileForStmt(x)
	case *script.SwitchStmt:
		return c.compileSwitchStmt(x)
	default:
		return fmt.Errorf("compile error: unsupported control flow statement %T", s)
	}
}

// compileDialogStmt handles menu/select/input statements.
func (c *Compiler) compileDialogStmt(s script.Stmt) error {
	switch x := s.(type) {
	case *script.MenuStmt:
		return c.compileMenuStmt(x)
	case *script.SelectStmt:
		return c.compileSelectStmt(x)
	case *script.InputStmt:
		return c.compileInputStmt(x)
	default:
		return fmt.Errorf("compile error: unsupported dialog statement %T", s)
	}
}

func (c *Compiler) compileBlockStmt(b *script.BlockStmt) error {
	for _, s := range b.Body {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) compileMesStmt(m *script.MesStmt) error {
	if err := c.compileExpr(m.Msg); err != nil {
		return err
	}
	c.emit(script.OpFunc, 0, "mes")
	return nil
}

func (c *Compiler) compileMenuStmt(m *script.MenuStmt) error {
	for _, opt := range m.Options {
		if err := c.compileExpr(opt.Prompt); err != nil {
			return err
		}
		c.emit(script.OpName, 0, opt.Label)
	}
	c.emit(script.OpFunc, 0, "menu")
	return nil
}

func (c *Compiler) compileSelectStmt(m *script.SelectStmt) error {
	for _, opt := range m.Options {
		if err := c.compileExpr(opt.Prompt); err != nil {
			return err
		}
		c.emit(script.OpName, 0, opt.Label)
	}
	c.emit(script.OpFunc, 0, "select")
	return nil
}

func (c *Compiler) compileInputStmt(i *script.InputStmt) error {
	if err := c.compileExpr(i.Dst); err != nil {
		return err
	}
	c.emit(script.OpFunc, 0, "input")
	return nil
}

func (c *Compiler) compileCallStmt(cs *script.CallStmt) error {
	// Bare expression statement (e.g. `.@i++;` or `a == b;`) is parsed as
	// a call with an empty name. Evaluate the expression for side effects
	// but leave no persistent stack values behind.
	if cs.Name == "" && len(cs.Args) == 1 {
		if err := c.compileExpr(cs.Args[0]); err != nil {
			return err
		}
		return nil
	}

	// The `set` builtin is syntactic sugar for assignment: lower it to
	// OpAssign so that `set .@var, 42;` emits INT 42 ASSIGN .@var.
	if cs.Name == "set" && len(cs.Args) == 2 {
		if err := c.compileExpr(cs.Args[1]); err != nil {
			return err
		}
		name, ok := assignmentTargetName(cs.Args[0])
		if !ok {
			return fmt.Errorf("compile error: invalid set target %T", cs.Args[0])
		}
		c.emit(script.OpAssign, 0, name)
		return nil
	}

	for _, a := range cs.Args {
		if err := c.compileExpr(a); err != nil {
			return err
		}
	}
	c.emit(script.OpFunc, 0, cs.Name)
	return nil
}

func (c *Compiler) compileCallSubStmt(cs *script.CallSubStmt) error {
	for _, a := range cs.Args {
		if err := c.compileExpr(a); err != nil {
			return err
		}
	}
	c.emitWithPos(script.OpCallSub, 0, cs.Label, cs.Pos())
	return nil
}

func (c *Compiler) compileAssignStmt(a *script.AssignStmt) error {
	// Array-element assignment: the VM's OpIndexSet pops name (top),
	// idx, then value. Compile the RHS first (its value is at the
	// bottom of the stack), then push the index, then push the name,
	// then emit OpIndexSet. Only plain `=` is supported on array
	// elements in Phase R0 (S1); compound assignment to arrays is
	// not part of the rAthena-canonical NPC corpus.
	if ix, ident, isArrayAssign := arrayAssignTarget(a.Lhs, a.Op); isArrayAssign {
		if err := c.compileExpr(a.Rhs); err != nil {
			return err
		}
		if err := c.compileExpr(ix.Index); err != nil {
			return err
		}
		c.emit(script.OpStr, 0, ident.Name)
		c.emit(script.OpIndexSet, 0, "")
		return nil
	}

	if err := c.compileExpr(a.Rhs); err != nil {
		return err
	}

	name, ok := assignmentTargetName(a.Lhs)
	if !ok {
		return fmt.Errorf("compile error: invalid assignment target %T", a.Lhs)
	}

	if a.Op == "=" {
		c.emit(script.OpAssign, 0, name)
		return nil
	}

	op, ok := assignOpcode(a.Op)
	if !ok {
		return fmt.Errorf("compile error: unsupported assignment operator %q", a.Op)
	}
	c.emit(op, 0, name)
	return nil
}

// arrayAssignTarget returns (ix, ident, true) when the assignment LHS
// is a plain `=` to an IdentExpr-indexed array. Anything else falls
// through to the scalar assignment path.
func arrayAssignTarget(lhs script.Expr, op string) (*script.IndexExpr, *script.IdentExpr, bool) {
	if op != "=" {
		return nil, nil, false
	}
	ix, ok := lhs.(*script.IndexExpr)
	if !ok {
		return nil, nil, false
	}
	ident, ok := ix.Target.(*script.IdentExpr)
	if !ok {
		return nil, nil, false
	}
	return ix, ident, true
}

// assignmentTargetName extracts the variable/array name used as the target
// of an assignment.
func assignmentTargetName(e script.Expr) (string, bool) {
	switch x := e.(type) {
	case *script.IdentExpr:
		return x.Name, true
	case *script.IndexExpr:
		// For array assignments the VM will consume both the value and the
		// index that compileExpr pushes, so we only need the base name.
		if ident, ok := x.Target.(*script.IdentExpr); ok {
			return ident.Name, true
		}
	}
	return "", false
}

func (c *Compiler) compileIfStmt(i *script.IfStmt) error {
	elseLabel := c.freshLabel("if_else")
	endLabel := c.freshLabel("if_end")

	if err := c.compileExpr(i.Cond); err != nil {
		return err
	}

	if len(i.Else) == 0 {
		c.emitWithPos(script.OpGoto, 0, endLabel, i.Pos())
		c.pendingJumps = append(c.pendingJumps, pendingJump{instrIndex: len(c.instructions) - 1, label: endLabel})
	} else {
		c.emitWithPos(script.OpGoto, 0, elseLabel, i.Pos())
		c.pendingJumps = append(c.pendingJumps, pendingJump{instrIndex: len(c.instructions) - 1, label: elseLabel})
	}

	for _, s := range i.Then {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}

	if len(i.Else) > 0 {
		c.emitGoto(endLabel, i.Pos())
		c.emitLabel(elseLabel)
		for _, s := range i.Else {
			if err := c.compileStmt(s); err != nil {
				return err
			}
		}
	}

	c.emitLabel(endLabel)
	return nil
}

func (c *Compiler) compileWhileStmt(w *script.WhileStmt) error {
	start := len(c.instructions)
	startLabel := c.freshLabel("while_start")
	endLabel := c.pushBreakContext(start)
	c.emitLabel(startLabel)

	if err := c.compileExpr(w.Cond); err != nil {
		return err
	}
	c.emitWithPos(script.OpGoto, 0, endLabel, w.Pos())
	c.pendingJumps = append(c.pendingJumps, pendingJump{instrIndex: len(c.instructions) - 1, label: endLabel})

	for _, s := range w.Body {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}

	c.emitGoto(startLabel, w.Pos())

	c.emitLabel(endLabel)
	c.popBreakContext()
	return nil
}

func (c *Compiler) compileDoWhileStmt(d *script.DoWhileStmt) error {
	start := len(c.instructions)
	endLabel := c.pushBreakContext(start)
	bodyLabel := c.freshLabel("do_body")
	c.emitLabel(bodyLabel)

	for _, s := range d.Body {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}

	if err := c.compileExpr(d.Cond); err != nil {
		return err
	}
	c.emitGoto(bodyLabel, d.Pos())

	c.emitLabel(endLabel)
	c.popBreakContext()
	return nil
}

func (c *Compiler) compileForStmt(f *script.ForStmt) error {
	for _, s := range f.Init {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}

	start := len(c.instructions)
	startLabel := c.freshLabel("for_start")
	endLabel := c.pushBreakContext(start)
	c.emitLabel(startLabel)

	if f.Cond != nil {
		if err := c.compileExpr(f.Cond); err != nil {
			return err
		}
		c.emitWithPos(script.OpGoto, 0, endLabel, f.Pos())
		c.pendingJumps = append(c.pendingJumps, pendingJump{instrIndex: len(c.instructions) - 1, label: endLabel})
	}

	for _, s := range f.Body {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}

	for _, s := range f.Post {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}

	c.emitGoto(startLabel, f.Pos())

	c.emitLabel(endLabel)
	c.popBreakContext()
	return nil
}

func (c *Compiler) compileSwitchStmt(sw *script.SwitchStmt) error {
	endLabel := c.pushBreakContext(-1)

	for i, ca := range sw.Cases {
		if len(ca.Values) == 0 {
			// default case: always run.
			for _, s := range ca.Body {
				if err := c.compileStmt(s); err != nil {
					return err
				}
			}
			continue
		}

		nextLabel := c.freshLabel(fmt.Sprintf("switch_case_%d", i))
		for _, v := range ca.Values {
			if err := c.compileExpr(sw.Value); err != nil {
				return err
			}
			if err := c.compileExpr(v); err != nil {
				return err
			}
			c.emit(script.OpLEq, 0, "")
			c.emitWithPos(script.OpGoto, 0, nextLabel, ca.Pos())
			c.pendingJumps = append(c.pendingJumps, pendingJump{instrIndex: len(c.instructions) - 1, label: nextLabel})
		}

		for _, s := range ca.Body {
			if err := c.compileStmt(s); err != nil {
				return err
			}
		}
		c.emitGoto(endLabel, sw.Pos())
		c.emitLabel(nextLabel)
	}

	c.emitLabel(endLabel)
	c.popBreakContext()
	return nil
}

func (c *Compiler) compileBreakStmt(b *script.BreakStmt) error {
	ctx := c.currentBreakContext()
	if ctx == nil {
		return fmt.Errorf("compile error: break outside loop at %s", b.Pos())
	}
	c.emitGoto(ctx.breakLabel, b.Pos())
	return nil
}

func (c *Compiler) compileContinueStmt(ct *script.ContinueStmt) error {
	ctx := c.currentBreakContext()
	if ctx == nil || ctx.continueTarget < 0 {
		return fmt.Errorf("compile error: continue outside loop at %s", ct.Pos())
	}
	c.emitWithPos(script.OpGoto, safeInt32(ctx.continueTarget), "", ct.Pos())
	return nil
}

func (c *Compiler) compileReturnStmt(r *script.ReturnStmt) error {
	if r.Value != nil {
		if err := c.compileExpr(r.Value); err != nil {
			return err
		}
	}
	c.emit(script.OpReturn, 0, "")
	return nil
}

func (c *Compiler) compileFuncDecl(f *script.FuncDecl) error {
	// Function declarations are compiled into separate scripts and stored
	// in the caller's function map. For a single CompiledScript they are
	// just a named label entry.
	c.emitLabel(f.Name)
	for _, s := range f.Body {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	c.emit(script.OpReturn, 0, "")
	return nil
}
