package vm

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

const (
	defaultMaxInstr = 100000
)

// State controls the execution state.
type State int

const (
	// StateRun indicates the VM is executing normally.
	StateRun State = iota
	// StateStop indicates the VM is paused waiting for client input.
	StateStop
	// StateEnd indicates the script ended.
	StateEnd
)

// funcFrame is the saved state needed to return from a callfunc'd
// function script back to its caller. It captures the previous script
// pointer (for switching back), the resume pc within the caller, and
// the length of argFrames so the function's arguments can be popped
// on return.
type funcFrame struct {
	priorScript       *script.CompiledScript
	priorPC           int
	priorArgFramesLen int
}

// VM executes compiled script bytecode.
type VM struct {
	stack      []Value
	pc         int
	script     *script.CompiledScript
	scopes     *ScopeStore
	builtins   *BuiltinRegistry
	funcs      map[string]*script.CompiledScript
	maxInstr   int
	instrCount int
	state      State
	callStack  []int
	funcFrames []funcFrame
	// argFrames is a stack of argument slices, one entry per active
	// callfunc invocation. The top of the stack is the current
	// function's argument frame; getarg/setarg read and mutate it.
	argFrames [][]Value
}

// New creates a VM for executing the given compiled script. Callfunc
// lookups against the script set's Funcs map will fail because no funcs
// map is wired in; use NewWithFuncs when the VM should be able to
// resolve function-script calls.
func New(code *script.CompiledScript, scopes *ScopeStore, builtins *BuiltinRegistry) *VM {
	if scopes == nil {
		scopes = NewScopeStore()
	}
	if builtins == nil {
		builtins = NewBuiltinRegistry()
		builtins.RegisterDefaults()
	}
	return &VM{
		stack:     make([]Value, 0, 64),
		script:    code,
		scopes:    scopes,
		builtins:  builtins,
		maxInstr:  defaultMaxInstr,
		state:     StateRun,
		callStack: make([]int, 0, 16),
	}
}

// NewWithFuncs creates a VM with access to the named function-script
// map. callfunc lookups will resolve against funcs.
func NewWithFuncs(code *script.CompiledScript, funcs map[string]*script.CompiledScript, scopes *ScopeStore, builtins *BuiltinRegistry) *VM {
	vm := New(code, scopes, builtins)
	vm.funcs = funcs
	return vm
}

// SetMaxInstr sets the instruction execution limit. A value <= 0 disables the
// limit (not recommended for untrusted scripts).
func (vm *VM) SetMaxInstr(n int) {
	vm.maxInstr = n
}

// State returns the current VM state.
func (vm *VM) State() State { return vm.state }

// StateSet transitions the VM to the given state. External callers
// (e.g. gateway dialog builtins running in another package) use this
// to pause or terminate the VM in response to script builtins whose
// semantics (next, menu, close, end) are defined by the host rather
// than by the VM. The VM honors the new state on its next Step or
// Run iteration.
func (vm *VM) StateSet(s State) { vm.state = s }

// Run executes the script to completion (or until STOP/END).
// It returns the final VM state.
func (vm *VM) Run(ctx context.Context) (State, error) {
	for vm.state == StateRun {
		if _, err := vm.Step(ctx); err != nil {
			return vm.state, err
		}
	}
	return vm.state, nil
}

// Step executes one instruction and returns the new state.
func (vm *VM) Step(ctx context.Context) (State, error) {
	if vm.state != StateRun {
		return vm.state, nil
	}
	if vm.pc < 0 || vm.pc >= len(vm.script.Instructions) {
		vm.state = StateEnd
		return vm.state, nil
	}

	vm.instrCount++
	if vm.maxInstr > 0 && vm.instrCount > vm.maxInstr {
		return vm.state, fmt.Errorf("instruction limit exceeded at pc=%d", vm.pc)
	}

	instr := vm.script.Instructions[vm.pc]
	vm.pc++

	if err := vm.execute(ctx, instr); err != nil {
		return vm.state, fmt.Errorf("vm error at pc=%d op=%s: %w", vm.pc-1, instr.Op, err)
	}
	return vm.state, nil
}

// Resume continues execution after a STOP state (e.g. after client input).
func (vm *VM) Resume(ctx context.Context) (State, error) {
	if vm.state == StateStop {
		vm.state = StateRun
		return vm.Run(ctx)
	}
	return vm.state, nil
}

