package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/rs/zerolog"
)

func TestLoadScripts_GracefulSkip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "healer.txt"), []byte("prontera,150,150,4\tscript\tHealer\t46,{\n\tmes \"Hi\";\n\tclose;\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.txt"), []byte("prontera,100,100,4\tscript\tBroken\t4_M_BAD,{\n\tmenu \"x\",L_x;\nL_x:\n\tset $x, 1;\n\tend;\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	store, _ := app.LoadScripts(root, &logger) // walk never returns nil even on partial failure
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if len(store.NPCs) != 1 {
		t.Fatalf("expected 1 NPC (the parseable healer), got %d", len(store.NPCs))
	}
}
