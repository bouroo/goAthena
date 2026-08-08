// Package itemdb loads rAthena item_db*.yml (version 3) into a lookup registry.
package itemdb

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bouroo/goAthena/pkg/ro/equip"
)

// ItemEntry holds scalar fields for a single item type.
type ItemEntry struct {
	Id            int32           `yaml:"Id"` //nolint:revive // yaml tag is "Id" to match rAthena item_db.yml
	AegisName     string          `yaml:"AegisName"`
	Name          string          `yaml:"Name"`
	Type          string          `yaml:"Type"`
	SubType       string          `yaml:"SubType"`
	Buy           int32           `yaml:"Buy"`
	Sell          int32           `yaml:"Sell"`
	Weight        int32           `yaml:"Weight"`
	Attack        int32           `yaml:"Attack"`
	Defense       int32           `yaml:"Defense"`
	Range         int32           `yaml:"Range"`
	Slots         int32           `yaml:"Slots"`
	WeaponLevel   int32           `yaml:"WeaponLevel"`
	ArmorLevel    int32           `yaml:"ArmorLevel"`
	EquipLevelMin int32           `yaml:"EquipLevelMin"`
	EquipLevelMax int32           `yaml:"EquipLevelMax"`
	Refineable    bool            `yaml:"Refineable"`
	View          int32           `yaml:"View"`
	Locations     map[string]bool `yaml:"Locations"`
	Script        string          `yaml:"Script"`
	// EquipLocations is the EQP_* bitmask derived from Locations in build()
	// (the value the inventory.equip column stores and the equip use case reads).
	// It is not a YAML field.
	EquipLocations uint32 `yaml:"-"`

	healHPMin int32
	healHPMax int32
	healSPMin int32
	healSPMax int32
	healOK    bool
}

var (
	itemHealPattern      = regexp.MustCompile(`(?is)\bitemheal\s+((?:rand\s*\([^)]*\)|[^,;\s]+))\s*,\s*((?:rand\s*\([^)]*\)|[^;,\s]+))`)
	itemHealValuePattern = regexp.MustCompile(`^(?:([+-]?\d+)|rand\(\s*([+-]?\d+)\s*,\s*([+-]?\d+)\s*\))$`)
)

func (e *ItemEntry) parseHeal() {
	match := itemHealPattern.FindStringSubmatch(e.Script)
	if len(match) != 3 {
		return
	}
	hpMin, hpMax, ok := parseHealValue(match[1])
	if !ok {
		return
	}
	spMin, spMax, ok := parseHealValue(match[2])
	if !ok {
		return
	}
	e.healHPMin, e.healHPMax = hpMin, hpMax
	e.healSPMin, e.healSPMax = spMin, spMax
	e.healOK = true
}

func parseHealValue(value string) (lo, hi int32, ok bool) {
	match := itemHealValuePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) == 0 {
		return 0, 0, false
	}
	if match[1] != "" {
		n, err := strconv.ParseInt(match[1], 10, 32)
		if err != nil {
			return 0, 0, false
		}
		return int32(n), int32(n), true
	}
	lo64, err := strconv.ParseInt(match[2], 10, 32)
	if err != nil {
		return 0, 0, false
	}
	hi64, err := strconv.ParseInt(match[3], 10, 32)
	if err != nil || lo64 > hi64 {
		return 0, 0, false
	}
	return int32(lo64), int32(hi64), true
}

// Heal returns the inclusive HP/SP ranges parsed from the item's itemheal script.
func (e *ItemEntry) Heal() (hpMin, hpMax, spMin, spMax int32, ok bool) {
	if e == nil || !e.healOK {
		return 0, 0, 0, 0, false
	}
	return e.healHPMin, e.healHPMax, e.healSPMin, e.healSPMax, true
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version int    `yaml:"Version"`
	} `yaml:"Header"`
	Body []*ItemEntry `yaml:"Body"`
}

// Registry provides thread-safe lookup of item entries by ID.
//
// Both maps are populated once at Load time and never mutated afterwards,
// so concurrent reads (the only operation after construction) are safe
// without locking.
type Registry struct {
	entries map[int32]*ItemEntry
	aegis   map[string]*ItemEntry // reverse lookup AegisName → entry
}

// Load parses a rAthena item_db YAML file from an io.Reader and returns a Registry.
// It expects the rAthena YAML format with Header.Type=="ITEM_DB" and Header.Version==3.
// Unknown fields are silently ignored. The document is streamed through
// yaml.NewDecoder to avoid buffering the entire (multi-MB) item_db.yml
// in memory before decoding.
func Load(r io.Reader) (*Registry, error) {
	body, err := decode(r)
	if err != nil {
		return nil, err
	}
	return build(body), nil
}

