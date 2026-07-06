package script

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/script/compiler"
	"github.com/bouroo/goAthena/internal/features/script/loader"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// Engine manages the compiled script set with atomic hot-reload.
type Engine struct {
	current   atomic.Pointer[script.CompiledScriptSet]
	logger    *zerolog.Logger
	scriptDir string
}

// NewEngine creates a new script engine.
func NewEngine(logger *zerolog.Logger, scriptDir string) *Engine {
	e := &Engine{
		logger:    logger,
		scriptDir: scriptDir,
	}
	e.current.Store(script.NewCompiledScriptSet())
	return e
}

// LoadAndCompile reads all scripts from the script directory,
// parses and compiles them, and returns the compiled set.
// This is the "compile in background" step before a swap.
func (e *Engine) LoadAndCompile(ctx context.Context) (*script.CompiledScriptSet, error) {
	results, err := loader.LoadDir(e.scriptDir)
	if err != nil {
		return nil, fmt.Errorf("load scripts: %w", err)
	}

	set := script.NewCompiledScriptSet()
	var compileErrs []error

	for _, r := range results {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("load and compile cancelled: %w", err)
		}

		if err := e.compileResult(r, set); err != nil {
			compileErrs = append(compileErrs, err)
		}
	}

	if len(compileErrs) > 0 && len(set.Scripts) == 0 && len(set.Funcs) == 0 && len(set.Warps) == 0 && len(set.Shops) == 0 {
		return nil, fmt.Errorf("all %d definitions failed to compile: %w", len(compileErrs), compileErrs[0])
	}

	if len(compileErrs) > 0 && e.logger != nil {
		e.logger.Warn().Int("errors", len(compileErrs)).Str("dir", e.scriptDir).Msg("script compile errors during reload")
	}

	return set, nil
}

func (e *Engine) compileResult(r loader.LoadResult, set *script.CompiledScriptSet) error {
	typ := ""
	if r.Header != nil {
		typ = r.Header.Type
	}
	typ = lowerASCII(typ)

	switch typ {
	case "script", "":
		return e.compileScriptResult(r, set)
	case "function":
		return e.compileFunctionResult(r, set)
	case "warp":
		return e.addWarpResult(r, set)
	case "shop":
		return e.addShopResult(r, set)
	default:
		// Other types (monster, duplicate, mapflag) are ignored by this loader.
		return nil
	}
}

func (e *Engine) compileScriptResult(r loader.LoadResult, set *script.CompiledScriptSet) error {
	if r.ParseErr != nil {
		return r.ParseErr
	}
	if r.Header == nil {
		return nil
	}
	name := uniqueName(r.Header)
	cs, err := compiler.New().Compile(name, r.Body)
	if err != nil {
		return fmt.Errorf("compile %s at %s:%d: %w", name, r.File, r.Line, err)
	}
	set.Scripts[name] = cs
	return nil
}

func (e *Engine) compileFunctionResult(r loader.LoadResult, set *script.CompiledScriptSet) error {
	if r.ParseErr != nil {
		return r.ParseErr
	}
	name := r.Header.Name
	if name == "" {
		return nil
	}
	cs, err := compiler.New().Compile(name, r.Body)
	if err != nil {
		return fmt.Errorf("compile function %s at %s:%d: %w", name, r.File, r.Line, err)
	}
	set.Funcs[name] = cs
	return nil
}

func (e *Engine) addWarpResult(r loader.LoadResult, set *script.CompiledScriptSet) error {
	w, err := buildWarpDef(r.Header)
	if err != nil {
		return fmt.Errorf("warp %s at %s:%d: %w", r.Header.Name, r.File, r.Line, err)
	}
	set.Warps = append(set.Warps, w)
	return nil
}

func (e *Engine) addShopResult(r loader.LoadResult, set *script.CompiledScriptSet) error {
	s, err := buildShopDef(r.Header)
	if err != nil {
		return fmt.Errorf("shop %s at %s:%d: %w", r.Header.Name, r.File, r.Line, err)
	}
	set.Shops = append(set.Shops, s)
	return nil
}

// Reload atomically swaps the current compiled set with a new one.
// In-flight VMs holding the old set continue unaffected.
func (e *Engine) Reload(ctx context.Context) error {
	newSet, err := e.LoadAndCompile(ctx)
	if err != nil {
		return err
	}
	old := e.current.Swap(newSet)
	if e.logger != nil {
		stats := newSetStats(newSet)
		e.logger.Info().
			Int("scripts", stats.scripts).
			Int("funcs", stats.funcs).
			Int("warps", stats.warps).
			Int("shops", stats.shops).
			Bool("hadOld", old != nil).
			Msg("script engine reloaded")
	}
	return nil
}

// Current returns the current compiled script set.
func (e *Engine) Current() *script.CompiledScriptSet {
	return e.current.Load()
}

func buildWarpDef(h *script.NPCHeader) (script.WarpDef, error) {
	_, destMap, destX, destY, err := loader.ParseWarpDest(h.SpriteName)
	if err != nil {
		return script.WarpDef{}, fmt.Errorf("parse warp dest: %w", err)
	}
	return script.WarpDef{
		MapName:  h.MapName,
		X:        h.X,
		Y:        h.Y,
		TriggerX: h.TriggerX,
		TriggerY: h.TriggerY,
		DestMap:  destMap,
		DestX:    destX,
		DestY:    destY,
	}, nil
}

func buildShopDef(h *script.NPCHeader) (script.ShopDef, error) {
	spriteID, items, err := loader.ParseShopItems(h.SpriteName)
	_ = spriteID
	if err != nil {
		return script.ShopDef{}, fmt.Errorf("parse shop items: %w", err)
	}
	return script.ShopDef{
		Name:    h.Name,
		MapName: h.MapName,
		X:       h.X,
		Y:       h.Y,
		Items:   items,
	}, nil
}

// uniqueName returns the unique suffix when present, otherwise the display name.
func uniqueName(h *script.NPCHeader) string {
	if h.SpriteName != "" {
		return h.SpriteName
	}
	return h.Name
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

type setStats struct {
	scripts int
	funcs   int
	warps   int
	shops   int
}

func newSetStats(set *script.CompiledScriptSet) setStats {
	if set == nil {
		return setStats{}
	}
	return setStats{
		scripts: len(set.Scripts),
		funcs:   len(set.Funcs),
		warps:   len(set.Warps),
		shops:   len(set.Shops),
	}
}
