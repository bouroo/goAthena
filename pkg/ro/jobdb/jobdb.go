// Package jobdb loads rAthena job_exp.yml (Header JOB_DB) into a lookup registry.
package jobdb

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// ExpLevel pairs a (1-based) level with its EXP threshold.
type ExpLevel struct {
	Level uint16 `yaml:"Level"`
	Exp   uint64 `yaml:"Exp"`
}

// JobEntry holds the EXP curve for a single job. Built by merging data
// from multiple Body entries in job_exp.yml (BaseExp and JobExp may come
// from different groups).
type JobEntry struct {
	Jobs         []string
	MaxBaseLevel uint16
	BaseExp      []ExpLevel
	MaxJobLevel  uint16
	JobExp       []ExpLevel
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version uint32 `yaml:"Version"`
	} `yaml:"Header"`
	Body []struct {
		Jobs         map[string]bool `yaml:"Jobs"`
		MaxBaseLevel uint16          `yaml:"MaxBaseLevel"`
		BaseExp      []ExpLevel      `yaml:"BaseExp"`
		MaxJobLevel  uint16          `yaml:"MaxJobLevel"`
		JobExp       []ExpLevel      `yaml:"JobExp"`
	} `yaml:"Body"`
}

// Registry is a lookup table mapping job names to their EXP curves.
type Registry struct {
	jobs map[string]*JobEntry
}

// Load parses job_exp.yml from r and builds a Registry.
func Load(r io.Reader) (*Registry, error) {
	var f fileFormat
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("parse job_exp yaml: %w", err)
	}
	if err := validateHeader(f.Header.Type, f.Header.Version); err != nil {
		return nil, err
	}

	jobs := make(map[string]*JobEntry)
	for _, group := range f.Body {
		for jobName, enabled := range group.Jobs {
			if !enabled {
				continue
			}
			entry := jobs[jobName]
			if entry == nil {
				entry = &JobEntry{Jobs: []string{jobName}}
				jobs[jobName] = entry
			}
			if entry.MaxBaseLevel == 0 && len(group.BaseExp) > 0 {
				entry.MaxBaseLevel = group.MaxBaseLevel
				entry.BaseExp = group.BaseExp
			}
			if entry.MaxJobLevel == 0 && len(group.JobExp) > 0 {
				entry.MaxJobLevel = group.MaxJobLevel
				entry.JobExp = group.JobExp
			}
		}
	}
	return &Registry{jobs: jobs}, nil
}

func validateHeader(headerType string, version uint32) error {
	switch headerType {
	case "JOB_STATS":
		if version != 4 {
			return fmt.Errorf("job_exp: unsupported Header.Version %d for JOB_STATS (want 4)", version)
		}
	case "JOB_DB":
		if version != 1 {
			return fmt.Errorf("job_exp: unsupported Header.Version %d for JOB_DB (want 1)", version)
		}
	default:
		return fmt.Errorf("job_exp: unexpected Header.Type %q (want %q or %q)", headerType, "JOB_STATS", "JOB_DB")
	}
	return nil
}

// LoadFile opens path and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured job_exp.yml, not user input
	if err != nil {
		return nil, fmt.Errorf("open job_exp %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// Get returns the JobEntry for the given job name, or nil if not found.
func (reg *Registry) Get(jobName string) *JobEntry {
	if reg == nil {
		return nil
	}
	return reg.jobs[jobName]
}

// Len returns the number of unique job names in the registry.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.jobs)
}

// BaseExpForLevel returns the base EXP threshold for a job and level.
func (reg *Registry) BaseExpForLevel(jobName string, level int) uint64 {
	entry := reg.Get(jobName)
	if entry == nil || level <= 0 || level > int(entry.MaxBaseLevel) {
		return 0
	}
	return expForLevel(entry.BaseExp, level)
}

// JobExpForLevel returns the job EXP threshold for a job and level.
func (reg *Registry) JobExpForLevel(jobName string, level int) uint64 {
	entry := reg.Get(jobName)
	if entry == nil || level <= 0 || level > int(entry.MaxJobLevel) {
		return 0
	}
	return expForLevel(entry.JobExp, level)
}

func expForLevel(levels []ExpLevel, level int) uint64 {
	for _, exp := range levels {
		if int(exp.Level) == level {
			return exp.Exp
		}
	}
	return 0
}
