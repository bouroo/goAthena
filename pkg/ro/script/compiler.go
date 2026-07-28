package script

import (
	"fmt"
	"strconv"
)

// Compile is the top-level entry: parse src, compile every block, and return a
// CompiledScriptSet keyed by NPC name with warps/shops extracted from headers.
// Floating `function script` blocks land in Funcs; placed/floating NPC blocks
// land in Scripts under the NPC name.
func Compile(src []byte) (*CompiledScriptSet, error) {
	files, err := Parse(src)
	if err != nil {
		return nil, err
	}
	set := NewCompiledScriptSet()
	c := newCompiler()
	for _, f := range files {
		if err := c.compileFile(f, set); err != nil {
			return nil, err
		}
	}
	return set, nil
}

// compiler holds per-compilation state: the CompiledScript being built, a
// unique label counter for synthesized jump targets, and a stack of enclosing
// loops for break/continue resolution.
type compiler struct {
	cs    *CompiledScript
	label int
	loops []loopFrame
}

// loopFrame records the innermost loop's continue (cond test) and break (exit)
// jump labels so `break`/`continue` compile to the right target.
type loopFrame struct{ cont, brk string }

func newCompiler() *compiler { return &compiler{} }

// compileFile compiles one *File into the set. function-script headers route
// into Funcs; warp headers extract a WarpDef (warp NPCs have no dialog body);
// everything else (placed/floating NPCs) becomes an executable script.
func (c *compiler) compileFile(f *File, set *CompiledScriptSet) error {
	hdr := f.Header()
	if hdr == nil {
		return nil
	}
	if hdr.Type == "warp" {
		set.Warps = append(set.Warps, WarpDef{
			MapName: hdr.MapName, X: hdr.X, Y: hdr.Y,
			TriggerX: hdr.TriggerX, TriggerY: hdr.TriggerY,
		})
		return nil
	}
	if hdr.Type == "func" {
		c.cs = NewCompiledScript(hdr.Name)
		if err := c.compileBlockStmts(f.Body); err != nil {
			return err
		}
		c.emitOp(OpEOF, hdr.pos)
		set.Funcs[hdr.Name] = c.cs
		return nil
	}
	c.cs = NewCompiledScript(hdr.Name)
	if err := c.compileBlockStmts(f.Body); err != nil {
		return err
	}
	c.emitOp(OpEOF, hdr.pos)
	if _, dup := set.Scripts[hdr.Name]; !dup {
		set.Scripts[hdr.Name] = c.cs
	}
	return nil
}

// emit appends a fully-formed instruction and returns its index.
func (c *compiler) emit(ins Instruction) int {
	idx := len(c.cs.Instructions)
	c.cs.Instructions = append(c.cs.Instructions, ins)
	return idx
}

func (c *compiler) emitOp(op Opcode, pos Position) int {
	return c.emit(Instruction{Op: op, Pos: pos})
}

// emitJump emits a jump to a fresh, not-yet-placed label and returns the label
// name. The destination is bound later by markLabel. This is single-pass
// backpatching by name: jumps carry the label name in Str and the VM resolves
// it through CompiledScript.Labels at run time.
func (c *compiler) emitJump(op Opcode, pos Position) string {
	name := c.freshLabel()
	c.emit(Instruction{Op: op, Str: name, Pos: pos})
	return name
}

// markLabel binds name to the current instruction position and emits an OpLabel
// marker (a VM no-op, kept for disassembly). All prior jumps to name now land
// here through the labels map.
func (c *compiler) markLabel(name string, pos Position) {
	c.cs.Labels[name] = len(c.cs.Instructions)
	c.emitOp(OpLabel, pos)
}

// freshLabel returns a unique compiler-synthesized label name, prefixed `__c`
// so it never collides with a user-declared `L_Name:`.
func (c *compiler) freshLabel() string {
	name := "__c" + strconv.Itoa(c.label)
	c.label++
	return name
}

func (c *compiler) compileBlockStmts(stmts []Stmt) error {
	for _, s := range stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	return nil
}

