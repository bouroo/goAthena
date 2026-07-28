package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
// in a directory tree. It extracts the placement metadata for placed NPCs using
// the parser's NPCHeader.
func LoadScripts(root string) (*ScriptStore, error) {
	set := script.NewCompiledScriptSet()
	var npcs []NPCInfo

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".txt") {
			return nil
		}

		f, oErr := os.Open(path)
		if oErr != nil {
			return fmt.Errorf("open script %q: %w", path, oErr)
		}
		defer f.Close()

		src, readErr := io.ReadAll(f)
		if readErr != nil {
			return fmt.Errorf("read script %q: %w", path, readErr)
		}

		// Use the compiler's Parse which returns the slice of files.
		files, parseErr := script.Parse(src)
		if parseErr != nil {
			return fmt.Errorf("parse script %q: %w", path, parseErr)
		}

		// Feed each file to the compiler (which accepts one full src payload but we can compile block by block)
		// For our interface though, `script.Compile` processes `src` wholesale.
		// Since we want the Header for placement, we invoke Parse directly and compile from there?
		// Or we can just use script.Compile(src) and then use the Parse to just get headers.
		// A cleaner way is using script.Compile, then script.Parse to get the headers... but
		// wait, script.Compile internally does Parse. Let's just do it directly.

		compiledSet, compErr := script.Compile(src)
		if compErr != nil {
			return fmt.Errorf("compile script %q: %w", path, compErr)
		}

		// Merge the compiled scripts into our global set
		for name, cs := range compiledSet.Scripts {
			set.Scripts[name] = cs
		}
		for name, cs := range compiledSet.Funcs {
			set.Funcs[name] = cs
		}

		// Extract placed NPCs using the parsed files' headers.
		for _, sf := range files {
			h := sf.Header()
			// Unplaced scripts and functions have a nil or invalid header.
			if h != nil && h.Type == "script" {
				npcs = append(npcs, NPCInfo{
					Name:     h.Name,
					MapName:  h.MapName,
					X:        int16(h.X),
					Y:        int16(h.Y),
					Facing:   uint8(h.Facing),
					SpriteID: uint16(h.SpriteID), //nolint:gosec // sprite IDs are small ints from the script header
				})
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return &ScriptStore{
		Set:  set,
		NPCs: npcs,
	}, nil
}
