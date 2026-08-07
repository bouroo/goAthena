//go:build unit

package script

import (
	"testing"
)

// fakeHost records every Host call so VM tests can assert dialog output and
// effects without a network. Next returns true (advance) so dialog scripts
// proceed; a future test can flip nextOK to model a cancelled dialog.
type fakeHost struct {
	mes            []string
	nexts          int
	closed         bool
	warps          []warpCall
	heals          []healCall
	selects        [][]string
	inputs         int
	inputStrs      int
	nextOK         bool
	selectChoice   int
	inputResult    int64
	inputOK        bool
	inputStrResult string
	inputStrOK     bool
}

type warpCall struct {
	mapName string
	x, y    int
}

type healCall struct{ hp, sp int }

func newFakeHost() *fakeHost {
	return &fakeHost{nextOK: true, inputOK: true, inputStrOK: true}
}

func (h *fakeHost) Mes(msg string)          { h.mes = append(h.mes, msg) }
func (h *fakeHost) Next() bool              { h.nexts++; return h.nextOK }
func (h *fakeHost) Close()                  { h.closed = true }
func (h *fakeHost) Warp(m string, x, y int) { h.warps = append(h.warps, warpCall{m, x, y}) }
func (h *fakeHost) PercentHeal(hp, sp int)  { h.heals = append(h.heals, healCall{hp, sp}) }
func (h *fakeHost) Select(options []string) int {
	h.selects = append(h.selects, options)
	return h.selectChoice
}

func (h *fakeHost) Input() (int64, bool) {
	h.inputs++
	return h.inputResult, h.inputOK
}

func (h *fakeHost) InputStr() (string, bool) {
	h.inputStrs++
	return h.inputStrResult, h.inputStrOK
}

// runFirstScript compiles src, takes the first script in the set, and runs it
// against host with the given initial variables.
func runFirstScript(t *testing.T, src string, host Host, vars map[string]Value) map[string]Value {
	t.Helper()
	set, err := Compile([]byte(src))
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if len(set.Scripts) == 0 {
		t.Fatalf("no scripts compiled from:\n%s", src)
	}
	var cs *CompiledScript
	for _, s := range set.Scripts {
		cs = s
		break
	}
	vm := NewVM(cs, host, DefaultBuiltins(), vars)
	if err := vm.Run(); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	return vm.Vars()
}

func TestVMDialogSequence(t *testing.T) {
	const src = "-\tscript\tHealer\t-1,{\n" +
		`mes "[Healer]";` + "\n" +
		`mes "I will heal you.";` + "\n" +
		`next;` + "\n" +
		`percentheal 100,100;` + "\n" +
		`mes "You are fully healed!";` + "\n" +
		`close;` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)

	want := []string{"[Healer]", "I will heal you.", "You are fully healed!"}
	if len(h.mes) != len(want) {
		t.Fatalf("mes lines = %v, want %v", h.mes, want)
	}
	for i, w := range want {
		if h.mes[i] != w {
			t.Errorf("mes[%d] = %q, want %q", i, h.mes[i], w)
		}
	}
	if h.nexts != 1 {
		t.Errorf("next count = %d, want 1", h.nexts)
	}
	if len(h.heals) != 1 || h.heals[0] != (healCall{100, 100}) {
		t.Errorf("heals = %v, want [{100 100}]", h.heals)
	}
	if !h.closed {
		t.Error("expected dialog closed")
	}
}

func TestVMIfFalseBranch(t *testing.T) {
	// .@Price = 0 ⇒ the if-body must NOT execute (no "costs" line).
	const src = "-\tscript\tN\t-1,{\n" +
		".@Price = 0;\n" +
		`if (.@Price > 0) mes "costs";` + "\n" +
		`mes "done";` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	if len(h.mes) != 1 || h.mes[0] != "done" {
		t.Errorf("mes = %v, want [done] (if-body skipped)", h.mes)
	}
}

func TestVMIfTrueBranch(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		".@Price = 50;\n" +
		`if (.@Price > 0) mes "costs";` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	if len(h.mes) != 1 || h.mes[0] != "costs" {
		t.Errorf("mes = %v, want [costs]", h.mes)
	}
}