// compileStmt dispatches on the AST node's concrete type. The switch is
// exhaustive over the dialog + control subset; an unhandled node returns a
// typed error rather than being silently skipped.
func (c *compiler) compileStmt(s Stmt) error { //nolint:gocyclo
	switch n := s.(type) {
	// Dialog builtins (mes/next/close/close2/end) are produced by the parser as
	// *CallStmt and resolved by name at runtime through the builtin map via
	// compileCall — no dedicated statement node is needed. `set` is the one
	// command that diverges: it desugars to an assignment (see compileSet).
	case *AssignStmt:
		return c.compileAssign(n)
	case *CallStmt:
		if n.Name == "set" { //nolint:goconst
			return c.compileSet(n)
		}
		return c.compileCall(n.Name, n.Args, n.pos)
	case *IfStmt:
		return c.compileIf(n)
	case *WhileStmt:
		return c.compileWhile(n)
	case *DoWhileStmt:
		return c.compileDoWhile(n)
	case *ForStmt:
		return c.compileFor(n)
	case *ReturnStmt:
		c.emitOp(OpReturn, n.pos)
		return nil
	case *BreakStmt:
		return c.compileBreak(n.pos)
	case *ContinueStmt:
		return c.compileContinue(n.pos)
	case *GotoStmt:
		c.emit(Instruction{Op: OpGoto, Str: n.Label, Pos: n.pos})
		return nil
	case *LabelDecl:
		c.markLabel(n.Name, n.pos)
		return nil
	case *CallSubStmt:
		for _, a := range n.Args {
			if err := c.compileExpr(a); err != nil {
				return err
			}
		}
		c.emit(Instruction{Op: OpCallSub, Str: n.Label, Pos: n.pos})
		return nil
	}
	return fmt.Errorf("script: unsupported statement %T at %s", s, s.Pos())
}

// compileCall evaluates each argument left-to-right onto the stack, then emits
// the OpFunc invocation carrying its arity in Operand.
func (c *compiler) compileCall(name string, args []Expr, pos Position) error {
	for _, a := range args {
		if err := c.compileExpr(a); err != nil {
			return err
		}
	}
	c.emit(Instruction{Op: OpFunc, Str: name, Operand: int32(len(args)), Pos: pos}) //nolint:gosec // G115: bounded by parse (args < 256)
	return nil
}

// compileSet desugars `set var, val;` into a direct assignment. The command
// form must NOT flow through compileCall: that would push var's *value* (via
// OpVar) as an argument, losing the name the store needs. Lowering to an
// AssignStmt makes the first argument the store target, matching rAthena's
// `set` semantics.
func (c *compiler) compileSet(n *CallStmt) error {
	if len(n.Args) != 2 {
		return fmt.Errorf("script: set expects 2 args, got %d at %s", len(n.Args), n.pos)
	}
	return c.compileAssign(&AssignStmt{Lhs: n.Args[0], Op: "=", Rhs: n.Args[1], pos: n.pos})
}

// compileAssign lowers `lhs = rhs` and compound forms. `=` evaluates rhs then
// stores via OpAssign; compound ops read-modify-write through the variable's
// current value. Index targets (`arr[i] = v`) route through OpIndexSet.
func (c *compiler) compileAssign(n *AssignStmt) error {
	if idx, ok := n.Lhs.(*IndexExpr); ok {
		if err := c.compileExpr(idx.Target); err != nil {
			return err
		}
		if err := c.compileExpr(idx.Index); err != nil {
			return err
		}
		if err := c.compileExpr(n.Rhs); err != nil {
			return err
		}
		c.emitOp(OpIndexSet, n.pos)
		return nil
	}
	name, ok := varName(n.Lhs)
	if !ok {
		return fmt.Errorf("script: invalid assignment target at %s", n.Lhs.Pos())
	}
	if n.Op == "=" {
		if err := c.compileExpr(n.Rhs); err != nil {
			return err
		}
		c.emit(Instruction{Op: OpAssign, Str: name, Pos: n.pos})
		return nil
	}
	c.emit(Instruction{Op: OpVar, Str: name, Pos: n.pos})
	if err := c.compileExpr(n.Rhs); err != nil {
		return err
	}
	op, err := compoundOp(n.Op)
	if err != nil {
		return fmt.Errorf("script: %w at %s", err, n.pos)
	}
	c.emitOp(op, n.pos)
	c.emit(Instruction{Op: OpAssign, Str: name, Pos: n.pos})
	return nil
}

