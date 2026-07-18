package compiler

import (
	"fmt"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

// compileExpr emits the bytecode needed to evaluate an expression and push
// its result onto the VM stack.
func (c *Compiler) compileExpr(e script.Expr) error {
	switch x := e.(type) {
	case *script.IntLit:
		c.emit(script.OpInt, safeInt32(int(x.Value)), "")
	case *script.StrLit:
		c.emit(script.OpStr, 0, x.Value)
	case *script.IdentExpr:
		c.emit(script.OpVar, 0, x.Name)
	case *script.BinExpr:
		return c.compileBinExpr(x)
	case *script.UnaryExpr:
		return c.compileUnaryExpr(x)
	case *script.TernaryExpr:
		return c.compileTernaryExpr(x)
	case *script.CallExpr:
		return c.compileCallExpr(x)
	case *script.IndexExpr:
		return c.compileIndexExpr(x)
	case *script.ParenExpr:
		return c.compileExpr(x.Inner)
	case *script.FloatLit:
		// Floats are rare; represent as an int cast for now so the VM
		// still has a single numeric stack cell.
		c.emit(script.OpInt, int32(x.Value), "")
	default:
		return fmt.Errorf("compile error: unsupported expression %T", e)
	}
	return nil
}

const (
	opIncrement = "++"
	opPreInc    = "pre++"
	opPostInc   = "post++"
	opPreDec    = "pre--"
	opPostDec   = "post--"
	opDecrement = "--"
)

func (c *Compiler) compileBinExpr(e *script.BinExpr) error {
	// Short-circuit logical operators need special control-flow handling.
	switch e.Op {
	case "&&":
		return c.compileLAnd(e)
	case "||":
		return c.compileLOr(e)
	}

	if err := c.compileExpr(e.Lhs); err != nil {
		return err
	}
	if err := c.compileExpr(e.Rhs); err != nil {
		return err
	}

	op, ok := binaryOpcode(e.Op)
	if !ok {
		return fmt.Errorf("compile error: unsupported binary operator %q", e.Op)
	}
	c.emit(op, 0, "")
	return nil
}

func (c *Compiler) compileLAnd(e *script.BinExpr) error {
	if err := c.compileExpr(e.Lhs); err != nil {
		return err
	}
	if err := c.compileExpr(e.Rhs); err != nil {
		return err
	}
	c.emit(script.OpLAnd, 0, "")
	return nil
}

func (c *Compiler) compileLOr(e *script.BinExpr) error {
	if err := c.compileExpr(e.Lhs); err != nil {
		return err
	}
	if err := c.compileExpr(e.Rhs); err != nil {
		return err
	}
	c.emit(script.OpLOr, 0, "")
	return nil
}

func (c *Compiler) compileUnaryExpr(e *script.UnaryExpr) error {
	// Increment/decrement are lowered to load/add-or-sub/store so that the
	// VM does not need a dedicated opcode. Both pre- and post-forms store
	// the new value; the stack result is the new value (pre semantics).
	switch e.Op {
	case opIncrement, opPreInc, opPostInc, opDecrement, opPreDec, opPostDec:
		name, ok := assignmentTargetName(e.Operand)
		if !ok {
			return fmt.Errorf("compile error: invalid increment/decrement target %T", e.Operand)
		}
		c.emit(script.OpVar, 0, name)
		c.emit(script.OpInt, 1, "")
		if e.Op == opDecrement || e.Op == opPreDec || e.Op == opPostDec {
			c.emit(script.OpSub, 0, "")
		} else {
			c.emit(script.OpAdd, 0, "")
		}
		c.emit(script.OpAssign, 0, name)
		return nil
	}

	if err := c.compileExpr(e.Operand); err != nil {
		return err
	}
	op, ok := unaryOpcode(e.Op)
	if !ok {
		return fmt.Errorf("compile error: unsupported unary operator %q", e.Op)
	}
	c.emit(op, 0, "")
	return nil
}

func (c *Compiler) compileTernaryExpr(e *script.TernaryExpr) error {
	elseLabel := c.freshLabel("tern_else")
	endLabel := c.freshLabel("tern_end")

	if err := c.compileExpr(e.Cond); err != nil {
		return err
	}
	c.emitWithPos(script.OpGoto, 0, elseLabel, e.Pos())
	c.pendingJumps = append(c.pendingJumps, pendingJump{instrIndex: len(c.instructions) - 1, label: elseLabel})

	if err := c.compileExpr(e.Then); err != nil {
		return err
	}
	c.emitGoto(endLabel, e.Pos())

	c.emitLabel(elseLabel)
	if err := c.compileExpr(e.Else); err != nil {
		return err
	}
	c.emitLabel(endLabel)
	return nil
}

func (c *Compiler) compileCallExpr(e *script.CallExpr) error {
	for _, a := range e.Args {
		if err := c.compileExpr(a); err != nil {
			return err
		}
	}
	c.emit(script.OpFunc, 0, e.Name)
	return nil
}

func (c *Compiler) compileIndexExpr(e *script.IndexExpr) error {
	// Phase R0 (S1) array reads: when the array base is a simple
	// IdentExpr, emit the array name as a string literal, compile
	// the index, then emit OpBinary — which the VM interprets as
	// the array-read opcode (pops name, pops idx, pushes the
	// stored element). For non-identifier targets (nested array
	// expressions, computed bases, etc.) fall back to the old
	// target-on-stack behavior, which is a no-op on the VM today.
	if ident, ok := e.Target.(*script.IdentExpr); ok {
		c.emit(script.OpStr, 0, ident.Name)
		if err := c.compileExpr(e.Index); err != nil {
			return err
		}
		c.emit(script.OpBinary, 0, "")
		return nil
	}
	// TODO(phase-r0-script-vm): handle non-IdentExpr IndexExpr targets
	// (e.g. nested indexing, computed bases). For now emit the old
	// target-then-index pattern, which leaves the operands on the
	// stack and matches the previous no-op VM behavior.
	if err := c.compileExpr(e.Target); err != nil {
		return err
	}
	if err := c.compileExpr(e.Index); err != nil {
		return err
	}
	c.emit(script.OpBinary, 0, "")
	return nil
}

// unaryOpcode maps source unary operators to VM opcodes.
func unaryOpcode(op string) (script.Opcode, bool) {
	switch op {
	case "!", "!!": // `!!` is not valid but tolerate it as logical not.
		return script.OpNot, true
	case "-", "neg":
		return script.OpNeg, true
	case "~", "bnot":
		return script.OpBNot, true
	case opIncrement, opPreInc, opPostInc, "add++":
		return script.OpExpr, true
	case opDecrement, opPreDec, opPostDec, "sub--":
		return script.OpExpr, true
	}
	return 0, false
}

// binaryOpcode maps source binary operators to VM opcodes.
func binaryOpcode(op string) (script.Opcode, bool) {
	if op, ok := binaryArithmetic(op); ok {
		return op, true
	}
	if op, ok := binaryComparison(op); ok {
		return op, true
	}
	if op, ok := binaryLogical(op); ok {
		return op, true
	}
	if op, ok := binaryBitwise(op); ok {
		return op, true
	}
	return 0, false
}

func binaryArithmetic(op string) (script.Opcode, bool) {
	switch op {
	case "+":
		return script.OpAdd, true
	case "-":
		return script.OpSub, true
	case "*":
		return script.OpMul, true
	case "/":
		return script.OpDiv, true
	case "%":
		return script.OpMod, true
	}
	return 0, false
}

func binaryComparison(op string) (script.Opcode, bool) {
	switch op {
	case "==":
		return script.OpLEq, true
	case "!=":
		return script.OpLNe, true
	case "<":
		return script.OpLLT, true
	case ">":
		return script.OpLGT, true
	case "<=":
		return script.OpLLE, true
	case ">=":
		return script.OpLGE, true
	}
	return 0, false
}

func binaryLogical(op string) (script.Opcode, bool) {
	switch op {
	case "&&":
		return script.OpLAnd, true
	case "||":
		return script.OpLOr, true
	}
	return 0, false
}

func binaryBitwise(op string) (script.Opcode, bool) {
	switch op {
	case "&":
		return script.OpAnd, true
	case "|":
		return script.OpOr, true
	case "^":
		return script.OpXor, true
	case "<<":
		return script.OpShiftL, true
	case ">>":
		return script.OpShiftR, true
	}
	return 0, false
}

// assignOpcode maps compound assignment operators to their VM opcodes. The
// simple `=` case is handled directly by OpAssign.
func assignOpcode(op string) (script.Opcode, bool) {
	switch op {
	case "+=":
		return script.OpAssignAdd, true
	case "-=":
		return script.OpAssignSub, true
	case "*=":
		return script.OpAssignMul, true
	case "/=":
		return script.OpAssignDiv, true
	case "%=":
		return script.OpAssignMod, true
	case "<<=":
		return script.OpAssignShiftL, true
	case ">>=":
		return script.OpAssignShiftR, true
	}
	return 0, false
}
