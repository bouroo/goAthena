package script

import "strings"

// Package-level builtin implementations for the dialog subset. Each takes the
// VM (for variable access) and the OpFunc argument slice, and returns a value
// plus a flow control (ctrlContinue keeps going; ctrlEnd stops the VM).
//
// These are the verbs a script exercises end-to-end in M11a: mes/next/close/
// close2/end for dialog, set (desugared to assignment in the compiler, but a
// builtin is provided for callfunc-style invocation), warp and percentheal for
// the healer/warper L3 added in M11d. Further builtins (getitem, select, menu,
// sc_start, gettimetick, strcharinfo, callfunc) are appended in later phases.

// builtinMes sends one dialog line. The argument is coerced to its textual
// form; an absent argument renders as the empty string.
func builtinMes(vm *VM, args []Value) (Value, control) {
	vm.host.Mes(argStr(args, 0))
	return NilVal(), ctrlContinue
}

// builtinNext blocks the VM on the Host until the client advances the dialog.
// A false return (closed/dropped) ends the script.
func builtinNext(vm *VM, args []Value) (Value, control) {
	if !vm.host.Next() {
		return NilVal(), ctrlEnd
	}
	return NilVal(), ctrlContinue
}

// builtinClose sends the close frame and ends the script.
func builtinClose(vm *VM, args []Value) (Value, control) {
	vm.host.Close()
	return NilVal(), ctrlEnd
}

// builtinClose2 sends the close frame but does NOT end the script — it is the
// "close at end of script, keep running" variant rAthena uses before a final
// terminator.
func builtinClose2(vm *VM, args []Value) (Value, control) {
	vm.host.Close()
	return NilVal(), ctrlContinue
}

// builtinEnd terminates the script immediately with no dialog frame.
func builtinEnd(vm *VM, args []Value) (Value, control) {
	return NilVal(), ctrlEnd
}

// builtinSet assigns a variable by name. The first argument is the variable
// name (pushed as a string via OpName by the compiler when `set` is invoked
// through a callfunc); the second is the value. The common `set var, val;`
// command form is desugared to a direct assignment in the compiler and never
// reaches this builtin — this covers the residual callfunc path.
func builtinSet(vm *VM, args []Value) (Value, control) {
	if len(args) >= 2 {
		vm.SetVar(args[0].String(), args[1])
	}
	return NilVal(), ctrlContinue
}

// builtinWarp moves the player: args = (mapName, x, y).
func builtinWarp(vm *VM, args []Value) (Value, control) {
	if len(args) >= 3 {
		vm.host.Warp(args[0].String(), int(args[1].asInt()), int(args[2].asInt()))
	}
	return NilVal(), ctrlContinue
}

// builtinPercentHeal restores HP/SP: args = (hpPct, spPct), defaulting both to
// 100 when omitted — the common `percentheal 100,100;` healer line.
func builtinPercentHeal(vm *VM, args []Value) (Value, control) {
	hp, sp := 100, 100
	if len(args) >= 1 {
		hp = int(args[0].asInt())
	}
	if len(args) >= 2 {
		sp = int(args[1].asInt())
	}
	vm.host.PercentHeal(hp, sp)
	return NilVal(), ctrlContinue
}

// builtinSelect implements select("opt1:opt2:..."). rAthena's select takes
// varargs where each may itself be colon-separated; the displayed menu is the
// arguments joined by ":" and the client numbers the colon-separated entries
// 1..N. The builtin builds that flat option list, delegates to Host.Select
// (which emits ZC_MENU_LIST and blocks for the client's CZ_CHOOSE_MENU), and
// returns the chosen 1-based index — 255 when the player cancels
// (clif.cpp:13337 clif_parse_NpcSelectMenu, choice byte 0xff). Because select
// is an expression (`if (select(...) == 2)`), its result stays on the stack;
// the compiler only OpPops statement-position calls.
func builtinSelect(vm *VM, args []Value) (Value, control) {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, a.String())
	}
	// Re-join then split so a single "a:b:c" arg and varargs "a","b" produce
	// the same option list the client numbers.
	options := strings.Split(strings.Join(parts, ":"), ":")
	return IntVal(int64(vm.host.Select(options))), ctrlContinue
}

// DefaultBuiltins returns a fresh map of the dialog-subset builtins, ready to
// hand to NewVM. Callers extend this map with phase-specific builtins (select,
// menu, getitem, sc_start, …) by copying it and inserting their entries.
func DefaultBuiltins() map[string]BuiltinFunc {
	return map[string]BuiltinFunc{
		"mes":         builtinMes,
		"next":        builtinNext,
		"close":       builtinClose,
		"close2":      builtinClose2,
		"end":         builtinEnd,
		"set":         builtinSet, //nolint:goconst
		"warp":        builtinWarp,
		"percentheal": builtinPercentHeal,
		"select":      builtinSelect,
	}
}

// argStr returns args[i] coerced to text, or "" when i is out of range.
func argStr(args []Value, i int) string {
	if i < 0 || i >= len(args) {
		return ""
	}
	return args[i].String()
}