// execute dispatches a single instruction.
func (vm *VM) execute(ctx context.Context, instr script.Instruction) error {
	if fn, ok := vm.dispatchTable()[instr.Op]; ok {
		return fn(ctx, instr)
	}
	return fmt.Errorf("unhandled opcode %s", instr.Op)
}

// dispatchTable returns the opcode -> handler mapping.
func (vm *VM) dispatchTable() map[script.Opcode]func(context.Context, script.Instruction) error {
	return map[script.Opcode]func(context.Context, script.Instruction) error{
		script.OpInt:          vm.execStackLoad,
		script.OpStr:          vm.execStackLoad,
		script.OpPush:         vm.execStackLoad,
		script.OpVar:          vm.execVarLoad,
		script.OpAssign:       vm.execAssign,
		script.OpAssignAdd:    vm.execAssign,
		script.OpAssignSub:    vm.execAssign,
		script.OpAssignMul:    vm.execAssign,
		script.OpAssignDiv:    vm.execAssign,
		script.OpAssignMod:    vm.execAssign,
		script.OpAssignShiftL: vm.execAssign,
		script.OpAssignShiftR: vm.execAssign,
		script.OpAdd:          vm.execArithmetic,
		script.OpSub:          vm.execArithmetic,
		script.OpMul:          vm.execArithmetic,
		script.OpDiv:          vm.execArithmetic,
		script.OpMod:          vm.execArithmetic,
		script.OpNeg:          vm.execUnaryNeg,
		script.OpAnd:          vm.execBitwise,
		script.OpOr:           vm.execBitwise,
		script.OpXor:          vm.execBitwise,
		script.OpBNot:         vm.execBitwise,
		script.OpShiftL:       vm.execBitwise,
		script.OpShiftR:       vm.execBitwise,
		script.OpLEq:          vm.execComparison,
		script.OpLNe:          vm.execComparison,
		script.OpLLT:          vm.execComparison,
		script.OpLGT:          vm.execComparison,
		script.OpLLE:          vm.execComparison,
		script.OpLGE:          vm.execComparison,
		script.OpLAnd:         vm.execLogical,
		script.OpLOr:          vm.execLogical,
		script.OpNot:          vm.execUnaryNot,
		script.OpGoto:         vm.execGoto,
		script.OpCallSub:      vm.execCallSub,
		script.OpReturn:       vm.execReturn,
		script.OpFunc:         vm.execFunc,
		script.OpEnd:          vm.execEnd,
		script.OpClose:        vm.execEnd,
		script.OpLabel:        vm.execNoop,
		script.OpLine:         vm.execNoop,
		script.OpExpr:         vm.execNoop,
		script.OpExpr2:        vm.execNoop,
		script.OpBinary:       vm.execIndexGet,
		script.OpIndexGet:     vm.execIndexGet,
		script.OpIndexSet:     vm.execIndexSet,
		script.OpName:         vm.execNoop,
		script.OpEOF:          vm.execEnd,
	}
}

func (vm *VM) execFunc(ctx context.Context, instr script.Instruction) error {
	if instr.Str == "callfunc" {
		return vm.execCallfunc(ctx)
	}
	return vm.callBuiltin(ctx, instr.Str)
}

func (vm *VM) execEnd(context.Context, script.Instruction) error {
	vm.state = StateEnd
	return nil
}

func (vm *VM) execNoop(context.Context, script.Instruction) error {
	return nil
}

func (vm *VM) execStackLoad(_ context.Context, instr script.Instruction) error {
	switch instr.Op {
	case script.OpInt:
		vm.push(IntValue(int64(instr.Operand)))
	case script.OpStr:
		vm.push(StrValue(instr.Str))
	case script.OpPush:
		if instr.Str != "" {
			vm.push(StrValue(instr.Str))
		} else {
			vm.push(IntValue(int64(instr.Operand)))
		}
	default:
		// Only stack-load opcodes are routed here.
	}
	return nil
}

func (vm *VM) execVarLoad(_ context.Context, instr script.Instruction) error {
	val, ok := vm.scopes.Get(instr.Str)
	if !ok {
		// Uninitialized variables are treated as 0/empty string.
		val = IntValue(0)
	}
	vm.push(val)
	return nil
}

func (vm *VM) execAssign(_ context.Context, instr script.Instruction) error {
	op := vm.assignOp(instr.Op)
	return vm.doAssign(instr.Str, op)
}

