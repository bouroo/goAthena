package vm

// ScopeStore holds variables for all 6 rAthena scopes.
// In Phase 3, all scopes are in-memory. DB persistence is deferred.
type ScopeStore struct {
	tempVars     map[string]Value // .@var (NPC instance scope)
	charVars     map[string]Value // var (character scope)
	accountVars  map[string]Value // #var (account scope)
	mapVars      map[string]Value // $var (map-local permanent)
	mapTempVars  map[string]Value // $@var (map-local temporary)
	instanceVars map[string]Value // 'var (instance scope)

	// arrays stores named arrays by their full name (including the scope
	// prefix, e.g. ".@arr"). Arrays are not split per scope because the
	// name is already scope-disambiguated; this mirrors the way the
	// scalar maps route by name prefix while keeping struct size flat.
	arrays map[string]map[int64]Value
}

// NewScopeStore creates an empty in-memory scope store.
func NewScopeStore() *ScopeStore {
	return &ScopeStore{
		tempVars:     make(map[string]Value),
		charVars:     make(map[string]Value),
		accountVars:  make(map[string]Value),
		mapVars:      make(map[string]Value),
		mapTempVars:  make(map[string]Value),
		instanceVars: make(map[string]Value),
		arrays:       make(map[string]map[int64]Value),
	}
}

// Get retrieves a variable value by name.
// The scope is determined by the name prefix:
//
//	.@ → tempVars
//	#  → accountVars
//	$@ → mapTempVars
//	$  → mapVars
//	'  → instanceVars
//	(no prefix) → charVars
func (s *ScopeStore) Get(name string) (Value, bool) {
	m := s.selectMap(name)
	v, ok := m[name]
	return v, ok
}

// Set stores a variable value.
func (s *ScopeStore) Set(name string, val Value) {
	m := s.selectMap(name)
	m[name] = val
}

// GetArray returns the value stored at name[idx] and whether it exists.
// The returned Value is the zero Value (IsString=false, Int=0) when the
// array or the index has never been written, mirroring rAthena's
// "uninitialized array element is 0" semantics.
func (s *ScopeStore) GetArray(name string, idx int64) (Value, bool) {
	arr, ok := s.arrays[name]
	if !ok {
		return IntValue(0), false
	}
	v, ok := arr[idx]
	if !ok {
		return IntValue(0), false
	}
	return v, true
}

// SetArray stores val at name[idx], lazily creating the inner map the
// first time the array is written to.
func (s *ScopeStore) SetArray(name string, idx int64, val Value) {
	arr, ok := s.arrays[name]
	if !ok {
		arr = make(map[int64]Value)
		s.arrays[name] = arr
	}
	arr[idx] = val
}

// selectMap returns the underlying map for a variable name based on its prefix.
func (s *ScopeStore) selectMap(name string) map[string]Value {
	switch {
	case len(name) >= 2 && name[0] == '.' && name[1] == '@':
		return s.tempVars
	case len(name) >= 1 && name[0] == '#':
		return s.accountVars
	case len(name) >= 2 && name[0] == '$' && name[1] == '@':
		return s.mapTempVars
	case len(name) >= 1 && name[0] == '$':
		return s.mapVars
	case len(name) >= 1 && name[0] == '\'':
		return s.instanceVars
	default:
		return s.charVars
	}
}
