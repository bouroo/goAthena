// Package compiler walks a rAthena-script AST and emits bytecode
// instructions for the script VM.
package compiler

import (
	"fmt"
	"maps"
	"math"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

// pendingJump tracks a forward label reference that needs backpatching.
type pendingJump struct {
	instrIndex int    // index in instructions[] of the jump
	label      string // target label name
}

// Compiler walks an AST and emits bytecode instructions.
type Compiler struct {
	instructions []script.Instruction
	labels       map[string]int // label name → instruction index
	pendingJumps []pendingJump
	breakStack   []breakContext
	depth        int
	labelCounter int
}

// breakContext tracks the labels/targets for break and continue in the
// current loop or switch nesting level.
type breakContext struct {
	breakLabel     string
	continueTarget int // -1 when continue is invalid (e.g. inside switch)
}

// New creates a new compiler.
func New() *Compiler {
	return &Compiler{
		instructions: make([]script.Instruction, 0, 64),
		labels:       make(map[string]int),
		pendingJumps: make([]pendingJump, 0, 8),
		breakStack:   make([]breakContext, 0, 8),
	}
}

// Compile converts AST statements into a CompiledScript.
func (c *Compiler) Compile(name string, body []script.Stmt) (*script.CompiledScript, error) {
	c.reset()

	for _, s := range body {
		if err := c.compileStmt(s); err != nil {
			return nil, err
		}
	}

	if err := c.resolvePendingJumps(); err != nil {
		return nil, err
	}

	// Always terminate the script with an explicit end marker so the VM
	// halts cleanly even if the source omitted it.
	if len(c.instructions) == 0 || c.instructions[len(c.instructions)-1].Op != script.OpEnd {
		c.emit(script.OpEnd, 0, "")
	}

	out := script.NewCompiledScript(name)
	out.Instructions = append([]script.Instruction(nil), c.instructions...)
	maps.Copy(out.Labels, c.labels)
	return out, nil
}

func (c *Compiler) reset() {
	c.instructions = c.instructions[:0]
	clear(c.labels)
	c.pendingJumps = c.pendingJumps[:0]
	c.breakStack = c.breakStack[:0]
	c.depth = 0
	c.labelCounter = 0
}

// emit appends an instruction and returns its index.
func (c *Compiler) emit(op script.Opcode, operand int32, str string) int {
	idx := len(c.instructions)
	c.instructions = append(c.instructions, script.Instruction{
		Op:      op,
		Operand: operand,
		Str:     str,
	})
	return idx
}

// emitWithPos appends an instruction that carries a source position.
func (c *Compiler) emitWithPos(op script.Opcode, operand int32, str string, pos script.Position) int {
	idx := len(c.instructions)
	c.instructions = append(c.instructions, script.Instruction{
		Op:      op,
		Operand: operand,
		Str:     str,
		Pos:     pos,
	})
	return idx
}

// emitGoto emits an unconditional jump to label. The target may be a forward
// reference; unresolved jumps are backpatched after the whole body is emitted.
func (c *Compiler) emitGoto(label string, pos script.Position) int {
	idx := c.emitWithPos(script.OpGoto, 0, label, pos)
	if _, ok := c.labels[label]; !ok {
		c.pendingJumps = append(c.pendingJumps, pendingJump{instrIndex: idx, label: label})
	}
	return idx
}

// emitCallSub emits a callsub to a label, handling forward references.
// emitLabel records that the current instruction index is the target for the
// given label and resolves any pending jumps to it.
func (c *Compiler) emitLabel(name string) {
	idx := len(c.instructions)
	c.labels[name] = idx
	c.emit(script.OpLabel, safeInt32(idx), name)
}

// safeInt32 converts an int to int32, saturating on overflow.
func safeInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

// resolvePendingJumps verifies that every forward-referenced label has been
// defined. It reports an error for unresolved labels and patches any jump
// instructions whose operand should hold the target instruction index.
func (c *Compiler) resolvePendingJumps() error {
	for _, pj := range c.pendingJumps {
		target, ok := c.labels[pj.label]
		if !ok {
			return fmt.Errorf("compile error: unresolved label %q at instruction %d", pj.label, pj.instrIndex)
		}
		c.instructions[pj.instrIndex].Operand = safeInt32(target)
	}
	return nil
}

// pushBreakContext records a new break/continue context.
func (c *Compiler) pushBreakContext(continueTarget int) string {
	endLabel := c.freshLabel("break_end")
	c.breakStack = append(c.breakStack, breakContext{
		breakLabel:     endLabel,
		continueTarget: continueTarget,
	})
	return endLabel
}

// popBreakContext removes the innermost break/continue context.
func (c *Compiler) popBreakContext() {
	if len(c.breakStack) > 0 {
		c.breakStack = c.breakStack[:len(c.breakStack)-1]
	}
}

// currentBreakContext returns the active break/continue context or nil when
// not inside a breakable construct.
func (c *Compiler) currentBreakContext() *breakContext {
	if len(c.breakStack) == 0 {
		return nil
	}
	return &c.breakStack[len(c.breakStack)-1]
}

// freshLabel returns a generated label name that is guaranteed not to collide
// with user-defined labels. It uses an internal counter so the name does not
// drift when instructions are later inserted.
func (c *Compiler) freshLabel(prefix string) string {
	c.labelCounter++
	return fmt.Sprintf("__%s_%d", prefix, c.labelCounter)
}
