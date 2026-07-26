package domain

import (
	"context"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
)

// CharacterGetter is the world context's view of character persistence: only
// the single-character lookup the spawn-on-enter flow needs, not the full
// CharacterRepository (Create/SaveProgression are not world concerns). The
// character module's CharacterRepository satisfies this structurally; the
// world module names its own port so its tests can substitute a fake without
// importing character/infra.
//
// The lookup is scoped by accountID (the impersonation guard): a CZ_ENTER
// carrying another account's char_id yields ErrCharacterNotFound, never the
// wrong character. AccountID must come from the verified session, not the
// packet's client-controlled AccountID field.
type CharacterGetter interface {
	GetByID(ctx context.Context, accountID, charID uint32) (*chardomain.Character, error)
}

// ProgressionStore is the world combat use case's view of character persistence:
// it loads a character (to read the current EXP/level) and writes back the
// levelable slice after a kill. It is satisfied structurally by the character
// module's CharacterRepository. Named in the world context — alongside
// CharacterGetter — so combat's unit tests substitute a fake without importing
// character/infra. The SaveProgression scoping contract (accountID +
// charID, never a client-supplied id) is the impersonation guard shared with
// CharacterGetter and the CZ_ENTER gate.
type ProgressionStore interface {
	GetByID(ctx context.Context, accountID, charID uint32) (*chardomain.Character, error)
	SaveProgression(ctx context.Context, accountID, charID uint32, p chardomain.Progression) error
}
