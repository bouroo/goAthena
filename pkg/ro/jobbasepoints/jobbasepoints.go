// Package jobbasepoints loads rAthena's three pre-re job-statistics YAML files
// (job_basepoints.yml, job_stats.yml, job_aspd.yml) into a single merged per-job
// registry.
//
// Each rAthena file shares the same JOB_STATS/Version 4 header but contributes
// only the keys it carries:
//
//   - job_basepoints.yml: BaseHp and BaseSp tables (Level -> Hp/Sp).
//   - job_stats.yml:      MaxWeight, HpFactor, HpIncrease, SpFactor,
//     SpIncrease, and BonusStats (Level -> Str/Agi/Vit/Int/Dex/Luk).
//   - job_aspd.yml:       BaseASPD (weapon-class name -> base ASPD).
//
// Callers typically Load each file separately and Merge the resulting
// registries together to assemble a complete JobEntry per job name.
package jobbasepoints

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// HpSpRow is one row of a BaseHp[] or BaseSp[] table.
type HpSpRow struct {
	Level uint16 `yaml:"Level"`
	Hp    uint32 `yaml:"Hp"`
	Sp    uint32 `yaml:"Sp"`
}

// BonusStat is one row of a BonusStats[] table.
type BonusStat struct {
	Level uint16 `yaml:"Level"`
	Str   uint8  `yaml:"Str"`
	Agi   uint8  `yaml:"Agi"`
	Vit   uint8  `yaml:"Vit"`
	Int   uint8  `yaml:"Int"`
	Dex   uint8  `yaml:"Dex"`
	Luk   uint8  `yaml:"Luk"`
}

// JobEntry holds the merged job parameters. Multiple Load/Merge calls
// accumulate into one entry per unique job name. Any field left unset by no
// input file remains at its zero value.
type JobEntry struct {
	Jobs       []string
	BaseHp     []HpSpRow
	BaseSp     []HpSpRow
	MaxWeight  uint32
	HpFactor   uint32
	HpIncrease uint32
	SpFactor   uint32
	SpIncrease uint32
	BonusStats []BonusStat
	BaseASPD   map[string]uint16
}

type bodyGroup struct {
	Jobs       map[string]bool   `yaml:"Jobs"`
	BaseHp     []HpSpRow         `yaml:"BaseHp"`
	BaseSp     []HpSpRow         `yaml:"BaseSp"`
	MaxWeight  uint32            `yaml:"MaxWeight"`
	HpFactor   uint32            `yaml:"HpFactor"`
	HpIncrease uint32            `yaml:"HpIncrease"`
	SpFactor   uint32            `yaml:"SpFactor"`
	SpIncrease uint32            `yaml:"SpIncrease"`
	BonusStats []BonusStat       `yaml:"BonusStats"`
	BaseASPD   map[string]uint16 `yaml:"BaseASPD"`
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version uint32 `yaml:"Version"`
	} `yaml:"Header"`
	Body []bodyGroup `yaml:"Body"`
}

// Registry is a lookup table mapping job names to their merged parameters.
type Registry struct {
	jobs map[string]*JobEntry
}

// Load parses one of the three JOB_STATS YAML files from r and returns a fresh
// Registry. Each Body entry contributes only the keys actually present in the
// source file; subsequent calls to Merge combine registries from different
// files into a complete JobEntry per job.
func Load(r io.Reader) (*Registry, error) {
	var f fileFormat
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("parse job_stats yaml: %w", err)
	}
	if err := validateHeader(f.Header.Type, f.Header.Version); err != nil {
		return nil, err
	}

	jobs := make(map[string]*JobEntry)
	for i := range f.Body {
		for jobName, enabled := range f.Body[i].Jobs {
			if !enabled {
				continue
			}
			entry := jobs[jobName]
			if entry == nil {
				entry = &JobEntry{Jobs: []string{jobName}}
				jobs[jobName] = entry
			}
			entry.applyGroup(&f.Body[i])
		}
	}
	return &Registry{jobs: jobs}, nil
}

// applyGroup folds one Body group's fields into dst using "first wins" semantics.
func (dst *JobEntry) applyGroup(g *bodyGroup) {
	takeSlice(&dst.BaseHp, g.BaseHp)
	takeSlice(&dst.BaseSp, g.BaseSp)
	takeUint32(&dst.MaxWeight, g.MaxWeight)
	takeUint32(&dst.HpFactor, g.HpFactor)
	takeUint32(&dst.HpIncrease, g.HpIncrease)
	takeUint32(&dst.SpFactor, g.SpFactor)
	takeUint32(&dst.SpIncrease, g.SpIncrease)
	takeSlice(&dst.BonusStats, g.BonusStats)
	for weapon, aspd := range g.BaseASPD {
		if dst.BaseASPD == nil {
			dst.BaseASPD = make(map[string]uint16)
		}
		if _, ok := dst.BaseASPD[weapon]; !ok {
			dst.BaseASPD[weapon] = aspd
		}
	}
}

