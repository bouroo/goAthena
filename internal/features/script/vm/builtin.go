package vm

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

// BuiltinFunc is a Go implementation of a script builtin.
// Builtins receive the VM so they can inspect the stack, mutate state,
// and return a single Value.
type BuiltinFunc func(ctx context.Context, vm *VM, args []Value) (Value, error)

// BuiltinRegistry holds registered builtin functions.
type BuiltinRegistry struct {
	funcs map[string]BuiltinFunc
}

// NewBuiltinRegistry creates an empty registry.
func NewBuiltinRegistry() *BuiltinRegistry {
	return &BuiltinRegistry{funcs: make(map[string]BuiltinFunc)}
}

// Register adds a builtin implementation under the given name.
func (r *BuiltinRegistry) Register(name string, fn BuiltinFunc) {
	r.funcs[name] = fn
}

// Lookup retrieves a builtin implementation by name.
func (r *BuiltinRegistry) Lookup(name string) (BuiltinFunc, bool) {
	fn, ok := r.funcs[name]
	return fn, ok
}

// RegisterDefaults registers the MVP builtin set. Each implementation is a
// stub that does not crash and returns a sensible default value.
func (r *BuiltinRegistry) RegisterDefaults() {
	r.Register("mes", builtinMes)
	r.Register("next", builtinNext)
	r.Register("close", builtinClose)
	r.Register("close2", builtinClose2)
	r.Register("end", builtinEnd)
	r.Register("menu", builtinMenu)
	r.Register("select", builtinSelect)
	r.Register("input", builtinInput)
	r.Register("set", builtinSet)
	r.Register("callfunc", builtinCallfunc)
	r.Register("warp", builtinWarp)
	r.Register("savepoint", builtinSavepoint)
	r.Register("cutin", builtinCutin)
	r.Register("getitem", builtinGetitem)
	r.Register("delitem", builtinDelitem)
	r.Register("countitem", builtinCountitem)
	r.Register("zeny", builtinZeny)
	r.Register("heal", builtinHeal)
	r.Register("strcharinfo", builtinStrcharinfo)
	r.Register("getmapxy", builtinGetmapxy)
	r.Register("gettimetick", builtinGettimetick)
	r.Register("rand", builtinRand)
	r.Register("sleep", builtinSleep)
	r.Register("getarg", builtinGetarg)
	r.Register("setarg", builtinSetarg)
}

// minArgs returns an error if len(args) is less than n.
func minArgs(name string, n int, args []Value) error {
	if len(args) < n {
		return fmt.Errorf("%s: expected at least %d args, got %d", name, n, len(args))
	}
	return nil
}

// builtinMes displays a message (stub: logs through fmt).
func builtinMes(_ context.Context, _ *VM, args []Value) (Value, error) {
	if len(args) > 0 {
		_ = args[0].AsStr()
	}
	return IntValue(0), nil
}

// builtinNext pauses execution and waits for the client to click "next".
func builtinNext(_ context.Context, vm *VM, _ []Value) (Value, error) {
	vm.state = StateStop
	return IntValue(0), nil
}

// builtinClose ends the script and closes the dialog.
func builtinClose(_ context.Context, vm *VM, _ []Value) (Value, error) {
	vm.state = StateEnd
	return IntValue(0), nil
}

// builtinClose2 closes the dialog but keeps executing.
func builtinClose2(_ context.Context, vm *VM, _ []Value) (Value, error) {
	vm.state = StateStop
	return IntValue(0), nil
}

// builtinEnd terminates the script.
func builtinEnd(_ context.Context, vm *VM, _ []Value) (Value, error) {
	vm.state = StateEnd
	return IntValue(0), nil
}

// builtinMenu displays a choice menu and pauses for selection.
func builtinMenu(_ context.Context, vm *VM, _ []Value) (Value, error) {
	vm.state = StateStop
	return IntValue(0), nil
}

// builtinSelect is an alias for menu that returns an index.
func builtinSelect(_ context.Context, vm *VM, _ []Value) (Value, error) {
	vm.state = StateStop
	return IntValue(0), nil
}

// builtinInput pauses to read an integer from the client.
func builtinInput(_ context.Context, vm *VM, _ []Value) (Value, error) {
	vm.state = StateStop
	return IntValue(0), nil
}

// builtinSet sets a variable from its arguments. It is primarily handled by
// OpAssign at compile time; this form accepts set(var, value).
func builtinSet(_ context.Context, vm *VM, args []Value) (Value, error) {
	if err := minArgs("set", 2, args); err != nil {
		return IntValue(0), err
	}
	name := args[0].AsStr()
	if name == "" {
		return IntValue(0), fmt.Errorf("set: empty variable name")
	}
	vm.scopes.Set(name, args[1])
	return IntValue(0), nil
}

