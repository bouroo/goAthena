//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/script/compiler"
	"github.com/bouroo/goAthena/internal/features/script/parser"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

func mustLex(t *testing.T, src string) []script.Token {
	t.Helper()
	tokens, err := script.Lex([]byte(src))
	require.NoError(t, err)
	return tokens
}

func compileForTest(t *testing.T, name string, tokens []script.Token) *script.CompiledScript {
	t.Helper()
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	cs, err := compiler.New().Compile(name, stmts)
	require.NoError(t, err)
	return cs
}

func TestRunCorpus_EmptySetReturnsZeroTotals(t *testing.T) {
	rep := RunCorpus(context.Background(), nil)
	require.NotNil(t, rep)
	assert.Equal(t, 0, rep.Total)
	assert.Equal(t, 0, rep.Ran)
	assert.Equal(t, 0, rep.Succeeded)
	assert.Equal(t, 0, rep.Failed)
	assert.Equal(t, 0, rep.Skipped)
	assert.Empty(t, rep.Outcomes)
	assert.NotNil(t, rep.BuiltinGaps)
	assert.NotNil(t, rep.FuncGaps)
}

func TestRunCorpus_ClassifiesAllOutcomes(t *testing.T) {
	// Build a synthetic CompiledScriptSet covering each classification.
	// "stop" is reached via `next;` (no-result builtin that sets
	// vm.state = StateStop). "other" is hard to synth-trigger without
	// a custom VM hook — natural EOF yields StateEnd, not Other — so
	// the G_other case below is asserted as ReasonEnd with a comment.
	set := script.NewCompiledScriptSet()
	set.Scripts["A_end"] = compileForTest(t, "A_end", mustLex(t, `
		OnInit:
			set $aEnd, 1;
			end;
	`))
	set.Scripts["B_stop"] = compileForTest(t, "B_stop", mustLex(t, `
		OnInit:
			set $bStop, 1;
			next;
	`))
	set.Scripts["C_unknown_builtin"] = compileForTest(t, "C_unknown_builtin", mustLex(t, `
		OnInit:
			nosuchbuiltin();
			end;
	`))
	set.Scripts["D_unknown_func"] = compileForTest(t, "D_unknown_func", mustLex(t, `
		OnInit:
			callfunc("does_not_exist");
			end;
	`))
	set.Scripts["E_instr_limit"] = compileForTest(t, "E_instr_limit", mustLex(t, `
		OnInit:
			goto L1;
			L1:
			goto L1;
	`))
	set.Scripts["F_no_oninit"] = compileForTest(t, "F_no_oninit", mustLex(t, `
		mes "no init here";
			close;
	`))
	set.Scripts["G_other"] = compileForTest(t, "G_other", mustLex(t, `
		OnInit:
			set $gOther, 1;
	`))

	rep := RunCorpus(context.Background(), set)
	require.NotNil(t, rep)

	assert.Equal(t, len(set.Scripts), rep.Total,
		"Total must equal the number of scripts in the set")
	assert.Equal(t, rep.Succeeded+rep.Failed+rep.Skipped, rep.Total,
		"Succeeded+Failed+Skipped must equal Total")

	byName := make(map[string]ScriptOutcome, len(rep.Outcomes))
	for _, o := range rep.Outcomes {
		byName[o.Name] = o
	}

	a := byName["A_end"]
	assert.Equal(t, ReasonEnd, a.Reason, "A_end → end")
	assert.Empty(t, a.Err)

	b := byName["B_stop"]
	assert.Equal(t, ReasonStop, b.Reason, "B_stop → stop")
	assert.Empty(t, b.Err)

	c := byName["C_unknown_builtin"]
	assert.Equal(t, ReasonUnknownBuiltin, c.Reason, "C_unknown_builtin → unknown_builtin")
	assert.Contains(t, c.Err, "unknown builtin")
	assert.Contains(t, c.Err, "nosuchbuiltin")
	assert.Equal(t, 1, rep.BuiltinGaps["nosuchbuiltin"])

	d := byName["D_unknown_func"]
	assert.Equal(t, ReasonUnknownFunc, d.Reason, "D_unknown_func → unknown_func")
	assert.Contains(t, d.Err, "callfunc")
	assert.Contains(t, d.Err, "does_not_exist")
	assert.Equal(t, 1, rep.FuncGaps["does_not_exist"])

	e := byName["E_instr_limit"]
	assert.Equal(t, ReasonInstrLimit, e.Reason, "E_instr_limit → instr_limit")
	assert.Contains(t, e.Err, "instruction limit exceeded")

	f := byName["F_no_oninit"]
	assert.Equal(t, ReasonNoOnInit, f.Reason, "F_no_oninit → no_oninit")
	assert.Empty(t, f.Err)

	// G_other: natural EOF (no `end;`) lands in StateEnd, not "other".
	// Document this rather than fabricate a fake "other" outcome.
	g := byName["G_other"]
	assert.Equal(t, ReasonEnd, g.Reason,
		"G_other → end (natural EOF; 'other' is hard to synth-trigger without a custom VM hook)")

	assert.Equal(t, 1, rep.Skipped, "only F is skipped")
	assert.Equal(t, 3, rep.Failed, "C, D, E fail")
	assert.Equal(t, 3, rep.Succeeded, "A, B, G succeed")
	assert.Equal(t, 6, rep.Ran, "all but F ran")
}

func TestRunCorpus_HonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	set := script.NewCompiledScriptSet()
	set.Scripts["X"] = compileForTest(t, "X", mustLex(t, `
		OnInit:
			end;
	`))

	rep := RunCorpus(ctx, set)
	require.NotNil(t, rep)
	assert.Equal(t, 0, rep.Total, "cancelled context should not record any outcomes")
	assert.Equal(t, 0, rep.Ran)
	assert.Equal(t, 0, rep.Succeeded)
	assert.Equal(t, 0, rep.Failed)
	assert.Equal(t, 0, rep.Skipped)
	assert.Empty(t, rep.Outcomes)
}