func takeSlice[T any](dst *[]T, src []T) {
	if len(*dst) == 0 && len(src) > 0 {
		*dst = src
	}
}

func takeUint32(dst *uint32, src uint32) {
	if *dst == 0 && src != 0 {
		*dst = src
	}
}

func validateHeader(headerType string, version uint32) error {
	if headerType != "JOB_STATS" {
		return fmt.Errorf("job_stats: unexpected Header.Type %q (want %q)", headerType, "JOB_STATS")
	}
	if version != 4 {
		return fmt.Errorf("job_stats: unsupported Header.version %d (want 4)", version)
	}
	return nil
}

// LoadFile opens path and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured job_*.yml, not user input
	if err != nil {
		return nil, fmt.Errorf("open job_stats %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// Merge folds src's per-job data into reg. Missing reg jobs from src are
// added; existing reg jobs are enriched using "first wins" semantics. Returns
// an error if reg or src is nil.
func (reg *Registry) Merge(src *Registry) error {
	if reg == nil {
		return fmt.Errorf("job_stats: cannot merge into nil registry")
	}
	if src == nil {
		return fmt.Errorf("job_stats: cannot merge from nil source registry")
	}
	if reg.jobs == nil {
		reg.jobs = make(map[string]*JobEntry)
	}
	for jobName, srcEntry := range src.jobs {
		dst, exists := reg.jobs[jobName]
		if !exists {
			reg.jobs[jobName] = srcEntry
			continue
		}
		dst.mergeFrom(srcEntry)
	}
	return nil
}

// mergeFrom copies fields from src into dst using "first wins" semantics.
func (dst *JobEntry) mergeFrom(src *JobEntry) {
	takeSlice(&dst.BaseHp, src.BaseHp)
	takeSlice(&dst.BaseSp, src.BaseSp)
	takeUint32(&dst.MaxWeight, src.MaxWeight)
	takeUint32(&dst.HpFactor, src.HpFactor)
	takeUint32(&dst.HpIncrease, src.HpIncrease)
	takeUint32(&dst.SpFactor, src.SpFactor)
	takeUint32(&dst.SpIncrease, src.SpIncrease)
	takeSlice(&dst.BonusStats, src.BonusStats)
	for weapon, aspd := range src.BaseASPD {
		if dst.BaseASPD == nil {
			dst.BaseASPD = make(map[string]uint16)
		}
		if _, ok := dst.BaseASPD[weapon]; !ok {
			dst.BaseASPD[weapon] = aspd
		}
	}
}

// Get returns the JobEntry for the given job name, or nil if not found.
// Safe on a nil receiver.
func (reg *Registry) Get(jobName string) *JobEntry {
	if reg == nil {
		return nil
	}
	return reg.jobs[jobName]
}

// Len returns the number of unique job names in the registry. Safe on a nil
// receiver.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.jobs)
}

func hpForLevel(entry *JobEntry, level int) uint32 {
	for _, row := range entry.BaseHp {
		if int(row.Level) == level {
			return row.Hp
		}
	}
	return 0
}

func spForLevel(entry *JobEntry, level int) uint32 {
	for _, row := range entry.BaseSp {
		if int(row.Level) == level {
			return row.Sp
		}
	}
	return 0
}

// BaseHpForLevel returns the base HP for a job and level. Returns 0 if the job
// is unknown or the level falls outside the registered rows. Safe on a nil
// receiver.
func (reg *Registry) BaseHpForLevel(jobName string, level int) uint32 {
	entry := reg.Get(jobName)
	if entry == nil {
		return 0
	}
	return hpForLevel(entry, level)
}

// BaseSpForLevel returns the base SP for a job and level. Returns 0 if the job
// is unknown or the level falls outside the registered rows. Safe on a nil
// receiver.
func (reg *Registry) BaseSpForLevel(jobName string, level int) uint32 {
	entry := reg.Get(jobName)
	if entry == nil {
		return 0
	}
	return spForLevel(entry, level)
}

// BaseASPD returns the base ASPD for a job and weapon-class name. Returns 0 if
// the job or weapon is unknown. Safe on a nil receiver.
func (reg *Registry) BaseASPD(jobName, weapon string) uint16 {
	entry := reg.Get(jobName)
	if entry == nil {
		return 0
	}
	return entry.BaseASPD[weapon]
}

// MaxWeight returns the configured MaxWeight for a job, or 0 if unset/unknown.
// Safe on a nil receiver.
func (reg *Registry) MaxWeight(jobName string) uint32 {
	entry := reg.Get(jobName)
	if entry == nil {
		return 0
	}
	return entry.MaxWeight
}
