package mobdb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
)

// TestSubmodulePreRenewalDataLoads proves the kernel loaders parse the real
// rathenaThailand pre-re game data (the PACKETVER 20250604 / Thai Classic data
// source) end-to-end: mob_db (single file), skill_db (single file), and the
// item_db family — item_db.yml is a Header/Footer import manifest with zero
// Body rows, so the real items live in item_db_{usable,equip,etc}.yml and are
// merged via LoadFiles (dup-tolerant). Skipped when the submodule is absent
// (e.g. a shallow checkout), so it never breaks CI without the submodule.
func TestSubmodulePreRenewalDataLoads(t *testing.T) {
	root := filepath.Join("..", "..", "..", "third_party", "rathenaThailand", "db", "pre-re")
	if _, err := os.Stat(filepath.Join(root, "mob_db.yml")); err != nil {
		t.Skipf("rathenaThailand submodule not available at %s: %v", root, err)
	}

	mobs, err := LoadFile(filepath.Join(root, "mob_db.yml"))
	if err != nil {
		t.Fatalf("mob_db: %v", err)
	}
	if mobs.Len() < 500 {
		t.Fatalf("mob_db: only %d entries, want >500", mobs.Len())
	}
	t.Logf("mob_db: %d entries", mobs.Len())

	itemFiles, err := filepath.Glob(filepath.Join(root, "item_db*.yml"))
	if err != nil {
		t.Fatalf("item_db glob: %v", err)
	}
	if len(itemFiles) == 0 {
		t.Fatal("item_db: no item_db*.yml files found")
	}
	items, err := itemdb.LoadFiles(itemFiles...)
	if err != nil {
		t.Fatalf("item_db: %v", err)
	}
	if items.Len() < 1000 {
		t.Fatalf("item_db: only %d items, want >1000 (the manifest file alone yields 0; the usable/equip/etc siblings carry the rows)", items.Len())
	}
	t.Logf("item_db: %d entries from %d files", items.Len(), len(itemFiles))

	skills, err := skilldb.LoadFile(filepath.Join(root, "skill_db.yml"))
	if err != nil {
		t.Fatalf("skill_db: %v", err)
	}
	if skills.Len() < 500 {
		t.Fatalf("skill_db: only %d skills, want >500", skills.Len())
	}
	t.Logf("skill_db: %d entries", skills.Len())
}
