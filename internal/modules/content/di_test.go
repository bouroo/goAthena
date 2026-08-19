//go:build unit

package content

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouroo/goAthena/internal/config"
)

// compileScripts on a nested temp corpus proves the three file shapes the
// production rAthena tree throws at it: a city dialog-NPC file (compiles),
// a nested mob-spawn file (NPC grammar rejects, line scanner accepts), and
// a junk file (skipped, others still load).
func TestCompileScripts_NestedCorpus(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pre-re", "mobs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	city := "prontera,101,288,3\tscript\tShuger#pront\t98,{\n\tmes \"yo\";\n\tclose;\n}\n"
	spawnFile := "gef_fild00,0,0\tmonster\tPoring\t1002,50\ngef_fild00,54,212,5,5\tmonster\tGreen Plant\t1080,3,360000,180000\n"
	junk := "}}}totally broken {{{"
	for path, content := range map[string]string{
		filepath.Join(root, "cities.txt"): city,
		filepath.Join(sub, "fields.txt"):  spawnFile,
		filepath.Join(root, "broken.txt"): junk,
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{}
	cfg.Zone.ScriptPath = root
	set := compileScripts(cfg, testLogger())
	if set == nil {
		t.Fatal("set = nil, want compiled set")
	}
	if len(set.Scripts) != 1 || set.Scripts["Shuger#pront"] == nil {
		t.Fatalf("scripts = %d, want 1 with Shuger#pront", len(set.Scripts))
	}
	if len(set.NPCs) != 1 || set.NPCs[0].MapName != "prontera" {
		t.Fatalf("npcs = %+v, want the placed dialog NPC", set.NPCs)
	}
	if len(set.Spawns) != 2 {
		t.Fatalf("spawns = %d, want 2 (spawn-file fallback)", len(set.Spawns))
	}
	if set.Spawns[0].Class != 1002 || set.Spawns[0].Amount != 50 {
		t.Fatalf("spawn[0] = %+v", set.Spawns[0])
	}
	if set.Spawns[1].XSize != 5 || set.Spawns[1].Delay1 != 360000 {
		t.Fatalf("spawn[1] = %+v", set.Spawns[1])
	}
}

// Empty and missing script paths degrade to an empty-but-usable set / nil.
func TestCompileScripts_Fallbacks(t *testing.T) {
	if set := compileScripts(&config.Config{}, testLogger()); set == nil || len(set.Spawns) != 0 {
		t.Fatalf("empty path: set = %v", set)
	}
	cfg := &config.Config{}
	cfg.Zone.ScriptPath = filepath.Join(t.TempDir(), "nope")
	if set := compileScripts(cfg, testLogger()); set != nil {
		t.Fatalf("missing path: set = %v, want nil", set)
	}
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }
