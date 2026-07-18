// Package skilltree loads rAthena skill_tree.yml (Header SKILL_TREE_DB, Version 1) into a per-job skill tree registry with inheritance resolution.
package skilltree

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// ErrCircularInherit is returned (wrapped) when the Inherit graph between jobs
// contains a cycle. Detect it with errors.Is.
var ErrCircularInherit = errors.New("circular Inherit")

// SkillRequirement is a prerequisite skill and the level it must reach for
// another skill in the same job to be unlockable.
type SkillRequirement struct {
	Name  string
	Level int16
}

// SkillEntry is one skill as it appears in a job's resolved tree. Every
// *SkillEntry stored in a Registry is a unique allocation per (job, skill):
// mutating the entry returned for one job MUST NOT affect any other job.
type SkillEntry struct {
	Name      string
	MaxLevel  int16
	Exclude   bool
	BaseLevel int16
	JobLevel  int16
	Requires  []SkillRequirement
}

// JobTree is a single job's resolved skill tree, keyed by skill Name.
type JobTree struct {
	Job    string
	Skills map[string]*SkillEntry
}

// Registry is a per-job lookup of resolved skill trees.
type Registry struct {
	jobs map[string]*JobTree
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version int    `yaml:"Version"`
	} `yaml:"Header"`
	Body []*jobNode `yaml:"Body"`
}

type jobNode struct {
	Job     string             `yaml:"Job"`
	Inherit map[string]bool    `yaml:"Inherit"`
	Tree    []*skillEntryInner `yaml:"Tree"`
}

// skillEntryInner mirrors SkillEntry with explicit yaml tags so that
// yaml.v3 maps the rAthena PascalCase keys to the lowercased Go fields.
type skillEntryInner struct {
	Name      string              `yaml:"Name"`
	MaxLevel  int16               `yaml:"MaxLevel"`
	Exclude   bool                `yaml:"Exclude"`
	BaseLevel int16               `yaml:"BaseLevel"`
	JobLevel  int16               `yaml:"JobLevel"`
	Requires  []*requirementInner `yaml:"Requires"`
}

type requirementInner struct {
	Name  string `yaml:"Name"`
	Level int16  `yaml:"Level"`
}

const (
	expectedHeaderType    = "SKILL_TREE_DB"
	expectedHeaderVersion = 1
)

// parsedJob holds the data extracted from one Body entry: the Inherit set
// (used to resolve inheritance) and the OWN parsed tree, before any merge.
type parsedJob struct {
	name    string
	inherit map[string]bool
	own     map[string]*SkillEntry
}

// Load parses a rAthena skill_tree YAML stream and returns a Registry with all
// inheritances resolved. It expects Header.Type=="SKILL_TREE_DB" and
// Header.Version==1, otherwise it returns a wrapped error.
func Load(r io.Reader) (*Registry, error) {
	var f fileFormat
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse skill_tree yaml: %w", err)
	}

	if f.Header.Type != expectedHeaderType {
		return nil, fmt.Errorf("skill_tree: unexpected Header.Type %q (want %q)", f.Header.Type, expectedHeaderType)
	}
	if f.Header.Version != expectedHeaderVersion {
		return nil, fmt.Errorf("skill_tree: unsupported Header.Version %d (want %d)", f.Header.Version, expectedHeaderVersion)
	}

	if len(f.Body) == 0 {
		return nil, errors.New("skill_tree: empty Body")
	}

	parsed, err := parseBody(f.Body)
	if err != nil {
		return nil, err
	}

	if err := validateAncestors(parsed); err != nil {
		return nil, err
	}

	if err := detectCycles(parsed); err != nil {
		return nil, err
	}

	resolved := resolveInheritance(parsed)

	pruneMaxLevelZero(resolved)

	return &Registry{jobs: resolved}, nil
}

