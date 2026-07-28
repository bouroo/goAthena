package app

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

// ScriptStore holds the compiled VM state ready to be instanced per dialog,
// plus the flat list of placed NPCs to be published into the world's AOI.
type ScriptStore struct {
	Set  *script.CompiledScriptSet
	NPCs []NPCInfo
}

// NPCInfo carries the minimal metadata needed to publish the NPC into the world.
type NPCInfo struct {
	Name     string
	MapName  string // The map the NPC is on (e.g. "prontera")
	X        int16  // X coordinate
	Y        int16  // Y coordinate
	Facing   uint8  // Facing direction
	SpriteID uint16 // Numeric sprite ID
}

// LoadScripts locates, reads, parses, and compiles all .txt script files found
// in a directory tree. Per-file failures (open, read, parse, compile) are
// logged as warnings via lg and skipped — one bad or unsupported script must
// not abort the whole walk, matching the documented graceful-degradation
// contract used by world/di.go's loadMobDB/loadItemDB. The returned ScriptStore
// contains every file that did compile; nil if no file did.
//
// Only a catastrophic walk error (e.g. root unreadable) is propagated; even
// then, any scripts collected before the failure are returned alongside so the
// caller can degrade rather than fail boot.
//
// When lg is nil, warnings are silently dropped (kept here so callers that
// have not yet resolved a logger, e.g. tests, can still exercise the loader).
func LoadScripts(root string, lg *zerolog.Logger) (*ScriptStore, error) {
	set := script.NewCompiledScriptSet()
	var npcs []NPCInfo

	warn := func() func(string, error) {
		if lg == nil {
			return func(string, error) {}
		}
		return func(file string, err error) {
			lg.Warn().Err(err).Str("file", file).Msg("content: skipping script file")
		}
	}()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".txt") {
			return nil
		}

		f, oErr := os.Open(path) //nolint:gosec // path is a config-controlled script file
		if oErr != nil {
			warn(path, fmt.Errorf("open: %w", oErr))
			return nil
		}
		src, readErr := io.ReadAll(f)
		_ = f.Close() //nolint:errcheck // best-effort close after read
		if readErr != nil {
			warn(path, fmt.Errorf("read: %w", readErr))
			return nil
		}

		files, parseErr := script.Parse(src)
		if parseErr != nil {
			warn(path, fmt.Errorf("parse: %w", parseErr))
			return nil
		}

		compiledSet, compErr := script.Compile(src)
		if compErr != nil {
			warn(path, fmt.Errorf("compile: %w", compErr))
			return nil
		}

		maps.Copy(set.Scripts, compiledSet.Scripts)
		maps.Copy(set.Funcs, compiledSet.Funcs)

		for _, sf := range files {
			h := sf.Header()
			if h != nil && h.Type == "script" {
				npcs = append(npcs, NPCInfo{
					Name:     h.Name,
					MapName:  h.MapName,
					X:        int16(h.X),         //nolint:gosec // G115: header X is a tile coord clamped by the script parser
					Y:        int16(h.Y),         //nolint:gosec // G115: header Y is a tile coord clamped by the script parser
					Facing:   uint8(h.Facing),    //nolint:gosec // G115: Facing is a 0–7 direction in the script header
					SpriteID: uint16(h.SpriteID), //nolint:gosec // sprite IDs are small ints from the script header
				})
			}
		}
		return nil
	})
	if err != nil {
		return &ScriptStore{Set: set, NPCs: npcs}, fmt.Errorf("walk scripts root %q: %w", root, err)
	}

	return &ScriptStore{Set: set, NPCs: npcs}, nil
}