// compoundOps maps each compound-assignment operator to the arithmetic opcode
// applied between the current value and the right-hand side.
//
//nolint:goconst // operator tokens read clearest as literals in this table
var compoundOps = map[string]Opcode{
	"+=":  OpAdd,
	"-=":  OpSub,
	"*=":  OpMul,
	"/=":  OpDiv,
	"%=":  OpMod,
	"&=":  OpAnd,
	"|=":  OpOr,
	"^=":  OpXor,
	"<<=": OpShiftL,
	">>=": OpShiftR,
}

func compoundOp(op string) (Opcode, error) {
	if code, ok := compoundOps[op]; ok {
		return code, nil
	}
	return 0, fmt.Errorf("unsupported compound assignment %q", op)
}

// compileIf emits:
//
//	<cond>            // leaves truthy value on stack
//	JMPF L_else       // pop; skip then when false
//	<then>
//	[JMP L_end]       // only when an else-branch exists
//	L_else:
//	[<else>]
//	[L_end:]
func (c *compiler) compileIf(n *IfStmt) error {
	if err := c.compileExpr(n.Cond); err != nil {
		return err
	}
	elseLabel := c.emitJump(OpJumpIfFalse, n.pos)
	if err := c.compileBlockStmts(n.Then); err != nil {
		return err
	}
	if len(n.Else) > 0 {
		endLabel := c.emitJump(OpGoto, n.pos)
		c.markLabel(elseLabel, n.pos)
		if err := c.compileBlockStmts(n.Else); err != nil {
			return err
		}
		c.markLabel(endLabel, n.pos)
	} else {
		c.markLabel(elseLabel, n.pos)
	}
	return nil
}

// compileWhile emits the back-edge loop:
//
//	L_start:
//	  <cond>
//	  JMPF L_end       // pop; exit when false
//	  <body>
//	  JMP L_start
//	L_end:
func (c *compiler) compileWhile(n *WhileStmt) error {
	startLabel := c.freshLabel()
	endLabel := c.freshLabel()
	c.markLabel(startLabel, n.pos)
	if err := c.compileExpr(n.Cond); err != nil {
		return err
	}
	c.emit(Instruction{Op: OpJumpIfFalse, Str: endLabel, Pos: n.pos})
	c.pushLoop(startLabel, endLabel)
	if err := c.compileBlockStmts(n.Body); err != nil {
		return err
	}
	c.popLoop()
	c.emit(Instruction{Op: OpGoto, Str: startLabel, Pos: n.pos})
	c.markLabel(endLabel, n.pos)
	return nil
}

// compileDoWhile tests the condition after the body:
//
//	L_start:
//	  <body>
//	L_cond:
//	  <cond>
//	  JMPT L_start     // pop; loop while true
//	L_end:
func (c *compiler) compileDoWhile(n *DoWhileStmt) error {
	startLabel := c.freshLabel()
	condLabel := c.freshLabel()
	endLabel := c.freshLabel()
	c.markLabel(startLabel, n.pos)
	c.pushLoop(condLabel, endLabel) // break→exit; continue→cond re-check
	if err := c.compileBlockStmts(n.Body); err != nil {
		return err
	}
	c.popLoop()
	c.markLabel(condLabel, n.pos)
	if err := c.compileExpr(n.Cond); err != nil {
		return err
	}
	c.emit(Instruction{Op: OpJumpIfTrue, Str: startLabel, Pos: n.pos})
	c.markLabel(endLabel, n.pos)
	return nil
}

