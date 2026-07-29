package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/rs/zerolog"
)

func TestLoadShopDefs_HappyPath(t *testing.T) {
	root := t.TempDir()
	yml := `shops:
  - name: "Tool Shop"
    map: prontera
    x: 155
    y: 153
    facing: 4
    sprite: 70
    items:
      - name_id: 501
        price: 50
      - name_id: 504
        price: 1000
`
	if err := os.WriteFile(filepath.Join(root, "prontera.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}

	defs, err := app.LoadShopDefs(root, nil)
	if err != nil {
		t.Fatalf("LoadShopDefs: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d, want 1", len(defs))
	}
	d := defs[0]
	if d.Name != "Tool Shop" || d.Map != "prontera" || d.X != 155 || d.Y != 153 || d.Facing != 4 || d.Sprite != 70 {
		t.Errorf("unexpected def: %+v", d)
	}
	if len(d.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(d.Items))
	}
	if d.Items[0].NameID != 501 || d.Items[0].Price != 50 {
		t.Errorf("Items[0] = %+v, want {501,50}", d.Items[0])
	}
	if d.Items[1].NameID != 504 || d.Items[1].Price != 1000 {
		t.Errorf("Items[1] = %+v, want {504,1000}", d.Items[1])
	}
}

func TestLoadShopDefs_GracefulSkip(t *testing.T) {
	root := t.TempDir()
	good := `shops:
  - name: "Good"
    map: prontera
    x: 1
    y: 1
    facing: 0
    sprite: 1
    items:
      - name_id: 501
        price: 10
`
	bad := "shops:\n  - name: \"Broken\"\n    map: prontera\n    x: a\n" // x is a string, not int
	if err := os.WriteFile(filepath.Join(root, "good.yml"), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad.yml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	defs, err := app.LoadShopDefs(root, &logger)
	if err != nil {
		t.Fatalf("LoadShopDefs: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d, want 1 (good file only)", len(defs))
	}
	if defs[0].Name != "Good" {
		t.Errorf("Name = %q, want %q", defs[0].Name, "Good")
	}
}

func TestLoadShopDefs_NoFilesFound(t *testing.T) {
	root := t.TempDir()
	defs, err := app.LoadShopDefs(root, nil)
	if err != nil {
		t.Fatalf("LoadShopDefs: %v", err)
	}
	if defs != nil {
		t.Errorf("defs = %v, want nil", defs)
	}
}