func (vm *VM) assignOp(op script.Opcode) func(cur, rhs Value) Value {
	switch op {
	case script.OpAssign:
		return func(cur, rhs Value) Value { return rhs }
	case script.OpAssignAdd:
		return func(a, b Value) Value { return IntValue(a.AsInt() + b.AsInt()) }
	case script.OpAssignSub:
		return func(a, b Value) Value { return IntValue(a.AsInt() - b.AsInt()) }
	case script.OpAssignMul:
		return func(a, b Value) Value { return IntValue(a.AsInt() * b.AsInt()) }
	case script.OpAssignDiv:
		return func(a, b Value) Value {
			if b.AsInt() == 0 {
				return IntValue(0)
			}
			return IntValue(a.AsInt() / b.AsInt())
		}
	case script.OpAssignMod:
		return func(a, b Value) Value {
			if b.AsInt() == 0 {
				return IntValue(0)
			}
			return IntValue(a.AsInt() % b.AsInt())
		}
	case script.OpAssignShiftL:
		return func(a, b Value) Value { return IntValue(a.AsInt() << b.AsInt()) }
	case script.OpAssignShiftR:
		return func(a, b Value) Value { return IntValue(a.AsInt() >> b.AsInt()) }
	default:
		return nil
	}
}

func (vm *VM) execArithmetic(_ context.Context, instr script.Instruction) error {
	b, a := vm.pop2()
	switch instr.Op {
	case script.OpAdd:
		vm.push(IntValue(a.AsInt() + b.AsInt()))
	case script.OpSub:
		vm.push(IntValue(a.AsInt() - b.AsInt()))
	case script.OpMul:
		vm.push(IntValue(a.AsInt() * b.AsInt()))
	case script.OpDiv:
		if b.AsInt() == 0 {
			vm.push(IntValue(0))
		} else {
			vm.push(IntValue(a.AsInt() / b.AsInt()))
		}
	case script.OpMod:
		if b.AsInt() == 0 {
			vm.push(IntValue(0))
		} else {
			vm.push(IntValue(a.AsInt() % b.AsInt()))
		}
	default:
		// Only arithmetic opcodes are routed here.
	}
	return nil
}

func (vm *VM) execBitwise(_ context.Context, instr script.Instruction) error {
	b, a := vm.pop2()
	switch instr.Op {
	case script.OpAnd:
		vm.push(IntValue(a.AsInt() & b.AsInt()))
	case script.OpOr:
		vm.push(IntValue(a.AsInt() | b.AsInt()))
	case script.OpXor:
		vm.push(IntValue(a.AsInt() ^ b.AsInt()))
	case script.OpBNot:
		vm.push(IntValue(^vm.pop().AsInt()))
	case script.OpShiftL:
		vm.push(IntValue(a.AsInt() << b.AsInt()))
	case script.OpShiftR:
		vm.push(IntValue(a.AsInt() >> b.AsInt()))
	default:
		// Only bitwise opcodes are routed here.
	}
	return nil
}

func (vm *VM) execComparison(_ context.Context, instr script.Instruction) error {
	b, a := vm.pop2()
	switch instr.Op {
	case script.OpLEq:
		vm.push(boolValue(a.AsInt() == b.AsInt()))
	case script.OpLNe:
		vm.push(boolValue(a.AsInt() != b.AsInt()))
	case script.OpLLT:
		vm.push(boolValue(a.AsInt() < b.AsInt()))
	case script.OpLGT:
		vm.push(boolValue(a.AsInt() > b.AsInt()))
	case script.OpLLE:
		vm.push(boolValue(a.AsInt() <= b.AsInt()))
	case script.OpLGE:
		vm.push(boolValue(a.AsInt() >= b.AsInt()))
	default:
		// Only comparison opcodes are routed here.
	}
	return nil
}

func (vm *VM) execLogical(_ context.Context, instr script.Instruction) error {
	b, a := vm.pop2()
	switch instr.Op {
	case script.OpLAnd:
		vm.push(boolValue(a.IsTruthy() && b.IsTruthy()))
	case script.OpLOr:
		vm.push(boolValue(a.IsTruthy() || b.IsTruthy()))
	default:
		// Only logical opcodes are routed here.
	}
	return nil
}

func (vm *VM) execUnaryNeg(context.Context, script.Instruction) error {
	vm.push(IntValue(-vm.pop().AsInt()))
	return nil
}

func (vm *VM) execUnaryNot(context.Context, script.Instruction) error {
	vm.push(boolValue(!vm.pop().IsTruthy()))
	return nil
}

