//go:build unit

package world

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/config"
)

// forkDBRoot resolves the rAthena fork's db/ root by walking up from this
// package's CWD to the directory containing go.mod (the repo root), then into
// third_party/rathenaThailand/db. Walking to go.mod keeps the path stable if the
// file moves within the tree. The submodule is optional: when it is not checked
// out, skipForkAbsent skips the real-fork tests instead of failing — the slim
// ./data set stays the CI default.
func forkDBRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod) from test CWD")
		}
		dir = parent
	}
	return filepath.Join(dir, "third_party", "rathenaThailand", "db")
}

func skipForkAbsent(t *testing.T, dbRoot string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dbRoot, "re", "mob_db.yml")); err != nil {
		t.Skipf("rathena fork submodule not present at %s; skipping real-DB load test", dbRoot)
	}
}

// TestLoadMobDB_RealForkBothModes is the M8 data-fidelity gate. With zone.db_path
// pointed at the rAthena fork and mob_db_path cleared, loadMobDB resolves the
// mode subtree (db/re when renewal is on, db/pre-re when off) and loads the real
// corpus — well over a hundred mobs in both modes, with Poring (1002) carrying
// the per-mode HP (55 renewal, 50 pre-re). This is the executable evidence that
// the two-tier resolution plus zone.renewal dispatch actually boots against the
// real rathenaThailand DB, not just the 4-mob slim fallback.
func TestLoadMobDB_RealForkBothModes(t *testing.T) {
	dbRoot := forkDBRoot(t)
	skipForkAbsent(t, dbRoot)
	noop := do.New() // no logger → warnLogger falls back to zerolog.Nop()

	reReg := loadMobDB(noop, config.ZoneConfig{DBPath: dbRoot, Renewal: true})
	require.NotNil(t, reReg, "renewal fork mob_db must load")
	assert.Greater(t, reReg.Len(), 100, "db/re has far more than 100 mobs")
	rePoring := reReg.Get(1002)
	require.NotNil(t, rePoring, "Poring 1002 present in db/re")
	assert.Equal(t, "PORING", rePoring.AegisName)
	assert.Equal(t, int32(55), rePoring.Hp, "renewal Poring HP")

	preReg := loadMobDB(noop, config.ZoneConfig{DBPath: dbRoot, Renewal: false})
	require.NotNil(t, preReg, "pre-re fork mob_db must load")
	assert.Greater(t, preReg.Len(), 100, "db/pre-re has far more than 100 mobs")
	prePoring := preReg.Get(1002)
	require.NotNil(t, prePoring, "Poring 1002 present in db/pre-re")
	assert.Equal(t, int32(50), prePoring.Hp, "pre-renewal Poring HP")
}

// TestLoadMobDB_OverrideWins pins the override branch: a non-empty mob_db_path
// always wins over the db_path subtree, so the committed slim ./data set stays
// the default and CI boots deterministically even with the submodule absent.
func TestLoadMobDB_OverrideWins(t *testing.T) {
	noop := do.New()
	// An override pointing at a missing file yields nil (graceful degrade), never
	// a fall-through to db_path. This is what keeps a slim-deployment pin stable.
	reg := loadMobDB(noop, config.ZoneConfig{
		DBPath:    "/nonexistent-fork-db",
		Renewal:   true,
		MobDBPath: "/nonexistent-override/mob_db.yml",
	})
	assert.Nil(t, reg, "override path is authoritative even when it is missing")
}