// compileFor is for-as-sugar: emit init, loop while cond, run body + post.
func (c *compiler) compileFor(n *ForStmt) error {
	if err := c.compileBlockStmts(n.Init); err != nil {
		return err
	}
	startLabel := c.freshLabel()
	endLabel := c.freshLabel()
	c.markLabel(startLabel, n.pos)
	if n.Cond != nil {
		if err := c.compileExpr(n.Cond); err != nil {
			return err
		}
		c.emit(Instruction{Op: OpJumpIfFalse, Str: endLabel, Pos: n.pos})
	}
	c.pushLoop(startLabel, endLabel)
	if err := c.compileBlockStmts(n.Body); err != nil {
		return err
	}
	c.popLoop()
	if err := c.compileBlockStmts(n.Post); err != nil {
		return err
	}
	c.emit(Instruction{Op: OpGoto, Str: startLabel, Pos: n.pos})
	c.markLabel(endLabel, n.pos)
	return nil
}

func (c *compiler) pushLoop(cont, brk string) {
	c.loops = append(c.loops, loopFrame{cont: cont, brk: brk})
}

func (c *compiler) popLoop() {
	if len(c.loops) == 0 {
		return
	}
	c.loops = c.loops[:len(c.loops)-1]
}

func (c *compiler) currentLoop() (loopFrame, bool) {
	if len(c.loops) == 0 {
		return loopFrame{}, false
	}
	return c.loops[len(c.loops)-1], true
}

func (c *compiler) compileBreak(pos Position) error {
	fr, ok := c.currentLoop()
	if !ok {
		return fmt.Errorf("script: break outside loop at %s", pos)
	}
	c.emit(Instruction{Op: OpGoto, Str: fr.brk, Pos: pos})
	return nil
}

func (c *compiler) compileContinue(pos Position) error {
	fr, ok := c.currentLoop()
	if !ok {
		return fmt.Errorf("script: continue outside loop at %s", pos)
	}
	c.emit(Instruction{Op: OpGoto, Str: fr.cont, Pos: pos})
	return nil
}

// compileExpr evaluates an expression, leaving one value on the stack.
func (c *compiler) compileExpr(e Expr) error {
	switch n := e.(type) {
	case *IntLit:
		c.emit(Instruction{Op: OpInt, Operand: int32(n.Value), Pos: n.Pos()}) //nolint:gosec // G115: script ints are small (int64 literal fits int32)
		return nil
	case *FloatLit:
		// rAthena script is int-dominant; the dialog subset coerces float
		// literals to int at compile time. A float cell can be added later if
		// a script exercises it.
		c.emit(Instruction{Op: OpInt, Operand: int32(n.Value), Pos: n.Pos()}) //nolint:gosec // G115: script ints are small (int64 literal fits int32)
		return nil
	case *StrLit:
		c.emit(Instruction{Op: OpStr, Str: n.Value, Pos: n.Pos()})
		return nil
	case *IdentExpr:
		c.emit(Instruction{Op: OpVar, Str: n.Name, Pos: n.Pos()})
		return nil
	case *ParenExpr:
		return c.compileExpr(n.Inner)
	case *UnaryExpr:
		return c.compileUnary(n)
	case *BinExpr:
		return c.compileBinExpr(n)
	case *TernaryExpr:
		return c.compileTernary(n)
	case *CallExpr:
		return c.compileCall(n.Name, n.Args, n.Pos())
	case *IndexExpr:
		return c.compileIndexGet(n)
	}
	return fmt.Errorf("script: unsupported expression %T at %s", e, e.Pos())
}