func (vm *VM) execGoto(_ context.Context, instr script.Instruction) error {
	cond := vm.pop()
	if !cond.IsTruthy() {
		vm.pc = vm.resolveLabel(instr.Str)
	}
	return nil
}

// execIndexGet implements both OpBinary (in its Phase R0 S1 array-read
// role) and OpIndexGet. Stack contract (top → bottom): idx, name.
// The compiler emits the array name as a string, so name.AsStr() is
// used. Uninitialized elements resolve to zero Value.
func (vm *VM) execIndexGet(_ context.Context, _ script.Instruction) error {
	idx := vm.pop()
	name := vm.pop()
	val, _ := vm.scopes.GetArray(name.AsStr(), idx.AsInt())
	vm.push(val)
	return nil
}

// execIndexSet implements OpIndexSet. Stack contract (top → bottom):
// name, idx, value. Stores value into ScopeStore.SetArray(name, idx).
func (vm *VM) execIndexSet(_ context.Context, _ script.Instruction) error {
	name := vm.pop()
	idx := vm.pop()
	val := vm.pop()
	vm.scopes.SetArray(name.AsStr(), idx.AsInt(), val)
	return nil
}

func (vm *VM) execCallSub(_ context.Context, instr script.Instruction) error {
	target := vm.resolveLabel(instr.Str)
	vm.callStack = append(vm.callStack, vm.pc)
	vm.pc = target
	return nil
}

// execReturn unwinds the callsub/callfunc stack. If a funcFrame is on
// top (callfunc-return), the VM restores the caller's script pointer
// and pc, truncates argFrames back to its saved length, and leaves
// the value placed on the operand stack by `return <expr>;` in place
// as the callfunc result for the caller's next instruction to
// consume. If only the callsub stack has frames, it pops that and
// resumes the caller. With no frames at all the script ends.
func (vm *VM) execReturn(context.Context, script.Instruction) error {
	// callfunc return takes priority: it restores the caller's
	// script pointer and pc and pops the function's argFrame.
	if len(vm.funcFrames) > 0 {
		frame := vm.funcFrames[len(vm.funcFrames)-1]
		vm.funcFrames = vm.funcFrames[:len(vm.funcFrames)-1]
		vm.script = frame.priorScript
		vm.pc = frame.priorPC
		if len(vm.argFrames) > frame.priorArgFramesLen {
			vm.argFrames = vm.argFrames[:frame.priorArgFramesLen]
		}
		return nil
	}
	if len(vm.callStack) == 0 {
		vm.state = StateEnd
		return nil
	}
	ret := vm.callStack[len(vm.callStack)-1]
	vm.callStack = vm.callStack[:len(vm.callStack)-1]
	vm.pc = ret
	return nil
}

// NewAtLabel creates a VM positioned to begin execution at the named label.
// It returns (vm, true) when the label exists in code, or (nil, false)
// when it does not. Use this to run event entry points such as "OnInit"
// without executing the script body that precedes them.
//
// The label instruction itself (OpLabel) is a no-op; execution begins with
// that no-op, then proceeds into the label body.
func NewAtLabel(code *script.CompiledScript, label string, scopes *ScopeStore, builtins *BuiltinRegistry) (*VM, bool) {
	idx, ok := code.LookupLabel(label)
	if !ok {
		return nil, false
	}
	vm := New(code, scopes, builtins)
	vm.pc = idx
	return vm, true
}

// NewAtLabelWithFuncs mirrors NewAtLabel but wires the named function
// script map so callfunc can resolve cross-script calls.
func NewAtLabelWithFuncs(
	code *script.CompiledScript,
	funcs map[string]*script.CompiledScript,
	label string,
	scopes *ScopeStore,
	builtins *BuiltinRegistry,
) (*VM, bool) {
	idx, ok := code.LookupLabel(label)
	if !ok {
		return nil, false
	}
	vm := NewWithFuncs(code, funcs, scopes, builtins)
	vm.pc = idx
	return vm, true
}

// resolveLabel returns the instruction index for a label, defaulting to the
// current pc if the label is missing.
func (vm *VM) resolveLabel(name string) int {
	if idx, ok := vm.script.LookupLabel(name); ok {
		return idx
	}
	return vm.pc
}

// doAssign pops a value and stores it in the named variable. If op is nil,
// the popped value is stored directly; otherwise the current variable value
// and popped value are combined.
func (vm *VM) doAssign(name string, op func(cur, rhs Value) Value) error {
	val := vm.pop()
	if op != nil {
		cur, _ := vm.scopes.Get(name)
		val = op(cur, val)
	}
	vm.scopes.Set(name, val)
	return nil
}

