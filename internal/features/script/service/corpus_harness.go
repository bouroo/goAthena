// Package service corpus harness: Phase R0 S5 measurement rail.
//
// RunCorpus executes every script's OnInit label against a fresh VM and
// classifies the per-script outcome. It is the conformance rail for the
// rAthena drop-in compatibility roadmap: today's numbers are the
// baseline that follow-up builtin work (S6+) cites as a delta.
package service

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bouroo/goAthena/internal/features/script/vm"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// Reason constants classify each ScriptOutcome into one of the S5
// buckets. They are stable strings consumed by the integration test
// (for top-N gap logs) and by future PR commit bodies (for delta
// reporting).
const (
	ReasonEnd            = "end"
	ReasonStop           = "stop"
	ReasonUnknownBuiltin = "unknown_builtin"
	ReasonUnknownFunc    = "unknown_func"
	ReasonInstrLimit     = "instr_limit"
	ReasonNoOnInit       = "no_oninit"
	ReasonOther          = "other"
)

// CorpusReport summarizes one RunCorpus sweep.
//
// Invariant: Total == Succeeded + Failed + Skipped. Outcomes for every
// script in the set (including skipped ones) appear in Outcomes so
// callers can audit per-file result without re-running.
type CorpusReport struct {
	Total       int
	Ran         int
	Succeeded   int
	Failed      int
	Skipped     int
	Outcomes    []ScriptOutcome
	BuiltinGaps map[string]int
	FuncGaps    map[string]int
	DurationMS  int64
}

// ScriptOutcome records the result of running one script's OnInit.
type ScriptOutcome struct {
	Name   string
	State  vm.State
	Err    string
	Reason string
}

// quotedName extracts the name between double quotes from a VM error
// of the form `unknown builtin "X"` or `callfunc: unknown function "X"`.
// Returns "" if the error has no quoted name.
var quotedName = regexp.MustCompile(`"([^"]*)"`)

func extractQuotedName(s string) string {
	m := quotedName.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// RunCorpus executes the OnInit label of every script in set that
// declares one and returns a structured report.
//
// Behavior matches the existing RunOnInit runner (shared ScopeStore so
// $ map vars set by one OnInit are visible to others) but the harness
// classifies per-script outcomes instead of collecting errors. One
// failing OnInit never aborts the sweep; the VM's per-script
// instruction limit guards against runaway scripts.
//
// ctx is honored between scripts: a cancelled context causes RunCorpus
// to return immediately with whatever partial report has been
// collected. Cancellation is never recorded as a script failure.
//
// Pure function: no goroutines, no I/O side effects.
func RunCorpus(ctx context.Context, set *script.CompiledScriptSet) *CorpusReport {
	start := time.Now()
	report := &CorpusReport{
		BuiltinGaps: make(map[string]int),
		FuncGaps:    make(map[string]int),
	}

	if set == nil {
		report.DurationMS = time.Since(start).Milliseconds()
		return report
	}

	scopes := vm.NewScopeStore()
	builtins := vm.NewBuiltinRegistry()
	builtins.RegisterDefaults()

	// Gather and sort names for deterministic iteration order so the
	// Outcomes slice and test assertions are stable.
	names := make([]string, 0, len(set.Scripts))
	for name := range set.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		// Honor cancellation between scripts. Cancellation is not a
		// script failure; we stop iterating and return what we have.
		if err := ctx.Err(); err != nil {
			report.DurationMS = time.Since(start).Milliseconds()
			return report
		}

		cs := set.Scripts[name]
		report.Total++
		if _, ok := cs.LookupLabel(onInitLabel); !ok {
			report.Skipped++
			report.Outcomes = append(report.Outcomes, ScriptOutcome{
				Name:   name,
				State:  0,
				Err:    "",
				Reason: ReasonNoOnInit,
			})
			continue
		}

		machine, ok := vm.NewAtLabelWithFuncs(cs, set.Funcs, onInitLabel, scopes, builtins)
		if !ok {
			// LookupLabel said the label exists but NewAtLabelWithFuncs
			// returned false: treat as skip (defensive).
			report.Skipped++
			report.Outcomes = append(report.Outcomes, ScriptOutcome{
				Name:   name,
				Err:    "label lookup race",
				Reason: ReasonNoOnInit,
			})
			continue
		}
		report.Ran++
		state, err := machine.Run(ctx)
		out := ScriptOutcome{
			Name:  name,
			State: state,
		}
		if err != nil {
			out.Err = err.Error()
			out.Reason = classifyError(err.Error(), report)
			report.Failed++
		} else {
			out.Reason = classifyState(state)
			if out.Reason == ReasonStop || out.Reason == ReasonEnd {
				report.Succeeded++
			} else {
				// Run returned without error but the VM is still in
				// StateRun. Treat as failure: the loop should not be
				// terminal.
				report.Failed++
				out.Reason = ReasonOther
			}
		}
		report.Outcomes = append(report.Outcomes, out)
	}

	// Sort Outcomes by Name for deterministic human reading and test
	// assertions. Iteration above is already sorted, but the no-oninit
	// paths and any future short-circuits may break that invariant.
	sort.SliceStable(report.Outcomes, func(i, j int) bool {
		return report.Outcomes[i].Name < report.Outcomes[j].Name
	})

	report.DurationMS = time.Since(start).Milliseconds()
	return report
}

// classifyState maps a VM terminal state to a Reason.
// StateRun should never reach here (Run loops until non-Run); defensively
// returns ReasonOther.
func classifyState(s vm.State) string {
	switch s {
	case vm.StateEnd:
		return ReasonEnd
	case vm.StateStop:
		return ReasonStop
	default:
		return ReasonOther
	}
}

// classifyError maps a VM error string to a Reason and tallies any
// unknown-builtin / unknown-function gaps. The VM emits these via
// fmt.Errorf and not typed errors (see vm.go:543, 580, 127), so we
// match on substrings; this is brittle by design and is flagged for a
// future typed-error refactor — do not branch on these strings for
// control flow elsewhere.
func classifyError(msg string, report *CorpusReport) string {
	switch {
	case strings.Contains(msg, "instruction limit exceeded"):
		return ReasonInstrLimit
	case strings.HasPrefix(msg, "callfunc: unknown function") ||
		strings.Contains(msg, "callfunc: unknown function"):
		if name := extractQuotedName(msg); name != "" {
			report.FuncGaps[name]++
		}
		return ReasonUnknownFunc
	case strings.Contains(msg, "unknown builtin"):
		if name := extractQuotedName(msg); name != "" {
			report.BuiltinGaps[name]++
		}
		return ReasonUnknownBuiltin
	default:
		return ReasonOther
	}
}