// builtinCallfunc is the registry fallback for the callfunc builtin.
// In normal operation callfunc is handled directly by VM.execCallfunc
// (dispatched from VM.execFunc before reaching the builtin registry),
// because callfunc is a VM-level jump-based operation that switches
// vm.script and pc. Reaching this function means the VM dispatch has
// regressed and forgot to intercept "callfunc"; return an error so
// the regression surfaces loudly instead of silently returning 0.
func builtinCallfunc(_ context.Context, _ *VM, _ []Value) (Value, error) {
	return IntValue(0), fmt.Errorf("callfunc: handled by VM dispatch, registry fallback should be unreachable")
}

// builtinGetarg returns an argument passed to the current callfunc
// invocation. Signature: getarg(idx [, default]).
//
// Out-of-range indices return the supplied default (or 0 when no
// default is given). Negative indices are treated as out of range
// (rAthena treats negative as 0 / out of range).
func builtinGetarg(_ context.Context, vm *VM, args []Value) (Value, error) {
	if err := minArgs("getarg", 1, args); err != nil {
		return IntValue(0), err
	}
	frame := vm.currentArgs()
	if frame == nil {
		return IntValue(0), nil
	}
	i := args[0].AsInt()
	if i < 0 || i >= int64(len(frame)) {
		if len(args) >= 2 {
			return args[1], nil
		}
		return IntValue(0), nil
	}
	return frame[i], nil
}

// builtinSetarg mutates the current callfunc argument frame in place.
// Signature: setarg(idx, value). Out-of-range indices return an error.
// Because argFrames stores []Value slices (not deep-copies), writing to
// frame[i] makes the change visible to subsequent getarg calls within
// the same function invocation.
func builtinSetarg(_ context.Context, vm *VM, args []Value) (Value, error) {
	if err := minArgs("setarg", 2, args); err != nil {
		return IntValue(0), err
	}
	frame := vm.currentArgs()
	if frame == nil {
		return IntValue(0), fmt.Errorf("setarg: no active function frame")
	}
	i := args[0].AsInt()
	if i < 0 || i >= int64(len(frame)) {
		return IntValue(0), fmt.Errorf("setarg: index out of range")
	}
	frame[i] = args[1]
	return IntValue(0), nil
}

// builtinWarp warps the player (stub).
func builtinWarp(_ context.Context, _ *VM, args []Value) (Value, error) {
	_ = minArgs("warp", 3, args)
	return IntValue(0), nil
}

// builtinSavepoint sets the respawn point (stub).
func builtinSavepoint(_ context.Context, _ *VM, _ []Value) (Value, error) {
	return IntValue(0), nil
}

// builtinCutin displays a cutin (stub).
func builtinCutin(_ context.Context, _ *VM, _ []Value) (Value, error) {
	return IntValue(0), nil
}

// builtinGetitem gives an item to the player (stub: returns 0).
func builtinGetitem(_ context.Context, _ *VM, args []Value) (Value, error) {
	_ = minArgs("getitem", 2, args)
	return IntValue(0), nil
}

// builtinDelitem removes an item from the player (stub).
func builtinDelitem(_ context.Context, _ *VM, args []Value) (Value, error) {
	_ = minArgs("delitem", 2, args)
	return IntValue(0), nil
}

// builtinCountitem counts an item (stub: returns 0).
func builtinCountitem(_ context.Context, _ *VM, args []Value) (Value, error) {
	_ = minArgs("countitem", 1, args)
	return IntValue(0), nil
}

// builtinZeny manipulates zeny (stub: returns 0).
func builtinZeny(_ context.Context, _ *VM, args []Value) (Value, error) {
	_ = minArgs("zeny", 1, args)
	return IntValue(0), nil
}

// builtinHeal heals the player (stub).
func builtinHeal(_ context.Context, _ *VM, _ []Value) (Value, error) {
	return IntValue(0), nil
}

// builtinStrcharinfo returns character info as a string (stub: empty).
func builtinStrcharinfo(_ context.Context, _ *VM, _ []Value) (Value, error) {
	return StrValue(""), nil
}

// builtinGetmapxy returns map coordinates (stub: 0).
func builtinGetmapxy(_ context.Context, _ *VM, args []Value) (Value, error) {
	_ = minArgs("getmapxy", 1, args)
	return IntValue(0), nil
}

// builtinGettimetick returns the current Unix time in milliseconds.
func builtinGettimetick(_ context.Context, _ *VM, _ []Value) (Value, error) {
	return IntValue(time.Now().UnixMilli()), nil
}

// builtinRand returns a random integer in [0, n). With no arguments returns 0.
func builtinRand(_ context.Context, _ *VM, args []Value) (Value, error) {
	if len(args) == 0 {
		return IntValue(0), nil
	}
	n := args[0].AsInt()
	if n <= 0 {
		return IntValue(0), nil
	}
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return IntValue(0), fmt.Errorf("rand: %w", err)
	}
	return IntValue(v.Int64()), nil
}

// builtinSleep pauses execution for a duration. In this stub it immediately
// transitions to StateStop; the caller is responsible for resuming later.
func builtinSleep(_ context.Context, vm *VM, args []Value) (Value, error) {
	_ = minArgs("sleep", 1, args)
	vm.state = StateStop
	return IntValue(0), nil
}