// compileBinExpr short-circuits && and ||; all other operators evaluate both
// sides then emit the matching opcode. The jump ops consume the tested value,
// so each branch leaves exactly one result value on the stack.
func (c *compiler) compileBinExpr(n *BinExpr) error {
	switch n.Op {
	case "&&": //nolint:goconst
		// a truthy → result is b; a falsy → JMPF pops a and lands at skip,
		// where we push 0. Both branches leave one value.
		if err := c.compileExpr(n.Lhs); err != nil {
			return err
		}
		skip := c.emitJump(OpJumpIfFalse, n.Pos())
		if err := c.compileExpr(n.Rhs); err != nil {
			return err
		}
		end := c.emitJump(OpGoto, n.Pos())
		c.markLabel(skip, n.Pos())
		c.emit(Instruction{Op: OpInt, Operand: 0, Pos: n.Pos()})
		c.markLabel(end, n.Pos())
		return nil
	case "||": //nolint:goconst
		// a falsy → result is b; a truthy → JMPT pops a and lands at skip,
		// where we push 1.
		if err := c.compileExpr(n.Lhs); err != nil {
			return err
		}
		skip := c.emitJump(OpJumpIfTrue, n.Pos())
		if err := c.compileExpr(n.Rhs); err != nil {
			return err
		}
		end := c.emitJump(OpGoto, n.Pos())
		c.markLabel(skip, n.Pos())
		c.emit(Instruction{Op: OpInt, Operand: 1, Pos: n.Pos()})
		c.markLabel(end, n.Pos())
		return nil
	}
	if err := c.compileExpr(n.Lhs); err != nil {
		return err
	}
	if err := c.compileExpr(n.Rhs); err != nil {
		return err
	}
	op, err := binOp(n.Op)
	if err != nil {
		return fmt.Errorf("script: %w at %s", err, n.Pos())
	}
	c.emitOp(op, n.Pos())
	return nil
}

// compileTernary lowers `c ? t : e` to jumps, leaving t or e on the stack.
func (c *compiler) compileTernary(n *TernaryExpr) error {
	if err := c.compileExpr(n.Cond); err != nil {
		return err
	}
	elseLabel := c.emitJump(OpJumpIfFalse, n.Pos())
	if err := c.compileExpr(n.Then); err != nil {
		return err
	}
	endLabel := c.emitJump(OpGoto, n.Pos())
	c.markLabel(elseLabel, n.Pos())
	if err := c.compileExpr(n.Else); err != nil {
		return err
	}
	c.markLabel(endLabel, n.Pos())
	return nil
}

func (c *compiler) compileUnary(n *UnaryExpr) error {
	if err := c.compileExpr(n.Operand); err != nil {
		return err
	}
	switch n.Op {
	case "-":
		c.emitOp(OpNeg, n.Pos())
	case "!":
		c.emitOp(OpNot, n.Pos())
	case "~":
		c.emitOp(OpBNot, n.Pos())
	default:
		return fmt.Errorf("script: unsupported unary %q at %s", n.Op, n.Pos())
	}
	return nil
}

func (c *compiler) compileIndexGet(n *IndexExpr) error {
	if err := c.compileExpr(n.Target); err != nil {
		return err
	}
	if err := c.compileExpr(n.Index); err != nil {
		return err
	}
	c.emitOp(OpIndexGet, n.Pos())
	return nil
}

// binOps maps each infix operator string to the opcode that evaluates it.
//
//nolint:goconst // operator tokens read clearest as literals in this table
var binOps = map[string]Opcode{
	"+":  OpAdd,
	"-":  OpSub,
	"*":  OpMul,
	"/":  OpDiv,
	"%":  OpMod,
	"==": OpLEq,
	"!=": OpLNe,
	"<":  OpLLT,
	">":  OpLGT,
	"<=": OpLLE,
	">=": OpLGE,
	"&":  OpAnd,
	"|":  OpOr,
	"^":  OpXor,
	"<<": OpShiftL,
	">>": OpShiftR,
}

func binOp(op string) (Opcode, error) {
	if code, ok := binOps[op]; ok {
		return code, nil
	}
	return 0, fmt.Errorf("unsupported binary operator %q", op)
}

// varName extracts the bare variable name from an assignment target. Only a
// plain identifier is accepted; array targets take the OpIndexSet path.
func varName(e Expr) (string, bool) {
	if id, ok := e.(*IdentExpr); ok {
		return id.Name, true
	}
	return "", false
}
