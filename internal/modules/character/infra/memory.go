package infra

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
)

// MemoryCharacterRepository is an in-memory domain.CharacterRepository for
// hermetic unit tests. It returns defensive copies so callers cannot mutate the
// store through a returned slice or pointer.
type MemoryCharacterRepository struct {
	bySlot map[accountSlot]domain.Character
}

type accountSlot struct {
	accountID uint32
	slot      uint8
}

// NewMemoryCharacterRepository seeds the store with copies of the given
// characters, keyed by (accountID, slot).
func NewMemoryCharacterRepository(seeds ...domain.Character) *MemoryCharacterRepository {
	r := &MemoryCharacterRepository{bySlot: make(map[accountSlot]domain.Character, len(seeds))}
	for _, c := range seeds {
		r.bySlot[accountSlot{c.AccountID, c.Slot}] = c
	}
	return r
}

// ListByAccount returns defensive copies of the account's characters, sorted by
// slot for a stable display. An account with no characters yields an empty
// (non-nil) slice and a nil error.
func (r *MemoryCharacterRepository) ListByAccount(_ context.Context, accountID uint32) ([]domain.Character, error) {
	var out []domain.Character
	for k, c := range r.bySlot {
		if k.accountID == accountID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return slices.Clone(out), nil
}

// GetBySlot returns a defensive copy or ErrCharacterNotFound.
func (r *MemoryCharacterRepository) GetBySlot(_ context.Context, accountID uint32, slot uint8) (*domain.Character, error) {
	c, ok := r.bySlot[accountSlot{accountID, slot}]
	if !ok {
		return nil, domain.ErrCharacterNotFound
	}
	cp := c
	return &cp, nil
}

// GetByID returns the character at (accountID, charID). The store is keyed by
// (accountID, slot), so this scans for a matching char_id within the account —
// a linear cost that is fine for the hermetic unit-test seed sizes. Scoping by
// accountID is the impersonation guard: a char_id from another account yields
// ErrCharacterNotFound, never the wrong character.
func (r *MemoryCharacterRepository) GetByID(_ context.Context, accountID uint32, charID uint32) (*domain.Character, error) {
	for k, c := range r.bySlot {
		if k.accountID == accountID && c.CharID == charID {
			cp := c
			return &cp, nil
		}
	}
	return nil, domain.ErrCharacterNotFound
}

// Create mirrors GORMCharacterRepository.Create: it guards name uniqueness and
// slot occupancy, then stores a novice-defaults character with a char_id chosen
// to mirror the DB's AUTO_INCREMENT (next = max(existing)+1, or 150000 when
// empty). Name comparison is case-insensitive to match the char table's default
// (case-insensitive) collation. It does not re-run the pure input validation
// (empty/short/control-char/reserved) — that is the handler's responsibility.
func (r *MemoryCharacterRepository) Create(_ context.Context, in domain.CreateCharacter) (*domain.Character, error) {
	name := strings.ToLower(strings.TrimSpace(in.Name))

	for _, c := range r.bySlot {
		if strings.ToLower(strings.TrimSpace(c.Name)) == name {
			return nil, domain.ErrCharNameTaken
		}
	}
	key := accountSlot{in.AccountID, in.Slot}
	if _, occupied := r.bySlot[key]; occupied {
		return nil, domain.ErrSlotOccupied
	}

	created := newNoviceDomain(in, r.nextCharID())
	r.bySlot[key] = created
	cp := created
	return &cp, nil
}

// nextCharID mirrors the char table's AUTO_INCREMENT: the next id is the largest
// in use plus one, or the initial seed (150000) when the store is empty.
func (r *MemoryCharacterRepository) nextCharID() uint32 {
	var maxID uint32 = firstCharID - 1
	for _, c := range r.bySlot {
		if c.CharID > maxID {
			maxID = c.CharID
		}
	}
	return maxID + 1
}

// firstCharID is the AUTO_INCREMENT seed of the char table (migration
// 000002_identity.up.sql: AUTO_INCREMENT=150000).
const firstCharID = 150000

// newNoviceDomain is the domain twin of newNoviceModel — same novice defaults,
// expressed as a domain.Character for the in-memory adapter.
func newNoviceDomain(in domain.CreateCharacter, charID uint32) domain.Character {
	return domain.Character{
		CharID:      charID,
		AccountID:   in.AccountID,
		Slot:        in.Slot,
		Name:        strings.TrimSpace(in.Name),
		Class:       uint16(in.Job), //nolint:gosec // G115: packet job→uint16 (domain Class), mirrors GORM adapter
		BaseLevel:   1,
		JobLevel:    1,
		Str:         1,
		Agi:         1,
		Vit:         1,
		Int:         1,
		Dex:         1,
		Luk:         1,
		MaxHP:       noviceMaxHP,
		HP:          noviceMaxHP,
		MaxSP:       noviceMaxSP,
		SP:          noviceMaxSP,
		StatusPoint: noviceStatusPoints,
		Hair:        uint8(in.HairStyle), //nolint:gosec // G115: packet hair_style→uint8 (domain Hair), mirrors GORM adapter
		HairColor:   in.HairColor,
		Body:        uint16(in.Job), //nolint:gosec // G115: packet job→uint16 (domain Body), mirrors GORM adapter
		LastMap:     noviceStartMap,
		LastX:       noviceStartX,
		LastY:       noviceStartY,
		Sex:         in.Sex,
	}
}
