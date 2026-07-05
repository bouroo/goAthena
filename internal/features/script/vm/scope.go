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