// LoadFile is a convenience wrapper that opens a file and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured skill_tree.yml, not user input
	if err != nil {
		return nil, fmt.Errorf("open skill_tree %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// Get returns the JobTree for the given job name, or (nil, false) if not
// present. It is nil-safe on a nil receiver.
func (reg *Registry) Get(jobName string) (*JobTree, bool) {
	if reg == nil {
		return nil, false
	}
	jt, ok := reg.jobs[jobName]
	return jt, ok
}

// Len returns the number of loaded jobs. It is nil-safe on a nil receiver.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.jobs)
}

// Jobs returns the registered job names sorted ascending. It is nil-safe on a
// nil receiver and returns nil for an empty/nil registry.
func (reg *Registry) Jobs() []string {
	if reg == nil || len(reg.jobs) == 0 {
		return nil
	}
	out := make([]string, 0, len(reg.jobs))
	for name := range reg.jobs {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func parseBody(body []*jobNode) (map[string]*parsedJob, error) {
	out := make(map[string]*parsedJob, len(body))
	for i, node := range body {
		if node == nil {
			continue
		}
		if node.Job == "" {
			return nil, fmt.Errorf("skill_tree: body index %d: missing Job name", i)
		}
		if _, dup := out[node.Job]; dup {
			return nil, fmt.Errorf("skill_tree: duplicate Job %q", node.Job)
		}
		own, err := parseOwnSkills(node.Job, node.Tree)
		if err != nil {
			return nil, err
		}
		out[node.Job] = &parsedJob{
			name:    node.Job,
			inherit: node.Inherit,
			own:     own,
		}
	}
	if len(out) == 0 {
		return nil, errors.New("skill_tree: empty Body")
	}
	return out, nil
}

func parseOwnSkills(jobName string, entries []*skillEntryInner) (map[string]*SkillEntry, error) {
	out := make(map[string]*SkillEntry, len(entries))
	for i, e := range entries {
		if e == nil {
			continue
		}
		if e.Name == "" {
			return nil, fmt.Errorf("skill_tree: job %q tree index %d: missing skill Name", jobName, i)
		}
		if _, dup := out[e.Name]; dup {
			return nil, fmt.Errorf("skill_tree: job %q has duplicate skill %q", jobName, e.Name)
		}
		reqs := make([]SkillRequirement, 0, len(e.Requires))
		for _, r := range e.Requires {
			if r == nil {
				continue
			}
			reqs = append(reqs, SkillRequirement{Name: r.Name, Level: r.Level})
		}
		out[e.Name] = &SkillEntry{
			Name:      e.Name,
			MaxLevel:  e.MaxLevel,
			Exclude:   e.Exclude,
			BaseLevel: e.BaseLevel,
			JobLevel:  e.JobLevel,
			Requires:  reqs,
		}
	}
	return out, nil
}

func validateAncestors(jobs map[string]*parsedJob) error {
	for name, pj := range jobs {
		for ancestor := range pj.inherit {
			if _, ok := jobs[ancestor]; !ok {
				return fmt.Errorf("skill_tree: job %q inherits unknown job %q", name, ancestor)
			}
		}
	}
	return nil
}

func detectCycles(jobs map[string]*parsedJob) error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int, len(jobs))
	for name := range jobs {
		color[name] = white
	}
	ancestorsOf := func(name string) []string {
		pj := jobs[name]
		if pj == nil || len(pj.inherit) == 0 {
			return nil
		}
		out := make([]string, 0, len(pj.inherit))
		for a := range pj.inherit {
			out = append(out, a)
		}
		slices.Sort(out)
		return out
	}

	var dfs func(name string) error
	dfs = func(name string) error {
		color[name] = grey
		for _, next := range ancestorsOf(name) {
			switch color[next] {
			case grey:
				return fmt.Errorf("skill_tree: circular Inherit detected at %q: %w", name, ErrCircularInherit)
			case white:
				if err := dfs(next); err != nil {
					return err
				}
			}
		}
		color[name] = black
		return nil
	}

	for name := range jobs {
		if color[name] == white {
			if err := dfs(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveInheritance(jobs map[string]*parsedJob) map[string]*JobTree {
	out := makeOwnTrees(jobs)
	for name, pj := range jobs {
		if len(pj.inherit) == 0 {
			continue
		}
		inherited := mergeAncestors(pj, jobs, out[name])
		maps.Copy(out[name].Skills, inherited)
	}
	return out
}

func makeOwnTrees(jobs map[string]*parsedJob) map[string]*JobTree {
	out := make(map[string]*JobTree, len(jobs))
	for name, pj := range jobs {
		skills := make(map[string]*SkillEntry, len(pj.own))
		maps.Copy(skills, pj.own)
		out[name] = &JobTree{Job: name, Skills: skills}
	}
	return out
}

func mergeAncestors(pj *parsedJob, jobs map[string]*parsedJob, jt *JobTree) map[string]*SkillEntry {
	inherited := make(map[string]*SkillEntry)
	ancestors := sortedKeys(pj.inherit)
	for _, a := range ancestors {
		apj, ok := jobs[a]
		if !ok || apj == nil || len(apj.own) == 0 {
			continue
		}
		for _, sn := range sortedSkillNames(apj.own) {
			src := apj.own[sn]
			if src == nil || src.Exclude {
				continue
			}
			if _, owns := jt.Skills[sn]; owns {
				continue
			}
			inherited[sn] = cloneSkillEntry(src)
		}
	}
	return inherited
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sortedSkillNames(m map[string]*SkillEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func pruneMaxLevelZero(jobs map[string]*JobTree) {
	for _, jt := range jobs {
		if jt == nil {
			continue
		}
		for sn, entry := range jt.Skills {
			if entry == nil || entry.MaxLevel == 0 {
				delete(jt.Skills, sn)
			}
		}
	}
}

func cloneSkillEntry(src *SkillEntry) *SkillEntry {
	if src == nil {
		return nil
	}
	dup := *src
	if src.Requires != nil {
		dup.Requires = make([]SkillRequirement, len(src.Requires))
		copy(dup.Requires, src.Requires)
	}
	return &dup
}
