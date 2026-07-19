package athenaconf

import (
	"errors"
	"fmt"
	"sort"

	"github.com/bouroo/goAthena/internal/config"
)

// keyMap maps rAthena .conf keys to setter closures that mutate a
// config.Config in place. The Initial key map (decision D-008) is
// intentionally narrow: only keys with a typed Config field home today
// are wired; everything else is surfaced via Manifest.Unmapped so the
// round-trip test can assert it is non-empty.
//
// Adding a new mapping is a one-line table entry.
// keyMapEntry binds a primary rAthena key to a setter closure. aliases
// lists every other rAthena spelling the entry also accepts (kept for
// documentation; the map itself is keyed by the primary key only, so
// lookup is O(1)).
type keyMapEntry struct {
	aliases []string
	apply   func(cfg *config.Config, v Value) error
}

// initialKeyMap returns a fresh C1 mapping table each call so callers
// cannot mutate the package-level state. Order is irrelevant; lookup
// is by rAthena key string.
func initialKeyMap() map[string]keyMapEntry {
	out := map[string]keyMapEntry{}

	out["use_MD5_passwords"] = keyMapEntry{
		aliases: []string{"use_MD5_passwords", "use_md5_passwds"},
		apply: func(cfg *config.Config, v Value) error {
			if v.Kind != KindBool {
				return fmt.Errorf("use_MD5_passwords: expected bool, got %s", v.Kind)
			}
			cfg.Identity.UseMD5Passwords = v.Bool
			return nil
		},
	}

	out["chars_per_account"] = keyMapEntry{
		aliases: []string{"chars_per_account", "max_char"},
		apply: func(cfg *config.Config, v Value) error {
			if v.Kind != KindInt {
				return fmt.Errorf("chars_per_account: expected int, got %s", v.Kind)
			}
			cfg.Identity.MaxChars = int(v.Int)
			return nil
		},
	}

	return out
}

// ApplyToConfig overlays parsed rAthena conf values onto an existing
// Config. Only keys with a known mapping in the Initial key map are
// applied; every other key present in f.Keys is appended to
// manifest.Unmapped so the round-trip test can verify nothing was
// silently dropped. Unknown keys are NOT errors.
//
// Apply errors from individual keys (e.g. type mismatch) are returned
// joined; the caller decides whether to abort or log and continue.
func ApplyToConfig(cfg *config.Config, f *File, manifest *Manifest) error {
	if manifest == nil {
		manifest = &Manifest{KeyOrigin: map[string]string{}}
	}

	mappings := initialKeyMap()

	// Flatten aliases into the same map so both the primary key and every
	// alias resolve in O(1). Aliases share the primary entry's apply fn.
	aliasMap := make(map[string]keyMapEntry, len(mappings)*2)
	for primary, entry := range mappings {
		aliasMap[primary] = entry
		for _, alias := range entry.aliases {
			if alias != primary {
				aliasMap[alias] = entry
			}
		}
	}

	var errs []error
	for _, rathenaKey := range sortedKeys(f.Keys) {
		v := f.Keys[rathenaKey]
		entry, known := aliasMap[rathenaKey]
		if !known {
			manifest.Unmapped = append(manifest.Unmapped, rathenaKey)
			continue
		}
		if err := entry.apply(cfg, v); err != nil {
			errs = append(errs, err)
		}
	}

	sort.Strings(manifest.Unmapped)
	return errors.Join(errs...)
}

// sortedKeys returns f.Keys' keys in stable order (f.Order if present,
// else lexicographic). Used so the round-trip output is deterministic.
func sortedKeys(m map[string]Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
