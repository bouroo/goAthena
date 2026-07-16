//go:build unit

package jobdb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_RealFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "third_party", "rathena", "db", "pre-re", "job_exp.yml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("rAthena submodule not available at %s: %v", path, err)
	}
	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if reg.Len() <= 60 {
		t.Fatalf("Len() = %d, want more than 60", reg.Len())
	}
	if reg.Get("Novice") == nil || reg.Get("Swordman") == nil {
		t.Fatal("expected Novice and Swordman entries")
	}
	if reg.Get("NonexistentJob") != nil {
		t.Fatal("unknown job returned an entry")
	}
	for _, test := range []struct {
		level int
		want  uint64
	}{
		{level: 1, want: 9},
		{level: 98, want: 99999998},
		{level: 99, want: 99999999},
		{level: 100, want: 0},
		{level: 0, want: 0},
	} {
		if got := reg.BaseExpForLevel("Swordman", test.level); got != test.want {
			t.Errorf("BaseExpForLevel(Swordman, %d) = %d, want %d", test.level, got, test.want)
		}
	}
	if got := reg.JobExpForLevel("Novice", 1); got != 10 {
		t.Errorf("JobExpForLevel(Novice, 1) = %d, want 10", got)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	_, err := Load(strings.NewReader("Header: [invalid"))
	if err == nil || !strings.Contains(err.Error(), "parse job_exp yaml:") {
		t.Fatalf("Load() error = %v, want wrapped parse error", err)
	}
}

func TestLoad_EmptyBody(t *testing.T) {
	reg, err := Load(strings.NewReader("Header:\n  Type: JOB_DB\n  Version: 1\nBody: []\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reg.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", reg.Len())
	}
}

func TestLoad_DuplicateJob(t *testing.T) {
	input := `Header:
  Type: JOB_DB
  Version: 1
Body:
  - Jobs:
      TestJob: true
    MaxBaseLevel: 1
    BaseExp:
      - {Level: 1, Exp: 10}
  - Jobs:
      TestJob: true
    MaxBaseLevel: 1
    BaseExp:
      - {Level: 1, Exp: 20}
`
	reg, err := Load(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reg.BaseExpForLevel("TestJob", 1); got != 10 {
		t.Fatalf("BaseExpForLevel(TestJob, 1) = %d, want first value 10", got)
	}
}

func TestLoad_PointerIsolation(t *testing.T) {
	input := `Header:
  Type: JOB_DB
  Version: 1
Body:
  - Jobs:
      JobA: true
      JobB: true
    MaxBaseLevel: 2
    BaseExp:
      - {Level: 1, Exp: 10}
      - {Level: 2, Exp: 20}
  - Jobs:
      JobA: true
    MaxJobLevel: 2
    JobExp:
      - {Level: 1, Exp: 5}
      - {Level: 2, Exp: 15}
`
	reg, err := Load(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reg.BaseExpForLevel("JobA", 1); got != 10 {
		t.Errorf("JobA BaseExp(1) = %d, want 10", got)
	}
	if got := reg.JobExpForLevel("JobA", 1); got != 5 {
		t.Errorf("JobA JobExp(1) = %d, want 5", got)
	}
	if got := reg.BaseExpForLevel("JobB", 1); got != 10 {
		t.Errorf("JobB BaseExp(1) = %d, want 10", got)
	}
	if got := reg.JobExpForLevel("JobB", 1); got != 0 {
		t.Errorf("JobB JobExp(1) = %d, want 0 (pointer isolation bug)", got)
	}
}

func TestLoad_MinimalYAML(t *testing.T) {
	input := `Header:
  Type: JOB_DB
  Version: 1
Body:
  - Jobs:
      TestJob: true
    MaxBaseLevel: 3
    BaseExp:
      - {Level: 1, Exp: 10}
      - {Level: 2, Exp: 20}
      - {Level: 3, Exp: 30}
    MaxJobLevel: 2
    JobExp:
      - {Level: 1, Exp: 5}
      - {Level: 2, Exp: 15}
`
	reg, err := Load(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	entry := reg.Get("TestJob")
	if entry == nil || entry.MaxBaseLevel != 3 {
		t.Fatalf("entry = %#v, want MaxBaseLevel 3", entry)
	}
	for _, test := range []struct {
		level int
		want  uint64
	}{
		{level: 1, want: 10},
		{level: 2, want: 20},
		{level: 3, want: 30},
	} {
		if got := reg.BaseExpForLevel("TestJob", test.level); got != test.want {
			t.Errorf("BaseExpForLevel(TestJob, %d) = %d, want %d", test.level, got, test.want)
		}
	}
	for _, test := range []struct {
		level int
		want  uint64
	}{
		{level: 1, want: 5},
		{level: 2, want: 15},
	} {
		if got := reg.JobExpForLevel("TestJob", test.level); got != test.want {
			t.Errorf("JobExpForLevel(TestJob, %d) = %d, want %d", test.level, got, test.want)
		}
	}
}

func TestJobExpForLevel_OutOfRange(t *testing.T) {
	reg, err := Load(strings.NewReader(`Header:
  Type: JOB_DB
  Version: 1
Body:
  - Jobs:
      TestJob: true
    MaxJobLevel: 2
    JobExp:
      - {Level: 1, Exp: 5}
      - {Level: 2, Exp: 15}
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, test := range []struct {
		job   string
		level int
	}{
		{job: "Unknown", level: 1},
		{job: "TestJob", level: 0},
		{job: "TestJob", level: 3},
	} {
		if got := reg.JobExpForLevel(test.job, test.level); got != 0 {
			t.Errorf("JobExpForLevel(%q, %d) = %d, want 0", test.job, test.level, got)
		}
	}
}
