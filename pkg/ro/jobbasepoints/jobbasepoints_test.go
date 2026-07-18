//go:build unit

package jobbasepoints

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validHeader = "Header:\n  Type: JOB_STATS\n  Version: 4\n"

func TestLoad_RealFiles(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "third_party", "rathena", "db", "pre-re")
	basepointsPath := filepath.Join(dir, "job_basepoints.yml")
	statsPath := filepath.Join(dir, "job_stats.yml")
	aspdPath := filepath.Join(dir, "job_aspd.yml")
	for _, p := range []string{basepointsPath, statsPath, aspdPath} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("rAthena submodule not available at %s: %v", p, err)
		}
	}

	baseReg, err := LoadFile(basepointsPath)
	if err != nil {
		t.Fatalf("LoadFile(basepoints) error = %v", err)
	}
	statsReg, err := LoadFile(statsPath)
	if err != nil {
		t.Fatalf("LoadFile(stats) error = %v", err)
	}
	aspdReg, err := LoadFile(aspdPath)
	if err != nil {
		t.Fatalf("LoadFile(aspd) error = %v", err)
	}

	if err := baseReg.Merge(statsReg); err != nil {
		t.Fatalf("baseReg.Merge(statsReg) error = %v", err)
	}
	if err := baseReg.Merge(aspdReg); err != nil {
		t.Fatalf("baseReg.Merge(aspdReg) error = %v", err)
	}

	if baseReg.Get("Novice") == nil {
		t.Fatal("Novice entry missing after merge")
	}
	if baseReg.Get("Swordman") == nil {
		t.Fatal("Swordman entry missing after merge")
	}

	if got, want := baseReg.BaseHpForLevel("Novice", 1), uint32(40); got != want {
		t.Errorf("BaseHpForLevel(Novice, 1) = %d, want %d", got, want)
	}
	if got, want := baseReg.BaseHpForLevel("Novice", 99), uint32(530); got != want {
		t.Errorf("BaseHpForLevel(Novice, 99) = %d, want %d", got, want)
	}

	if got, want := baseReg.MaxWeight("Novice"), uint32(0); got != want {
		t.Errorf("MaxWeight(Novice) = %d, want %d (Novice has no MaxWeight in job_stats)", got, want)
	}
	if got, want := baseReg.MaxWeight("Swordman"), uint32(28000); got != want {
		t.Errorf("MaxWeight(Swordman) = %d, want %d", got, want)
	}

	swordman := baseReg.Get("Swordman")
	if swordman == nil {
		t.Fatal("Swordman entry nil")
	}
	if swordman.HpFactor != 70 {
		t.Errorf("Swordman HpFactor = %d, want 70", swordman.HpFactor)
	}
	if swordman.SpIncrease != 200 {
		t.Errorf("Swordman SpIncrease = %d, want 200", swordman.SpIncrease)
	}

	if got, want := baseReg.BaseASPD("Swordman", "1hSword"), uint16(550); got != want {
		t.Errorf("BaseASPD(Swordman, 1hSword) = %d, want %d", got, want)
	}
	if got, want := baseReg.BaseASPD("Novice", "Fist"), uint16(500); got != want {
		t.Errorf("BaseASPD(Novice, Fist) = %d, want %d", got, want)
	}
}