func TestVMIfElse(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		".@n = 0;\n" +
		`if (.@n == 1) mes "one"; else mes "other";` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	if len(h.mes) != 1 || h.mes[0] != "other" {
		t.Errorf("mes = %v, want [other]", h.mes)
	}
}

func TestVMStringConcat(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		".@n = 42;\n" +
		`mes "value is " + .@n + "!";` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	if len(h.mes) != 1 || h.mes[0] != "value is 42!" {
		t.Errorf("mes = %v, want [value is 42!]", h.mes)
	}
}

func TestVMCompoundAssign(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		"Zeny = 100;\n" +
		"Zeny += 50;\n" +
		"Zeny -= 30;\n" +
		`mes "Zeny: " + Zeny;` + "\n" +
		"}\n"
	h := newFakeHost()
	vars := runFirstScript(t, src, h, nil)
	if h.mes[0] != "Zeny: 120" {
		t.Errorf("mes = %q, want 'Zeny: 120'", h.mes[0])
	}
	if vars["Zeny"].Int != 120 {
		t.Errorf("Zeny var = %d, want 120", vars["Zeny"].Int)
	}
}

func TestVMSetBuiltin(t *testing.T) {
	// `set var, value;` desugars to assignment.
	const src = "-\tscript\tN\t-1,{\n" +
		`set .@Count, 7;` + "\n" +
		`mes "count " + .@Count;` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	if h.mes[0] != "count 7" {
		t.Errorf("mes = %q, want 'count 7'", h.mes[0])
	}
}

func TestVMLogicalAndOr(t *testing.T) {
	// 1 && 0 → 0 (falsy) ⇒ "no". Short-circuit: rhs evaluated only when lhs truthy.
	cases := []struct {
		name string
		cond string
		want string // "yes" if cond truthy else "no"
	}{
		{"and-true", "1 && 1", "yes"},
		{"and-false", "1 && 0", "no"},
		{"or-true", "0 || 1", "yes"},
		{"or-false", "0 || 0", "no"},
		{"and-short", "0 && 1", "no"},
		{"or-short", "1 || 0", "yes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "-\tscript\tN\t-1,{\nif (" + tc.cond + ") mes \"yes\"; else mes \"no\";\n}\n"
			h := newFakeHost()
			runFirstScript(t, src, h, nil)
			if len(h.mes) != 1 || h.mes[0] != tc.want {
				t.Errorf("cond %q → mes %v, want [%s]", tc.cond, h.mes, tc.want)
			}
		})
	}
}

func TestVMWhileLoop(t *testing.T) {
	// Count 0..3, mes each value.
	const src = "-\tscript\tN\t-1,{\n" +
		".@i = 0;\n" +
		"while (.@i < 3) {\n" +
		`mes "i=" + .@i;` + "\n" +
		".@i += 1;\n" +
		"}\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	want := []string{"i=0", "i=1", "i=2"}
	if len(h.mes) != len(want) {
		t.Fatalf("mes = %v, want %v", h.mes, want)
	}
	for i, w := range want {
		if h.mes[i] != w {
			t.Errorf("mes[%d] = %q, want %q", i, h.mes[i], w)
		}
	}
}

func TestVMForLoop(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		"for (.@i = 0; .@i < 2; .@i += 1) {\n" +
		`mes "loop " + .@i;` + "\n" +
		"}\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	want := []string{"loop 0", "loop 1"}
	if len(h.mes) != len(want) {
		t.Fatalf("mes = %v, want %v", h.mes, want)
	}
}

func TestVMGotoLabel(t *testing.T) {
	// goto skips the middle statement.
	const src = "-\tscript\tN\t-1,{\n" +
		`mes "a";` + "\n" +
		"goto L_End;\n" +
		`mes "b";` + "\n" +
		"L_End:\n" +
		`mes "c";` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	want := []string{"a", "c"}
	if len(h.mes) != len(want) {
		t.Fatalf("mes = %v, want %v", h.mes, want)
	}
}

func TestVMEndTerminates(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		`mes "before";` + "\n" +
		`end;` + "\n" +
		`mes "after";` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	if len(h.mes) != 1 || h.mes[0] != "before" {
		t.Errorf("mes = %v, want [before] (end stops)", h.mes)
	}
}

func TestVMWarpBuiltin(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		`warp "geffen", 120, 150;` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	if len(h.warps) != 1 || h.warps[0] != (warpCall{"geffen", 120, 150}) {
		t.Errorf("warps = %v, want [{geffen 120 150}]", h.warps)
	}
}