// noResultBuiltins is the set of builtins whose return value is a
// statement-only signal (always 0 / ""). They must not push a result
// onto the operand stack, otherwise subsequent expressions like a
// follow-up callfunc would mistake the leftover IntValue(0) for the
// function-name argument. Builtins whose value IS consumed by the
// caller (rand, getarg, etc.) are not in this set.
var noResultBuiltins = map[string]bool{
	"mes":       true,
	"next":      true,
	"close":     true,
	"close2":    true,
	"end":       true,
	"menu":      true,
	"select":    true,
	"input":     true,
	"set":       true,
	"sleep":     true,
	"warp":      true,
	"savepoint": true,
	"cutin":     true,
	"getitem":   true,
	"delitem":   true,
	"zeny":      true,
	"heal":      true,
	"getmapxy":  true,
}

// callBuiltin invokes a registered builtin with the arguments currently on
// the stack. Builtins in noResultBuiltins consume their arguments and
// return no value (the operand stack is left as it was before the
// arguments were pushed, minus the arguments). Other builtins push
// their result back onto the stack.
func (vm *VM) callBuiltin(ctx context.Context, name string) error {
	fn, ok := vm.builtins.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown builtin %q", name)
	}
	args := vm.collectArgs()
	res, err := fn(ctx, vm, args)
	if err != nil {
		return err
	}
	if !noResultBuiltins[name] {
		vm.push(res)
	}
	return nil
}

// execCallfunc implements the `callfunc("name", args...)` builtin.
// It is a jump-based VM-level operation (not a normal builtin): it
// pushes a funcFrame saving the caller's script pointer, pc, and the
// length of argFrames, swaps vm.script to the named function's
// CompiledScript, resets pc to 0, and pushes args[1:] onto argFrames
// so getarg/setarg can read/mutate them. The main Run loop then drives
// the function directly, so any pause/resume boundary (next, menu,
// select, sleep) inside the function is handled by the same state
// machine that pauses the caller — no nested execution loop is used.
//
// execReturn restores the caller's script pointer, pc, and argFrames
// length when a funcFrame is on top, leaving the value placed on the
// operand stack by `return <expr>;` as the callfunc result for the
// caller's `OpAssign` to consume.
//
// args[0] is the function name; args[1:] are forwarded to the function.
func (vm *VM) execCallfunc(ctx context.Context) error {
	args := vm.collectArgs()
	if len(args) < 1 {
		return fmt.Errorf("callfunc: expected at least 1 arg, got %d", len(args))
	}
	name := args[0].AsStr()
	fn, ok := vm.funcs[name]
	if !ok {
		return fmt.Errorf("callfunc: unknown function %q", name)
	}
	vm.funcFrames = append(vm.funcFrames, funcFrame{
		priorScript:       vm.script,
		priorPC:           vm.pc,
		priorArgFramesLen: len(vm.argFrames),
	})
	vm.argFrames = append(vm.argFrames, args[1:])
	vm.script = fn
	vm.pc = 0
	return nil
}

// currentArgs returns the argument slice for the active callfunc
// invocation, or nil if no function is running.
func (vm *VM) currentArgs() []Value {
	if len(vm.argFrames) == 0 {
		return nil
	}
	return vm.argFrames[len(vm.argFrames)-1]
}

// collectArgs returns all values currently on the stack as arguments and
// clears the stack. The compiler does not insert argument separators for
// builtins in this phase, so every value on the stack is an argument.
func (vm *VM) collectArgs() []Value {
	args := make([]Value, len(vm.stack))
	copy(args, vm.stack)
	vm.stack = vm.stack[:0]
	return args
}

// push adds a value to the top of the stack.
func (vm *VM) push(v Value) {
	vm.stack = append(vm.stack, v)
}

// pop removes and returns the top value from the stack.
func (vm *VM) pop() Value {
	if len(vm.stack) == 0 {
		return IntValue(0)
	}
	v := vm.stack[len(vm.stack)-1]
	vm.stack = vm.stack[:len(vm.stack)-1]
	return v
}

// pop2 removes and returns the top two values in the order (second, first).
func (vm *VM) pop2() (Value, Value) {
	return vm.pop(), vm.pop()
}

// boolValue converts a boolean to a Value (1 or 0).
func boolValue(b bool) Value {
	if b {
		return IntValue(1)
	}
	return IntValue(0)
}