func TestLoad_BasePoints_Happy(t *testing.T) {
	in := validHeader + `Body:
  - Jobs:
      TestJob: true
    BaseHp:
      - {Level: 1, Hp: 100}
      - {Level: 2, Hp: 110}
    BaseSp:
      - {Level: 1, Sp: 20}
      - {Level: 5, Sp: 50}
`
	reg, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := reg.BaseHpForLevel("TestJob", 1), uint32(100); got != want {
		t.Errorf("BaseHpForLevel(TestJob, 1) = %d, want %d", got, want)
	}
	if got, want := reg.BaseHpForLevel("TestJob", 2), uint32(110); got != want {
		t.Errorf("BaseHpForLevel(TestJob, 2) = %d, want %d", got, want)
	}
	if got, want := reg.BaseSpForLevel("TestJob", 1), uint32(20); got != want {
		t.Errorf("BaseSpForLevel(TestJob, 1) = %d, want %d", got, want)
	}
	if got, want := reg.BaseSpForLevel("TestJob", 5), uint32(50); got != want {
		t.Errorf("BaseSpForLevel(TestJob, 5) = %d, want %d", got, want)
	}
	if got := reg.BaseHpForLevel("TestJob", 3); got != 0 {
		t.Errorf("BaseHpForLevel(TestJob, 3) = %d, want 0 (out of range)", got)
	}
	if got := reg.BaseSpForLevel("TestJob", 99); got != 0 {
		t.Errorf("BaseSpForLevel(TestJob, 99) = %d, want 0 (out of range)", got)
	}
	if got := reg.BaseHpForLevel("Unknown", 1); got != 0 {
		t.Errorf("BaseHpForLevel(Unknown, 1) = %d, want 0 (unknown job)", got)
	}
}

func TestLoad_Stats_Happy(t *testing.T) {
	in := validHeader + `Body:
  - Jobs:
      TestJob: true
    MaxWeight: 25000
    HpFactor: 50
    HpIncrease: 500
    SpFactor: 20
    SpIncrease: 100
    BonusStats:
      - {Level: 2, Str: 1}
      - {Level: 4, Int: 1}
`
	reg, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := reg.MaxWeight("TestJob"), uint32(25000); got != want {
		t.Errorf("MaxWeight(TestJob) = %d, want %d", got, want)
	}
	entry := reg.Get("TestJob")
	if entry == nil {
		t.Fatal("TestJob entry is nil")
	}
	if entry.HpFactor != 50 {
		t.Errorf("HpFactor = %d, want 50", entry.HpFactor)
	}
	if entry.HpIncrease != 500 {
		t.Errorf("HpIncrease = %d, want 500", entry.HpIncrease)
	}
	if entry.SpFactor != 20 {
		t.Errorf("SpFactor = %d, want 20", entry.SpFactor)
	}
	if entry.SpIncrease != 100 {
		t.Errorf("SpIncrease = %d, want 100", entry.SpIncrease)
	}
	if len(entry.BonusStats) != 2 {
		t.Fatalf("BonusStats len = %d, want 2", len(entry.BonusStats))
	}
	if entry.BonusStats[0].Level != 2 || entry.BonusStats[0].Str != 1 {
		t.Errorf("BonusStats[0] = %+v, want Level=2 Str=1", entry.BonusStats[0])
	}
	if entry.BonusStats[1].Level != 4 || entry.BonusStats[1].Int != 1 {
		t.Errorf("BonusStats[1] = %+v, want Level=4 Int=1", entry.BonusStats[1])
	}
	if got := reg.MaxWeight("Unknown"); got != 0 {
		t.Errorf("MaxWeight(Unknown) = %d, want 0", got)
	}
}

func TestLoad_ASPD_Happy(t *testing.T) {
	in := validHeader + `Body:
  - Jobs:
      TestJob: true
    BaseASPD:
      Fist: 400
      Dagger: 500
      1hSword: 550
`
	reg, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := reg.BaseASPD("TestJob", "Fist"), uint16(400); got != want {
		t.Errorf("BaseASPD(TestJob, Fist) = %d, want %d", got, want)
	}
	if got, want := reg.BaseASPD("TestJob", "Dagger"), uint16(500); got != want {
		t.Errorf("BaseASPD(TestJob, Dagger) = %d, want %d", got, want)
	}
	if got, want := reg.BaseASPD("TestJob", "1hSword"), uint16(550); got != want {
		t.Errorf("BaseASPD(TestJob, 1hSword) = %d, want %d", got, want)
	}
	if got := reg.BaseASPD("TestJob", "Bow"); got != 0 {
		t.Errorf("BaseASPD(TestJob, Bow) = %d, want 0 (unknown weapon)", got)
	}
	if got := reg.BaseASPD("Unknown", "Fist"); got != 0 {
		t.Errorf("BaseASPD(Unknown, Fist) = %d, want 0 (unknown job)", got)
	}
}

