//go:build integration

package content

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouroo/goAthena/internal/config"
)

func corpusLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// The two source packs' NPC corpora both compile.
// The Thai fork (rAthena-Thailand/rathenaThailand) is the primary target and
// its corpus is a superset of upstream's in practice: 1209 files against
// upstream's 1138. The server must run either one, so the corpus test runs
// both rather than only the fork's.
//
// The test skips a corpus whose submodule is not checked out, so a clone
// without the submodules still builds and tests green.
func TestCompileScripts_SourcePackCorpora(t *testing.T) {
	for _, corpus := range []struct {
		name string
		rel  string
	}{
		{name: "thailand", rel: filepath.Join("third_party", "rathenaThailand", "npc")},
		{name: "upstream", rel: filepath.Join("third_party", "rathena", "npc")},
	} {
		t.Run(corpus.name, func(t *testing.T) {
			root := repoPath(t, corpus.rel)
			if root == "" {
				t.Skipf("%s corpus not present (submodule uninitialised); skipping", corpus.name)
			}
			cfg := &config.Config{}
			cfg.Zone.ScriptPath = root
			set := compileScripts(cfg, corpusLogger())
			if set == nil {
				t.Fatalf("%s corpus: set = nil, want compiled set", corpus.name)
			}
			// The corpus is real content: it must yield dialog scripts, placed
			// NPCs, and mob spawns. A silently empty set means the grammar
			// rejected everything, which the client would see as an empty world.
			if len(set.Scripts) == 0 {
				t.Errorf("%s corpus: scripts = 0, want the corpus's dialog scripts", corpus.name)
			}
			if len(set.NPCs) == 0 {
				t.Errorf("%s corpus: npcs = 0, want the corpus's placed NPCs", corpus.name)
			}
			if len(set.Spawns) == 0 {
				t.Errorf("%s corpus: spawns = 0, want the corpus's mob spawns", corpus.name)
			}
			t.Logf("%s corpus: %d scripts, %d npcs, %d shops, %d spawns",
				corpus.name, len(set.Scripts), len(set.NPCs), len(set.Shops), len(set.Spawns))
		})
	}
}

// repoPath returns the absolute path of a repository-relative path, or "" when
// the path does not exist.
func repoPath(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// The test lives in internal/modules/content, so the repository root is
	// three levels up.
	root := filepath.Clean(filepath.Join(dir, "..", "..", ".."))
	path := filepath.Join(root, rel)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}
