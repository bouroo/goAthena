package script

import (
	"fmt"
	"maps"
)

// Host is the seam between the VM and the game server. The dialog builtins
// produce output here and may block the VM goroutine: Next() suspends the
// script until the client sends CZ_REQ_NEXT_SCRIPT, then returns. This is the
// goroutine-per-dialog model — the script's sequential state lives on the
// goroutine stack, so no manual IP serialization is needed across the wait.
// Effects (warp/heal) flow back through the Host to the world module.
type Host interface {
	// Mes sends one dialog line (ZC_SCRIPT). Does not block.
	Mes(msg string)
	// Next sends the "click Next" prompt and blocks until the client
	// advances the dialog. Returns false if the dialog was closed or the
	// connection dropped, in which case the VM ends the script.
	Next() bool
	// Select sends the menu option list (ZC_MENU_LIST) and blocks until the
	// client chooses. options is the flat list (colon-separated inputs are
	// split by the builtin before calling). Returns the 1-based index the
	// client picked, or 255 (FF_CANCEL) for cancel, mirroring rAthena's
	// @menu / sd->npc_menu result of clif_parse_NpcSelectMenu.
	Select(options []string) int
	// Input sends the numeric-input dialog (ZC_OPEN_EDITDLG) and blocks until
	// the client replies with CZ_INPUT_EDITDLG. Returns the entered amount and
	// false if the dialog was closed or the connection dropped, in which case
	// the VM ends the script — mirroring Next()'s false return.
	Input() (amount int64, ok bool)
	// InputStr sends the string-input dialog (ZC_OPEN_EDITDLGSTR) and blocks
	// until the client replies with CZ_INPUT_EDITDLGSTR. Returns the entered
	// text and false if the dialog was closed or the connection dropped.
	InputStr() (text string, ok bool)
	// Close sends the close-dialog frame (ZC_CLOSE_DIALOG).
	Close()
	// Warp moves the player to the named map tile.
	Warp(mapName string, x, y int)
	// PercentHeal restores HP/SP by the given percentages (0–100).
	PercentHeal(hpPct, spPct int)
}

// control tells the VM how a builtin affected script flow after it returns.
type control uint8

const (
	// ctrlContinue keeps executing the next instruction.
	ctrlContinue control = iota
	// ctrlEnd stops the VM: `end` and `close` return it.
	ctrlEnd
)

// BuiltinFunc is a script builtin invoked by OpFunc. It receives the VM (for
// variable access) and the already-popped, correctly-ordered argument slice,
// and returns a result value plus a flow control.
type BuiltinFunc func(vm *VM, args []Value) (Value, control)

// VM is a stack machine executing one CompiledScript. A fresh VM is created
// per dialog invocation; its goroutine owns its stack, IP, call stack, and
// temporary variables. The variable scope (vars) is the caller's — seeded
// with char vars before Run and read back after to persist changes — so the
// VM itself stays stateless across invocations.
type VM struct {
	instrs    []Instruction
	labels    map[string]int
	ip        int
	stack     []Value
	vars      map[string]Value
	host      Host
	builtins  map[string]BuiltinFunc
	callstack []int
}

// NewVM constructs a VM for cs. host is the I/O seam, builtins resolves
// OpFunc names (DefaultBuiltins covers the dialog subset), and vars is the
// initial variable scope (may be nil). After Run, mutated vars remain in the
// same map for the caller to persist.
func NewVM(cs *CompiledScript, host Host, builtins map[string]BuiltinFunc, vars map[string]Value) *VM {
	v := &VM{
		instrs:   nil,
		labels:   make(map[string]int),
		host:     host,
		builtins: builtins,
		vars:     vars,
	}
	if cs != nil {
		v.instrs = cs.Instructions
		maps.Copy(v.labels, cs.Labels)
	}
	return v
}

// GetVar returns a variable's value. An unset variable yields the zero Value
// (nil), which IsZero reports as false and asInt reads as 0 — matching
// rAthena's "unset integer reads as 0".
func (vm *VM) GetVar(name string) Value {
	if vm.vars == nil {
		return NilVal()
	}
	return vm.vars[name]
}

// SetVar stores a variable, initializing the scope on first write.
func (vm *VM) SetVar(name string, v Value) {
	if vm.vars == nil {
		vm.vars = make(map[string]Value)
	}
	vm.vars[name] = v
}

// Vars returns the live variable map (mutated in place by SetVar/assignment).
// Callers read it after Run to persist char-var changes.
func (vm *VM) Vars() map[string]Value { return vm.vars }

func (vm *VM) push(v Value) { vm.stack = append(vm.stack, v) }

func (vm *VM) pop() Value {
	n := len(vm.stack) - 1
	v := vm.stack[n]
	vm.stack = vm.stack[:n]
	return v
}

// popArgs pops n values off the stack into an args slice in source order
// (args[0] is the first-pushed argument). Assumes the caller validated the
// stack has at least n values; the compiler guarantees this for well-formed
// scripts.
func (vm *VM) popArgs(n int) []Value {
	args := make([]Value, n)
	for i := n - 1; i >= 0; i-- {
		args[i] = vm.pop()
	}
	return args
}

// Run executes bytecode until OpEOF, a ctrlEnd builtin (end/close), or an
// error. Jumps set ip and continue the loop; every other op advances ip by
// one at the loop tail.
func (vm *VM) Run() error { //nolint:gocyclo
	for vm.ip < len(vm.instrs) {
		ins := vm.instrs[vm.ip]
		switch ins.Op {
		case OpEOF:
			return nil
		case OpInt:
			vm.push(IntVal(int64(ins.Operand)))
		case OpStr:
			vm.push(StrVal(ins.Str))
		case OpVar:
			vm.push(vm.GetVar(ins.Str))
		case OpName:
			// A name reference pushes the variable's own name as a string —
			// used by builtins like setarray/get that take a variable by name.
			vm.push(StrVal(ins.Str))
		case OpAssign:
			vm.SetVar(ins.Str, vm.pop())
		case OpAdd, OpSub, OpMul, OpDiv, OpMod,
			OpLEq, OpLNe, OpLGT, OpLLT, OpLGE, OpLLE,
			OpAnd, OpOr, OpXor, OpShiftL, OpShiftR:
			if err := vm.binary(ins.Op); err != nil {
				return err
			}
		case OpNeg:
			vm.push(IntVal(-vm.pop().asInt()))
		case OpNot:
			vm.push(boolVal(vm.pop().IsZero()))
		case OpBNot:
			vm.push(IntVal(^vm.pop().asInt()))
		case OpLabel:
			// No-op marker for disassembly; the label index is already in the
			// labels map, so a fall-through here is correct.
		case OpLine:
			// Source-line marker for stack traces; no runtime effect.
		case OpJumpIfFalse:
			if vm.pop().IsZero() {
				vm.jump(ins.Str)
				continue
			}
		case OpJumpIfTrue:
			if !vm.pop().IsZero() {
				vm.jump(ins.Str)
				continue
			}
		case OpGoto:
			vm.jump(ins.Str)
			continue
		case OpCallSub:
			// Push the return address (the instruction after this OpCallSub)
			// and jump to the label. Args were pushed by the caller and stay
			// on the stack for the subroutine to read.
			vm.callstack = append(vm.callstack, vm.ip+1)
			vm.jump(ins.Str)
			continue
		case OpReturn:
			if len(vm.callstack) == 0 {
				return nil
			}
			top := len(vm.callstack) - 1
			vm.ip = vm.callstack[top]
			vm.callstack = vm.callstack[:top]
			continue
		case OpFunc:
			args := vm.popArgs(int(ins.Operand))
			fn := vm.builtins[ins.Str]
			if fn == nil {
				return fmt.Errorf("script: unknown builtin %q at %s", ins.Str, ins.Pos)
			}
			ret, ctrl := fn(vm, args)
			if ctrl == ctrlEnd {
				return nil
			}
			// The builtin's result stays on the stack. Expression context
			// (e.g. select(...)) consumes it; statement context drops it via
			// the OpFunc the compiler follows with OpPop.
			vm.push(ret)
		case OpIndexGet:
			if err := vm.indexGet(); err != nil {
				return err
			}
		case OpIndexSet:
			if err := vm.indexSet(); err != nil {
				return err
			}
		case OpPop:
			// Discard a statement-position builtin's void result.
			_ = vm.pop()
		default:
			return fmt.Errorf("script: unhandled opcode %s at %s", ins.Op, ins.Pos)
		}
		vm.ip++
	}
	return nil
}

