package jobexp

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// ExpRow is one row of a BaseExp[] or JobExp[] table.
type ExpRow struct {
	Level int    `yaml:"Level"`
	Exp   uint64 `yaml:"Exp"`
}

type bodyGroup struct {
	Jobs         map[string]bool `yaml:"Jobs"`
	MaxBaseLevel int             `yaml:"MaxBaseLevel"`
	BaseExp      []ExpRow        `yaml:"BaseExp"`
	MaxJobLevel  int             `yaml:"MaxJobLevel"`
	JobExp       []ExpRow        `yaml:"JobExp"`
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version uint32 `yaml:"Version"`
	} `yaml:"Header"`
	Body []bodyGroup `yaml:"Body"`
}

// Registry is a lookup table mapping job names to their merged experience curves.
type Registry struct {
	baseCurves map[string][]ExpRow
	jobCurves  map[string][]ExpRow
	maxBaseLv  map[string]int
	maxJobLv   map[string]int
}

// Load parses a JOB_STATS YAML file (like job_exp.yml) from r and returns a Registry.
// Merge semantics: last group wins for a given field (per rAthena convention).
func Load(r io.Reader) (*Registry, error) {
	var f fileFormat
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("parse job_exp yaml: %w", err)
	}
	if err := validateHeader(f.Header.Type, f.Header.Version); err != nil {
		return nil, err
	}

	reg := &Registry{
		baseCurves: make(map[string][]ExpRow),
		jobCurves:  make(map[string][]ExpRow),
		maxBaseLv:  make(map[string]int),
		maxJobLv:   make(map[string]int),
	}

	for i := range f.Body {
		group := &f.Body[i]
		for jobName, enabled := range group.Jobs {
			if !enabled {
				continue
			}

			if group.MaxBaseLevel != 0 {
				reg.maxBaseLv[jobName] = group.MaxBaseLevel
			}
			if group.MaxJobLevel != 0 {
				reg.maxJobLv[jobName] = group.MaxJobLevel
			}
			if len(group.BaseExp) > 0 {
				reg.baseCurves[jobName] = append([]ExpRow(nil), group.BaseExp...)
			}
			if len(group.JobExp) > 0 {
				reg.jobCurves[jobName] = append([]ExpRow(nil), group.JobExp...)
			}
		}
	}
	return reg, nil
}

func validateHeader(headerType string, version uint32) error {
	if headerType != "JOB_STATS" {
		return fmt.Errorf("job_exp: unexpected Header.Type %q (want %q)", headerType, "JOB_STATS")
	}
	if version != 4 {
		return fmt.Errorf("job_exp: unsupported Header.version %d (want 4)", version)
	}
	return nil
}

// LoadFile opens path and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("open job_exp %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// NewRegistry returns an empty registry — the fallback when job_exp.yml cannot
// be read, so leveling degrades to disabled rather than failing boot (mirrors
// itemdb/mobdb NewRegistry).
func NewRegistry() *Registry {
	return &Registry{
		baseCurves: map[string][]ExpRow{},
		jobCurves:  map[string][]ExpRow{},
		maxBaseLv:  map[string]int{},
		maxJobLv:   map[string]int{},
	}
}

// NextBaseExp returns the Exp needed to go from level -> level+1.
// Returns false if the job is unknown or the level is at or past the max.
func (reg *Registry) NextBaseExp(jobName string, level int) (uint64, bool) {
	curve, ok := reg.baseCurves[jobName]
	if !ok {
		return 0, false
	}

	maxLv, ok := reg.maxBaseLv[jobName]
	if !ok || level >= maxLv {
		return 0, false
	}

	// rAthena convention: curve[0] is Level 1 Exp (to go to 2), etc.
	// The slice may not be 0-indexed by level. Find the row where row.Level == level.
	for _, row := range curve {
		if row.Level == level {
			return row.Exp, true
		}
	}
	return 0, false
}

// MaxBaseLevel returns the maximum base level for a job.
func (reg *Registry) MaxBaseLevel(jobName string) (int, bool) {
	max, ok := reg.maxBaseLv[jobName]
	return max, ok
}

// NextJobExp returns the JobExp needed to go from level → level+1 for the given
// job. Returns false if the job is unknown or the level is at or past the max.
func (reg *Registry) NextJobExp(jobName string, level int) (uint64, bool) {
	curve, ok := reg.jobCurves[jobName]
	if !ok {
		return 0, false
	}

	maxLv, ok := reg.maxJobLv[jobName]
	if !ok || level >= maxLv {
		return 0, false
	}

	// rAthena convention: curve[0] is Level 1 Exp (to go to 2), etc.
	for _, row := range curve {
		if row.Level == level {
			return row.Exp, true
		}
	}
	return 0, false
}

// MaxJobLevel returns the maximum job level for a job.
func (reg *Registry) MaxJobLevel(jobName string) (int, bool) {
	max, ok := reg.maxJobLv[jobName]
	return max, ok
}