func TestMerge_AccumulatesAcrossFiles(t *testing.T) {
	basepoints := validHeader + `Body:
  - Jobs:
      TestJob: true
    BaseHp:
      - {Level: 1, Hp: 100}
    BaseSp:
      - {Level: 1, Sp: 20}
`
	stats := validHeader + `Body:
  - Jobs:
      TestJob: true
    MaxWeight: 30000
    HpFactor: 60
`
	aspd := validHeader + `Body:
  - Jobs:
      TestJob: true
    BaseASPD:
      Fist: 450
`
	reg1, err := Load(strings.NewReader(basepoints))
	if err != nil {
		t.Fatalf("Load(basepoints) error = %v", err)
	}
	reg2, err := Load(strings.NewReader(stats))
	if err != nil {
		t.Fatalf("Load(stats) error = %v", err)
	}
	reg3, err := Load(strings.NewReader(aspd))
	if err != nil {
		t.Fatalf("Load(aspd) error = %v", err)
	}
	if err := reg1.Merge(reg2); err != nil {
		t.Fatalf("reg1.Merge(reg2) error = %v", err)
	}
	if err := reg1.Merge(reg3); err != nil {
		t.Fatalf("reg1.Merge(reg3) error = %v", err)
	}

	entry := reg1.Get("TestJob")
	if entry == nil {
		t.Fatal("TestJob entry missing after merge")
	}
	if got, want := reg1.BaseHpForLevel("TestJob", 1), uint32(100); got != want {
		t.Errorf("BaseHpForLevel(TestJob, 1) = %d, want %d (from basepoints)", got, want)
	}
	if got, want := reg1.BaseSpForLevel("TestJob", 1), uint32(20); got != want {
		t.Errorf("BaseSpForLevel(TestJob, 1) = %d, want %d (from basepoints)", got, want)
	}
	if got, want := reg1.MaxWeight("TestJob"), uint32(30000); got != want {
		t.Errorf("MaxWeight(TestJob) = %d, want %d (from stats)", got, want)
	}
	if entry.HpFactor != 60 {
		t.Errorf("HpFactor = %d, want 60 (from stats)", entry.HpFactor)
	}
	if got, want := reg1.BaseASPD("TestJob", "Fist"), uint16(450); got != want {
		t.Errorf("BaseASPD(TestJob, Fist) = %d, want %d (from aspd)", got, want)
	}
}

func TestMerge_NoMutationOfSource(t *testing.T) {
	basepoints := validHeader + `Body:
  - Jobs:
      TestJob: true
    BaseHp:
      - {Level: 1, Hp: 100}
    HpFactor: 60
`
	stats := validHeader + `Body:
  - Jobs:
      TestJob: true
    SpFactor: 20
`
	reg2, err := Load(strings.NewReader(basepoints))
	if err != nil {
		t.Fatalf("Load(basepoints) error = %v", err)
	}
	reg3, err := Load(strings.NewReader(stats))
	if err != nil {
		t.Fatalf("Load(stats) error = %v", err)
	}
	reg1 := &Registry{}
	if err := reg1.Merge(reg2); err != nil {
		t.Fatalf("reg1.Merge(reg2) error = %v", err)
	}
	if err := reg1.Merge(reg3); err != nil {
		t.Fatalf("reg1.Merge(reg3) error = %v", err)
	}

	source := reg2.Get("TestJob")
	if source == nil {
		t.Fatal("reg2 TestJob entry missing")
	}
	if source.SpFactor != 0 {
		t.Errorf("reg2 SpFactor = %d, want 0", source.SpFactor)
	}
	if len(source.BaseHp) != 1 || source.BaseHp[0] != (HpSpRow{Level: 1, Hp: 100}) {
		t.Errorf("reg2 BaseHp = %+v, want [{Level:1 Hp:100}]", source.BaseHp)
	}

	merged := reg1.Get("TestJob")
	if merged == nil {
		t.Fatal("reg1 TestJob entry missing")
	}
	if merged.HpFactor != 60 || merged.SpFactor != 20 {
		t.Errorf("reg1 factors = HpFactor:%d SpFactor:%d, want 60 and 20", merged.HpFactor, merged.SpFactor)
	}
	if len(merged.BaseHp) != 1 || merged.BaseHp[0] != (HpSpRow{Level: 1, Hp: 100}) {
		t.Errorf("reg1 BaseHp = %+v, want [{Level:1 Hp:100}]", merged.BaseHp)
	}
	merged.BaseHp[0].Hp = 999
	if got := source.BaseHp[0].Hp; got != 100 {
		t.Errorf("reg2 BaseHp[0].Hp = %d after mutating reg1, want 100", got)
	}
}