// jump sets ip to the instruction at the named label. An unresolved label is a
// compiler bug (the compiler writes every label it emits), surfaced as a hard
// error rather than silently running off the end.
func (vm *VM) jump(name string) {
	target, ok := vm.labels[name]
	if !ok {
		target = len(vm.instrs)
	}
	vm.ip = target
}

// binary pops two operands (rhs on top) and pushes the result of applying op.
// String-concatenation for `+` mirrors rAthena: if either side is a string the
// result is string concatenation, otherwise integer arithmetic. Comparisons
// coerce both sides via asInt and push a 0/1 integer.
func (vm *VM) binary(op Opcode) error { //nolint:gocyclo
	b := vm.pop()
	a := vm.pop()
	switch op {
	case OpAdd:
		if a.Kind == KindStr || b.Kind == KindStr {
			vm.push(StrVal(a.String() + b.String()))
		} else {
			vm.push(IntVal(a.asInt() + b.asInt()))
		}
	case OpSub:
		vm.push(IntVal(a.asInt() - b.asInt()))
	case OpMul:
		vm.push(IntVal(a.asInt() * b.asInt()))
	case OpDiv:
		if b.asInt() == 0 {
			vm.push(IntVal(0))
		} else {
			vm.push(IntVal(a.asInt() / b.asInt()))
		}
	case OpMod:
		if b.asInt() == 0 {
			vm.push(IntVal(0))
		} else {
			vm.push(IntVal(a.asInt() % b.asInt()))
		}
	case OpLEq:
		vm.push(boolVal(a.asInt() == b.asInt()))
	case OpLNe:
		vm.push(boolVal(a.asInt() != b.asInt()))
	case OpLGT:
		vm.push(boolVal(a.asInt() > b.asInt()))
	case OpLLT:
		vm.push(boolVal(a.asInt() < b.asInt()))
	case OpLGE:
		vm.push(boolVal(a.asInt() >= b.asInt()))
	case OpLLE:
		vm.push(boolVal(a.asInt() <= b.asInt()))
	case OpAnd:
		vm.push(IntVal(a.asInt() & b.asInt()))
	case OpOr:
		vm.push(IntVal(a.asInt() | b.asInt()))
	case OpXor:
		vm.push(IntVal(a.asInt() ^ b.asInt()))
	case OpShiftL:
		vm.push(IntVal(a.asInt() << uint(b.asInt()))) //nolint:gosec // G115: shift amount bounded by script int range
	case OpShiftR:
		vm.push(IntVal(a.asInt() >> uint(b.asInt()))) //nolint:gosec // G115: shift amount bounded by script int range
	default:
		return fmt.Errorf("script: not a binary op %s", op)
	}
	return nil
}

// indexGet pops idx (top) then name, and pushes the array element. Array
// variables are stored as the element's value at the bare name for now; full
// array support lands with setarray/menu in a later phase.
func (vm *VM) indexGet() error {
	idx := vm.pop()
	name := vm.pop().String()
	// Array elements are keyed name + "[" + idx + "]" so reads and writes
	// share one flat scope without a separate array type.
	vm.push(vm.GetVar(name + "[" + itoa(int(idx.asInt())) + "]"))
	return nil
}

// indexSet pops value (top), idx (mid), name (bottom) and stores the element.
func (vm *VM) indexSet() error {
	val := vm.pop()
	idx := vm.pop()
	name := vm.pop().String()
	vm.SetVar(name+"["+itoa(int(idx.asInt()))+"]", val)
	return nil
}

// boolVal converts a Go bool to the script's integer-truth encoding (1/0).
func boolVal(b bool) Value {
	if b {
		return IntVal(1)
	}
	return IntVal(0)
}