// decode reads one ITEM_DB v3 document from r and returns its Body. It validates
// the header (Type=="ITEM_DB", Version==3) so a wrong-format file fails fast
// rather than silently yielding an empty registry. Unknown fields are ignored.
//
// Duplicate mapping keys are tolerated (last-wins) to match rAthena's loader.
// Some vendored item_db YAMLs repeat a block within one item (e.g. two Trade:
// stanzas); yaml.v3 rejects those when unmarshaling into a struct. We decode the
// raw node tree first — which preserves duplicates without erroring — collapse
// them, then unmarshal into the struct. The cost is one full-tree walk at the
// multi-MB startup load; the resulting node is the same size the struct decode
// would allocate anyway.
func decode(r io.Reader) ([]*ItemEntry, error) {
	var node yaml.Node
	if err := yaml.NewDecoder(r).Decode(&node); err != nil {
		return nil, fmt.Errorf("parse item_db yaml: %w", err)
	}
	dedupeMappingKeys(&node)
	var f fileFormat
	if err := node.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse item_db yaml: %w", err)
	}
	if f.Header.Type != "ITEM_DB" {
		return nil, fmt.Errorf("item_db: unexpected Header.Type %q (want %q)", f.Header.Type, "ITEM_DB")
	}
	if f.Header.Version != 3 {
		return nil, fmt.Errorf("item_db: unsupported Header.Version %d (want 3)", f.Header.Version)
	}
	return f.Body, nil
}

// build constructs a Registry from one or more item bodies, merging them. A nil
// entry is skipped; a blank Type defaults to "Etc". A duplicate Id or AegisName
// across bodies is a data error; last write wins to stay deterministic. mob_db
// drop tables carry an AegisName, not a numeric id, so the reverse map is what
// lets a drop resolve its item.
func build(bodies ...[]*ItemEntry) *Registry {
	entries := make(map[int32]*ItemEntry)
	aegis := make(map[string]*ItemEntry)
	for _, body := range bodies {
		for _, entry := range body {
			if entry == nil {
				continue
			}
			if entry.Type == "" {
				entry.Type = "Etc"
			}
			entry.EquipLocations = equip.LocationBits(entry.Locations)
			entry.parseHeal()
			entries[entry.Id] = entry
			if entry.AegisName != "" {
				aegis[entry.AegisName] = entry
			}
		}
	}
	return &Registry{entries: entries, aegis: aegis}
}

// LoadFile is a convenience wrapper that opens a file and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured item_db.yml, not user input
	if err != nil {
		return nil, fmt.Errorf("open item_db %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// LoadFiles loads and merges one or more ITEM_DB v3 files into one Registry.
// rAthena ships item_db as a router file (item_db.yml) whose Footer.Imports point
// at item_db_usable.yml / item_db_equip.yml / item_db_etc.yml — the actual item
// bodies. That router has no Body, so loading it directly yields zero items;
// LoadFiles consumes the sub-files directly and merges them. A missing file is
// skipped (ENOENT) so a partial fork subtree still yields whatever items are
// present; any other open error, or a parse error in any file, aborts. When every
// path is absent the result is an empty (non-nil) Registry — callers treat
// Len()==0 as "no items" (the world boots with no item drops, mirroring the
// nil-mob_db contract).
func LoadFiles(paths ...string) (*Registry, error) {
	var bodies [][]*ItemEntry
	for _, p := range paths {
		f, err := os.Open(p) // #nosec G304 -- paths are operator-configured item_db files, not user input
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("open item_db %q: %w", p, err)
		}
		body, err := decode(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("item_db %q: %w", p, err)
		}
		bodies = append(bodies, body)
	}
	return build(bodies...), nil
}

// Get returns the ItemEntry for the given item ID, or nil if not found.
func (reg *Registry) Get(id int32) *ItemEntry {
	if reg == nil {
		return nil
	}
	return reg.entries[id]
}

// ByAegisName returns the ItemEntry whose AegisName matches, or nil if not
// found. Used to resolve mob_db drop entries (which carry an AegisName
// string) to the numeric item id the wire format requires.
func (reg *Registry) ByAegisName(name string) *ItemEntry {
	if reg == nil || name == "" {
		return nil
	}
	return reg.aegis[name]
}

// Len returns the number of loaded item entries.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.entries)
}