func TestMerge_NilReceiver(t *testing.T) {
	var reg *Registry
	src, err := Load(strings.NewReader(validHeader + `Body:
  - Jobs:
      X: true
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := reg.Merge(src); err == nil {
		t.Error("nil.Merge(nonNil) error = nil, want error")
	}
	empty := &Registry{}
	if err := empty.Merge(nil); err == nil {
		t.Error("empty.Merge(nil) error = nil, want error")
	}
}

func TestGet_NotFound(t *testing.T) {
	in := validHeader + `Body:
  - Jobs:
      TestJob: true
    MaxWeight: 1000
`
	reg, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reg.Get("Nonexistent") != nil {
		t.Error("Get(Nonexistent) = entry, want nil")
	}
}

func TestNil_Receiver(t *testing.T) {
	var r *Registry
	if r.Get("anything") != nil {
		t.Error("nil.Get() should return nil")
	}
	if r.Len() != 0 {
		t.Errorf("nil.Len() = %d, want 0", r.Len())
	}
	if got := r.BaseHpForLevel("anything", 1); got != 0 {
		t.Errorf("nil.BaseHpForLevel() = %d, want 0", got)
	}
	if got := r.BaseSpForLevel("anything", 1); got != 0 {
		t.Errorf("nil.BaseSpForLevel() = %d, want 0", got)
	}
	if got := r.BaseASPD("anything", "Fist"); got != 0 {
		t.Errorf("nil.BaseASPD() = %d, want 0", got)
	}
	if got := r.MaxWeight("anything"); got != 0 {
		t.Errorf("nil.MaxWeight() = %d, want 0", got)
	}
}

func TestLoad_WrongHeaderType(t *testing.T) {
	in := `Header:
  Type: WRONG_STATS
  Version: 4
Body:
  - Jobs:
      X: true
`
	_, err := Load(strings.NewReader(in))
	if err == nil {
		t.Fatal("Load() error = nil, want wrong-type error")
	}
	if !strings.Contains(err.Error(), "JOB_STATS") {
		t.Errorf("error = %q, want it to contain JOB_STATS", err.Error())
	}
}

func TestLoad_WrongHeaderVersion(t *testing.T) {
	in := `Header:
  Type: JOB_STATS
  Version: 3
Body:
  - Jobs:
      X: true
`
	_, err := Load(strings.NewReader(in))
	if err == nil {
		t.Fatal("Load() error = nil, want wrong-version error")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error = %q, want it to contain version", err.Error())
	}
}

func TestLoad_EmptyBody(t *testing.T) {
	in := validHeader + `Body: []
`
	reg, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reg == nil {
		t.Fatal("Load() returned nil registry, want non-nil")
	}
	if reg.Len() != 0 {
		t.Errorf("Len() = %d, want 0", reg.Len())
	}
}

func TestLoad_DisabledJob(t *testing.T) {
	in := validHeader + `Body:
  - Jobs:
      Foo: false
      Bar: true
    MaxWeight: 12345
`
	reg, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reg.Get("Foo") != nil {
		t.Error("Get(Foo) = entry, want nil (disabled)")
	}
	if reg.Get("Bar") == nil {
		t.Error("Get(Bar) = nil, want entry (enabled)")
	}
	if got, want := reg.MaxWeight("Bar"), uint32(12345); got != want {
		t.Errorf("MaxWeight(Bar) = %d, want %d", got, want)
	}
}

func TestLoadFile_Missing(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "does_not_exist.yml"))
	if err == nil {
		t.Fatal("LoadFile() error = nil, want open error")
	}
	if !strings.Contains(err.Error(), "open") {
		t.Errorf("error = %q, want it to contain open", err.Error())
	}
}