func TestVMArithmeticVectors(t *testing.T) {
	cases := []struct {
		expr string
		want int64
	}{
		{"2 + 3", 5},
		{"10 - 4", 6},
		{"6 * 7", 42},
		{"20 / 4", 5},
		{"17 % 5", 2},
		{"2 + 3 * 4", 14},
		{"(2 + 3) * 4", 20},
		{"-5 + 8", 3},
		{"!0", 1},
		{"!7", 0},
		{"5 == 5", 1},
		{"5 == 6", 0},
		{"5 != 6", 1},
		{"5 > 6", 0},
		{"6 > 5", 1},
		{"5 <= 5", 1},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			src := "-\tscript\tN\t-1,{\n." +
				"@r = " + tc.expr + ";\n" +
				`mes "r=" + .@r;` + "\n}\n"
			h := newFakeHost()
			vars := runFirstScript(t, src, h, nil)
			got := vars[".@r"].Int
			if got != tc.want {
				t.Errorf("expr %q = %d, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestVMNextCancelledEndsScript(t *testing.T) {
	// When the client cancels at `next`, the script must stop without the
	// trailing mes.
	const src = "-\tscript\tN\t-1,{\n" +
		`mes "first";` + "\n" +
		`next;` + "\n" +
		`mes "second";` + "\n" +
		"}\n"
	h := newFakeHost()
	h.nextOK = false // model a cancelled/failed next
	runFirstScript(t, src, h, nil)
	if len(h.mes) != 1 || h.mes[0] != "first" {
		t.Errorf("mes = %v, want [first] (next cancelled stops script)", h.mes)
	}
}

func TestVMHealerFlowWithSeedVars(t *testing.T) {
	// A healer with a non-zero price the player CAN afford: charge and heal.
	const src = "-\tscript\tN\t-1,{\n" +
		".@Price = 50;\n" +
		"if (.@Price > 0) {\n" +
		"if (Zeny < .@Price)\n" +
		`mes "too poor";` + "\n" +
		"else {\n" +
		"Zeny -= .@Price;\n" +
		`mes "charged " + .@Price;` + "\n" +
		"}\n" +
		"}\n" +
		"if (Zeny >= 0)\n" +
		"percentheal 100,100;\n" +
		"}\n"
	h := newFakeHost()
	vars := runFirstScript(t, src, h, map[string]Value{"Zeny": IntVal(100)})
	if len(h.mes) != 1 || h.mes[0] != "charged 50" {
		t.Errorf("mes = %v, want [charged 50]", h.mes)
	}
	if vars["Zeny"].Int != 50 {
		t.Errorf("Zeny = %d, want 50 (100-50)", vars["Zeny"].Int)
	}
	if len(h.heals) != 1 {
		t.Errorf("heals = %v, want 1 heal", h.heals)
	}
}

// TestVMSelectReturnsChoice proves the VM-stack change that select() relies on:
// select is an expression, so its result must reach the assignment rather than
// being discarded by OpFunc. It also checks the option list is the flat
// colon-split of the argument (select("Stay:Leave") → ["Stay","Leave"]).
func TestVMSelectReturnsChoice(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		`.@c = select("Stay:Leave");` + "\n" +
		`mes "picked " + .@c;` + "\n" +
		"}\n"
	h := newFakeHost()
	h.selectChoice = 2
	vars := runFirstScript(t, src, h, nil)
	if len(h.selects) != 1 {
		t.Fatalf("selects = %v, want one call", h.selects)
	}
	got := h.selects[0]
	if len(got) != 2 || got[0] != "Stay" || got[1] != "Leave" {
		t.Errorf("options = %v, want [Stay Leave]", got)
	}
	if vars[".@c"].Int != 2 {
		t.Errorf(".@c = %d, want 2", vars[".@c"].Int)
	}
	if len(h.mes) != 1 || h.mes[0] != "picked 2" {
		t.Errorf("mes = %v, want [picked 2]", h.mes)
	}
}

// TestVMSelectCancelReturnsFF proves cancel surfaces as 255 (0xff), the value
// rAthena's clif_parse_NpcSelectMenu sends and scripts test for.
func TestVMSelectCancelReturnsFF(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		`.@c = select("A:B");` + "\n" +
		`mes "got " + .@c;` + "\n" +
		"}\n"
	h := newFakeHost()
	h.selectChoice = 255
	vars := runFirstScript(t, src, h, nil)
	if vars[".@c"].Int != 255 {
		t.Errorf(".@c = %d, want 255 (cancel)", vars[".@c"].Int)
	}
}

// TestVMStatementCallNoLeak guards the OpPop change: a dialog built only of
// statement-position void calls (mes/next/close) must run cleanly — if OpFunc
// pushed without the statement-path OpPop, the stack would grow per call. The
// assertion is implicit (Run does not panic / misbehave); an explicit mes
// check confirms the dialog executed top-to-bottom.
func TestVMStatementCallNoLeak(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		`mes "a";` + "\n" +
		`mes "b";` + "\n" +
		`mes "c";` + "\n" +
		"}\n"
	h := newFakeHost()
	runFirstScript(t, src, h, nil)
	if len(h.mes) != 3 || h.mes[0] != "a" || h.mes[2] != "c" {
		t.Errorf("mes = %v, want [a b c]", h.mes)
	}
}

// menuScript is a two-option menu whose cancel path falls through to a final
// mes. Each label ends with close so a jump there halts; a non-matching choice
// (cancel) skips the labels and runs the fall-through line.
const menuScript = "-\tscript\tN\t-1,{\n" +
	`menu("Stay",L_stay,"Leave",L_leave);` + "\n" +
	`mes "cancelled";` + "\n" +
	`close;` + "\n" +
	`L_stay:` + "\n" +
	`mes "staying";` + "\n" +
	`close;` + "\n" +
	`L_leave:` + "\n" +
	`mes "leaving";` + "\n" +
	`close;` + "\n" +
	"}\n"

// TestVMMenuJumpsToSelectedLabel proves the compiler-lowered jump chain: choice
// 2 must goto L_leave, skipping the fall-through and L_stay. It also asserts
// builtinMenu passes each prompt as one option (no colon re-split, unlike
// builtinSelect) and stores the choice in @menu.
func TestVMMenuJumpsToSelectedLabel(t *testing.T) {
	h := newFakeHost()
	h.selectChoice = 2
	vars := runFirstScript(t, menuScript, h, nil)
	if len(h.selects) != 1 || len(h.selects[0]) != 2 ||
		h.selects[0][0] != "Stay" || h.selects[0][1] != "Leave" {
		t.Errorf("options = %v, want [Stay Leave] (one option per prompt)", h.selects)
	}
	if len(h.mes) != 1 || h.mes[0] != "leaving" {
		t.Errorf("mes = %v, want [leaving] (choice 2 → L_leave)", h.mes)
	}
	if vars["@menu"].Int != 2 {
		t.Errorf("@menu = %d, want 2", vars["@menu"].Int)
	}
	if !h.closed {
		t.Error("expected dialog closed")
	}
}

// TestVMMenuFirstOption covers the first label (i+1 == 1) so the jump chain's
// initial comparison is exercised, not just the second.
func TestVMMenuFirstOption(t *testing.T) {
	h := newFakeHost()
	h.selectChoice = 1
	vars := runFirstScript(t, menuScript, h, nil)
	if len(h.mes) != 1 || h.mes[0] != "staying" {
		t.Errorf("mes = %v, want [staying] (choice 1 → L_stay)", h.mes)
	}
	if vars["@menu"].Int != 1 {
		t.Errorf("@menu = %d, want 1", vars["@menu"].Int)
	}
}

// TestVMMenuCancelFallsThrough proves cancel (255) matches no label, so the
// menu falls through to the statement after it — rAthena's cancel semantics.
func TestVMMenuCancelFallsThrough(t *testing.T) {
	h := newFakeHost()
	h.selectChoice = 255
	vars := runFirstScript(t, menuScript, h, nil)
	if len(h.mes) != 1 || h.mes[0] != "cancelled" {
		t.Errorf("mes = %v, want [cancelled] (cancel → fall through)", h.mes)
	}
	if vars["@menu"].Int != 255 {
		t.Errorf("@menu = %d, want 255 (cancel)", vars["@menu"].Int)
	}
}

// TestVMPromptReturnsChoice proves prompt() behaves like select() — returns the
// 1-based index and colon-splits its argument — rather than jumping to a label.
func TestVMPromptReturnsChoice(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		`.@p = prompt("A:B");` + "\n" +
		`mes "got " + .@p;` + "\n" +
		"}\n"
	h := newFakeHost()
	h.selectChoice = 2
	vars := runFirstScript(t, src, h, nil)
	if len(h.selects) != 1 || len(h.selects[0]) != 2 ||
		h.selects[0][0] != "A" || h.selects[0][1] != "B" {
		t.Errorf("options = %v, want [A B] (prompt colon-splits like select)", h.selects)
	}
	if vars[".@p"].Int != 2 {
		t.Errorf(".@p = %d, want 2", vars[".@p"].Int)
	}
	if len(h.mes) != 1 || h.mes[0] != "got 2" {
		t.Errorf("mes = %v, want [got 2]", h.mes)
	}
}

func TestVMInputNumeric(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		"input(.@donate);\n" +
		`mes "got " + .@donate;` + "\n" +
		"}\n"
	h := newFakeHost()
	h.inputResult = 500
	vars := runFirstScript(t, src, h, nil)
	if h.inputs != 1 {
		t.Fatalf("inputs = %d, want 1", h.inputs)
	}
	if vars[".@donate"].Int != 500 {
		t.Errorf(".@donate = %d, want 500", vars[".@donate"].Int)
	}
	if len(h.mes) != 1 || h.mes[0] != "got 500" {
		t.Errorf("mes = %v, want [got 500]", h.mes)
	}
}

func TestVMInputString(t *testing.T) {
	const src = "-\tscript\tN\t-1,{\n" +
		"input(.@name$);\n" +
		`mes "hi " + .@name$;` + "\n" +
		"}\n"
	h := newFakeHost()
	h.inputStrResult = "Bob"
	vars := runFirstScript(t, src, h, nil)
	if h.inputStrs != 1 {
		t.Fatalf("inputStrs = %d, want 1", h.inputStrs)
	}
	if vars[".@name$"].Str != "Bob" {
		t.Errorf(".@name$ = %q, want Bob", vars[".@name$"].Str)
	}
	if len(h.mes) != 1 || h.mes[0] != "hi Bob" {
		t.Errorf("mes = %v, want [hi Bob]", h.mes)
	}
}

func TestVMInputCancelEndsScript(t *testing.T) {
	// A cancelled/closed input ends the script (ok=false → ctrlEnd): the mes
	// after input must NOT run.
	const src = "-\tscript\tN\t-1,{\n" +
		"input(.@donate);\n" +
		`mes "after";` + "\n" +
		"}\n"
	h := newFakeHost()
	h.inputOK = false
	runFirstScript(t, src, h, nil)
	if h.inputs != 1 {
		t.Errorf("inputs = %d, want 1", h.inputs)
	}
	if len(h.mes) != 0 {
		t.Errorf("mes = %v, want none (cancelled input ends the script)", h.mes)
	}
}

func TestVMInputClampBounds(t *testing.T) {
	cases := []struct {
		name   string
		result int64
		want   int64
	}{
		{"above max clamps to max", 150, 100},
		{"below min clamps to min", 5, 10},
		{"in-range stored as-is", 42, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const src = "-\tscript\tN\t-1,{\n" +
				"input(.@x, 10, 100);\n" +
				"}\n"
			h := newFakeHost()
			h.inputResult = tc.result
			vars := runFirstScript(t, src, h, nil)
			if vars[".@x"].Int != tc.want {
				t.Errorf(".@x = %d, want %d (input %d)", vars[".@x"].Int, tc.want, tc.result)
			}
		})
	}
}

func TestVMInputDefaultClampsNegative(t *testing.T) {
	// With no explicit bounds, rAthena defaults to [0, INT_MAX], so a negative
	// input is clamped to 0 (script_config.input_min_value default).
	const src = "-\tscript\tN\t-1,{\n" +
		"input(.@amt);\n" +
		"}\n"
	h := newFakeHost()
	h.inputResult = -50
	vars := runFirstScript(t, src, h, nil)
	if vars[".@amt"].Int != 0 {
		t.Errorf(".@amt = %d, want 0 (default min clamps negative)", vars[".@amt"].Int)
	}
}