// Weight returns the non-negative weight for the given item name ID.
func (reg *Registry) Weight(nameID uint32) uint32 {
	const maxItemID = uint32(1<<31 - 1)
	if nameID > maxItemID {
		return 0
	}

	entry := reg.Get(int32(nameID))
	if entry == nil || entry.Weight < 0 {
		return 0
	}
	return uint32(entry.Weight)
}

// itemTypeZero is the IT_* value itemdb.cpp falls back to for an invalid or
// blank Type (itemdb.cpp:145,151: "defaulting to IT_ETC"). Exported via WireType
// so callers (and tests) share one source of truth for the default.
const itemTypeEtc uint16 = 3

// WireType maps an item_db Type string to the rAthena IT_* enum the wire format
// carries in floor-item and inventory packets (clif_dropflooritem's itemtype();
// itemdb.cpp resolves the same string to the enum at load via script constants).
// Faithful to clif.cpp itemtype()'s clamps for the common drop types; an unknown
// or empty string returns IT_ETC, matching itemdb.cpp's load-time default. Pet
// eggs / pet armor / shadowgear (which the client renders as armor absent
// equip-location data) approximate to IT_ARMOR — none appear in the basic
// mob-drop corpus, so the clamp is a safe default rather than a verified path.
func WireType(typeStr string) uint16 {
	switch strings.ToLower(typeStr) {
	case "healing":
		return 0 // IT_HEALING
	case "usable", "unknown", "delayconsume":
		return 2 // IT_USABLE (DelayConsume folds to Usable at load, itemdb.cpp:1122)
	case "etc", "":
		return itemTypeEtc // IT_ETC
	case "armor", "petegg", "petarmor", "shadowgear":
		return 4 // IT_ARMOR (itemtype clamps pet/shadowgear absent equip data)
	case "weapon":
		return 5 // IT_WEAPON
	case "card":
		return 6 // IT_CARD
	case "ammo":
		return 10 // IT_AMMO
	case "cash":
		return 13 // IT_CASH
	default:
		return itemTypeEtc // IT_ETC (itemdb.cpp default)
	}
}

// IsStackable reports whether the item type stacks in rAthena's inventory
// model (itemdb.cpp:4932 item_data::isStackable): false for Weapon, Armor,
// PetEgg, PetArmor, and ShadowGear; true otherwise. Unknown nameids (and a
// nil registry) return true — the AddItem merge only commits when a matching
// plain stack already exists, so a permissive default cannot corrupt a
// distinct item.
//
// The comparison is case-insensitive because the shipped YAMLs are
// inconsistent: renewal item_db.yml uses both "ShadowGear" and "Shadowgear"
// for shadow-gear entries, and rAthena's own Type strings vary in casing.
func (reg *Registry) IsStackable(nameID uint32) bool {
	const maxItemID = uint32(1<<31 - 1)
	if nameID > maxItemID {
		return true
	}
	entry := reg.Get(int32(nameID))
	if entry == nil {
		return true
	}
	switch {
	case strings.EqualFold(entry.Type, "Weapon"),
		strings.EqualFold(entry.Type, "Armor"),
		strings.EqualFold(entry.Type, "Petegg"),
		strings.EqualFold(entry.Type, "Petarmor"),
		strings.EqualFold(entry.Type, "Shadowgear"):
		return false
	default:
		return true
	}
}

// dedupeMappingKeys recursively collapses duplicate keys in every mapping node,
// keeping the last occurrence (rAthena's loader semantics). A mapping node's
// Content is a flat [key, value, key, value, ...] slice; on a repeat scalar key
// the earlier pair is dropped so the struct decode that follows sees a single
// value. Document and sequence nodes carry no keys themselves but must be
// descended to reach the item mappings inside the Body list.
func dedupeMappingKeys(n *yaml.Node) {
	switch {
	case n == nil:
		return
	case n.Kind == yaml.MappingNode:
		seen := make(map[string]int, len(n.Content)/2) // key text -> index of its key node in out
		out := make([]*yaml.Node, 0, len(n.Content))
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind == yaml.ScalarNode {
				if idx, dup := seen[k.Value]; dup {
					out[idx], out[idx+1] = k, v // last-wins replace in place
					continue
				}
				seen[k.Value] = len(out)
			}
			out = append(out, k, v)
		}
		n.Content = out
		fallthrough
	case n.Kind == yaml.DocumentNode || n.Kind == yaml.SequenceNode:
		for _, c := range n.Content {
			dedupeMappingKeys(c)
		}
	}
}
