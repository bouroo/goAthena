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

// JobEntry holds the EXP data for a group of jobs sharing the same curve.
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
	if f.Header.Type != "JOB_STATS" && f.Header.Type != "JOB_DB" {
		return nil, fmt.Errorf("job_exp: unexpected Header.Type %q (want %q)", f.Header.Type, "JOB_DB")
	}
	if f.Header.Type == "JOB_STATS" && f.Header.Version != 4 {
		return nil, fmt.Errorf("job_exp: unsupported Header.Version %d (want 4)", f.Header.Version)
	}
	if f.Header.Type == "JOB_DB" && f.Header.Version != 1 {
		return nil, fmt.Errorf("job_exp: unsupported Header.Version %d (want 1)", f.Header.Version)
	}

	jobs := make(map[string]*JobEntry)
	for _, group := range f.Body {
		jobNames := make([]string, 0, len(group.Jobs))
		for jobName, enabled := range group.Jobs {
			if !enabled {
				continue
			}
			if existing := jobs[jobName]; existing != nil {
				mergeEntry(existing, group.MaxBaseLevel, group.BaseExp, group.MaxJobLevel, group.JobExp)
				continue
			}
			jobNames = append(jobNames, jobName)
		}
		entry := &JobEntry{
			Jobs:         jobNames,
			MaxBaseLevel: group.MaxBaseLevel,
			BaseExp:      group.BaseExp,
			MaxJobLevel:  group.MaxJobLevel,
			JobExp:       group.JobExp,
		}
		for _, jobName := range jobNames {
			jobs[jobName] = entry
		}
	}
	return &Registry{jobs: jobs}, nil
}

func mergeEntry(entry *JobEntry, maxBaseLevel uint16, baseExp []ExpLevel, maxJobLevel uint16, jobExp []ExpLevel) {
	if len(entry.JobExp) == 0 && len(jobExp) > 0 {
		entry.MaxJobLevel = maxJobLevel
		entry.JobExp = jobExp
	}
	if len(entry.BaseExp) == 0 && len(baseExp) > 0 {
		entry.MaxBaseLevel = maxBaseLevel
		entry.BaseExp = baseExp
	}
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
