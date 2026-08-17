package jobexp

import (
	"os"
	"testing"
)

func TestLoad_Synthetic(t *testing.T) {
	yamlData := `
Header:
  Type: JOB_STATS
  Version: 4
Body:
  - Jobs:
      Novice: true
    MaxBaseLevel: 99
    BaseExp:
      - Level: 1
        Exp: 9
      - Level: 2
        Exp: 16
  - Jobs:
      Novice: true
    MaxBaseLevel: 100
    BaseExp:
      - Level: 1
        Exp: 10
`
	tmpFile, err := os.CreateTemp("", "job_exp_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(yamlData); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	reg, err := LoadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}

	if max, ok := reg.MaxBaseLevel("Novice"); !ok || max != 100 {
		t.Errorf("MaxBaseLevel expected 100, got %d (ok: %v)", max, ok)
	}
	next, ok := reg.NextBaseExp("Novice", 1)
	if !ok || next != 10 {
		t.Errorf("NextBaseExp(1) expected 10, got %d (ok: %v)", next, ok)
	}
}

func TestLoad_UnknownJob(t *testing.T) {
	yamlData := `
Header:
  Type: JOB_STATS
  Version: 4
Body:
  - Jobs:
      Novice: true
`
	tmpFile, err := os.CreateTemp("", "job_exp_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(yamlData); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	reg, err := LoadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}

	if _, ok := reg.MaxBaseLevel("Warrior"); ok {
		t.Error("Expected MaxBaseLevel for unknown job to be not ok")
	}
	if _, ok := reg.NextBaseExp("Warrior", 1); ok {
		t.Error("Expected NextBaseExp for unknown job to be not ok")
	}
}

func TestLoad_RealSubmodule(t *testing.T) {
	path := "third_party/rathenaThailand/db/pre-re/job_exp.yml"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("submodule data missing")
	}

	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile real failed: %v", err)
	}

	// Novice MaxBaseLevel == 99 (from line 141)
	if max, ok := reg.MaxBaseLevel("Novice"); !ok || max != 99 {
		t.Errorf("Novice MaxBaseLevel expected 99, got %d (ok: %v)", max, ok)
	}

	// Novice Level 1 Exp == 9 (from line 144)
	if next, ok := reg.NextBaseExp("Novice", 1); !ok || next != 9 {
		t.Errorf("Novice NextBaseExp(1) expected 9, got %d (ok: %v)", next, ok)
	}

	// Novice Level 2 Exp == 16 (from line 146)
	if next, ok := reg.NextBaseExp("Novice", 2); !ok || next != 16 {
		t.Errorf("Novice NextBaseExp(2) expected 16, got %d (ok: %v)", next, ok)
	}

	// Novice Level 99 should be false (past max)
	if _, ok := reg.NextBaseExp("Novice", 99); ok {
		t.Error("Novice NextBaseExp(99) should be false")
	}
}
