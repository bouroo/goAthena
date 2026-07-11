// Package service provides zone-side orchestration of the script engine:
// running one-time NPC initialization (OnInit) at startup and, in later
// phases, driving per-connection dialog VMs.
package service

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/script/vm"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// onInitLabel is the rAthena convention for the label executed once when
// an NPC is loaded. See rAthena src/map/npc.cpp npc_event_do_oninit.
const onInitLabel = "OnInit"

// RunOnInit executes the OnInit label of every script in set that defines
// one. A single shared ScopeStore is created once and threaded through
// every per-script VM so that map-scope ($) variables set in one OnInit
// are visible to others — this matches rAthena's behavior where $vars are
// shared across all NPCs on a map. Per-character, account, and instance
// scopes (.@, #, ', $@) are still per-VM in semantics; rAthena resets them
// per NPC load, but for this phase they share the same store too. Errors
// from individual scripts are collected and returned; one failing OnInit
// never aborts the batch.
//
// Returns the shared ScopeStore (any $ map vars set by OnInit are
// preserved here for future per-connection dialog VMs in Phase 4b), the
// number of OnInit scripts that ran (attempted), and any per-script
// errors. The ScopeStore is always non-nil: when set is nil, a fresh
// empty store is returned so callers can persist it unconditionally.
// OnInit runs at zone startup, single-threaded, with no player context —
// it is server/map initialization, not per-character logic.
func RunOnInit(ctx context.Context, set *script.CompiledScriptSet, logger *zerolog.Logger) (*vm.ScopeStore, int, []error) {
	scopes := vm.NewScopeStore()
	if set == nil {
		return scopes, 0, nil
	}
	ran, errs := runOnInitWithScope(ctx, set, scopes, logger)
	return scopes, ran, errs
}

// runOnInitWithScope is the testable inner worker: it takes the shared
// ScopeStore explicitly so callers (notably tests) can inspect post-run
// state. Production callers go through RunOnInit, which supplies a fresh
// store.
func runOnInitWithScope(
	ctx context.Context,
	set *script.CompiledScriptSet,
	scopes *vm.ScopeStore,
	logger *zerolog.Logger,
) (ran int, errs []error) {
	if set == nil {
		return 0, nil
	}
	builtins := vm.NewBuiltinRegistry()
	builtins.RegisterDefaults()

	// Deterministic iteration is not required for correctness but helps
	// reproducible logs; map iteration order is fine for now.
	for name, cs := range set.Scripts {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			return ran, errs
		}
		if _, ok := cs.LookupLabel(onInitLabel); !ok {
			continue
		}
		machine, ok := vm.NewAtLabel(cs, onInitLabel, scopes, builtins)
		if !ok {
			continue
		}
		ran++
		if _, err := machine.Run(ctx); err != nil {
			wrapped := fmt.Errorf("OnInit %s: %w", name, err)
			errs = append(errs, wrapped)
			if logger != nil {
				logger.Warn().Err(err).Str("script", name).Msg("script: OnInit execution failed")
			}
			continue
		}
	}
	if logger != nil {
		logger.Info().
			Int("oninit_ran", ran).
			Int("oninit_errors", len(errs)).
			Msg("script: OnInit batch complete")
	}
	return ran, errs
}
