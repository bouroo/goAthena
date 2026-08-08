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

// builtinMenu implements the prompt half of menu("optA",L_a,"optB",L_b,...):
// each argument is one menu option, passed verbatim to Host.Select (no colon
// re-split, unlike builtinSelect). The label jump is a compiler concern —
// compileMenu lowers each prompt,label pair into this builtin for the choice
// followed by an OpJumpIfTrue to the matching label — because a builtin cannot
// set the VM's instruction pointer. Keeping one option per argument preserves
// the positional prompt↔label pairing the jump chain relies on: builtinSelect's
// join+split would let a "a:b" prompt spawn two client rows (ZC_MENU_LIST joins
// options with ":" and the client re-splits) while only one label exists, so the
// returned index could miss its label. Cancel (choice 255) and the resulting
// @menu value are handled by compileMenu's jump chain (no label matches 255).
func builtinMenu(vm *VM, args []Value) (Value, control) {
	options := make([]string, len(args))
	for i, a := range args {
		options[i] = a.String()
	}
	return IntVal(int64(vm.host.Select(options))), ctrlContinue
}

// inputDefaultMax is rAthena's script_config.input_max_value default: INT_MAX
// (script.cpp:6159). With the matching min default of 0, numeric inputs are
// clamped to the non-negative int32 range unless a script overrides the bounds.
const inputDefaultMax int64 = 2147483647

// builtinInput implements input(<var>{,<min>{,<max>}}). rAthena declares it as
// BUILDIN_DEF(input,"r??") (script.cpp:27980): a writable variable reference
// plus optional bounds. The variable's type — a "$" suffix marks a string
// variable (script.cpp is_string_variable) — selects the dialog: string →
// ZC_OPEN_EDITDLGSTR + CZ_INPUT_EDITDLGSTR, numeric → ZC_OPEN_EDITDLG +
// CZ_INPUT_EDITDLG. The result is stored in the named variable (mirroring
// set_reg_* at script.cpp:6180/6186) and the builtin returns rAthena's
// range-check code (0 in-range, -1 below min, +1 above max) — for strings the
// code compares the text length against the bounds. A cancelled, closed, or
// dropped input ends the script (ok=false), mirroring Next() returning false.
//
// Because arg0 is a variable *name* (not its value), input is compiler-lowered
// (compileInput) like set/menu: the identifier is emitted as an OpStr literal so
// the builtin receives the name to store under.
func builtinInput(vm *VM, args []Value) (Value, control) {
	name := argStr(args, 0)
	minVal, maxVal := inputBounds(args)
	if isStringVar(name) {
		text, ok := vm.host.InputStr()
		if !ok {
			return NilVal(), ctrlEnd
		}
		vm.SetVar(name, StrVal(text))
		return IntVal(inputRangeCode(int64(len(text)), minVal, maxVal)), ctrlContinue
	}
	amount, ok := vm.host.Input()
	if !ok {
		return NilVal(), ctrlEnd
	}
	vm.SetVar(name, IntVal(clampInput(amount, minVal, maxVal)))
	return IntVal(inputRangeCode(amount, minVal, maxVal)), ctrlContinue
}

// inputBounds resolves the optional min/max arguments. rAthena defaults to
// [0, INT_MAX] when the bounds are omitted (script.cpp:6158-6159); a script that
// passes only min leaves max at the INT_MAX default.
func inputBounds(args []Value) (minVal, maxVal int64) {
	minVal, maxVal = 0, inputDefaultMax
	if len(args) > 1 {
		minVal = args[1].asInt()
	}
	if len(args) > 2 {
		maxVal = args[2].asInt()
	}
	return minVal, maxVal
}

// isStringVar reports whether name is a rAthena string variable (suffix "$").
func isStringVar(name string) bool { return strings.HasSuffix(name, "$") }

// inputRangeCode returns rAthena's input result code: 0 in-range, -1 below min,
// +1 above max (script.cpp:6181/6187).
func inputRangeCode(v, minVal, maxVal int64) int64 {
	switch {
	case v > maxVal:
		return 1
	case v < minVal:
		return -1
	default:
		return 0
	}
}

// clampInput bounds v to [min, max], matching rAthena's cap_value(amount,min,max)
// applied to the stored numeric value (script.cpp:6186). The range code is
// computed against the original, unclamped value.
func clampInput(v, minVal, maxVal int64) int64 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
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
		// prompt() is select()'s paginated ("Prev/Next") sibling — same
		// return-the-index semantics, no label jump.
		"prompt": builtinSelect, //nolint:goconst
		"menu":   builtinMenu,   //nolint:goconst
		"input":  builtinInput,  //nolint:goconst
	}
}

// argStr returns args[i] coerced to text, or "" when i is out of range.
func argStr(args []Value, i int) string {
	if i < 0 || i >= len(args) {
		return ""
	}
	return args[i].String()
}
