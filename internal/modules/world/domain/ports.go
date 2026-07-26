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
