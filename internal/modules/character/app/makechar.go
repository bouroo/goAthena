package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// charNameMinLength is rAthena's char_name_min_length default (char_athena.conf),
// the 4-character minimum the client also enforces client-side. char_check_char_name
// rejects a shorter name with -2 (char.cpp:1347).
const charNameMinLength = 4

// wispServerName is the reserved name for internal server messages
// (char_athena.conf wisp_server_name = "Server"); char_check_char_name rejects a
// case-insensitive match with -1 (char.cpp:1354).
const wispServerName = "Server"

// jobNovice is JOB_NOVICE (0), the only starting class the combat-slice create
// flow permits. NOTE: rAthena's char_make_new_char trusts the client's job byte
// and inserts it verbatim; goAthena deviates by rejecting any other job, keeping
// the slice coherent (a client-controlled class would yield a level-1 non-novice).
const jobNovice uint32 = 0

// CharMakeHandler serves CH_MAKE_CHAR (0x0a39). It validates the client's
// creation request against the pure rules from char_check_char_name and
// char_make_new_char, then creates the character through the repository (which
// applies the server-side novice defaults) and replies HC_ACCEPT_MAKECHAR
// (0x0b6f) on success or HC_REFUSE_MAKECHAR (0x006e) on rejection.
//
// The owning account is read from the connection's auth cache — never the
// packet — so a crafted request cannot attach a character to another account.
// (CH_MAKE_CHAR carries no AccountID field anyway; the trust comes from the
// gateway having advanced this conn to RoleChar via a verified CH_ENTER.)
//
// Error policy matches CharEnterHandler/CharSelectHandler: a parse/encode fault
// or an unexpected repo error is returned so ProcessBytes logs it; an expected
// validation/refuse outcome is a nil error and the connection stays open,
// mirroring chclif_parse_createnewchar's refuse-and-continue.
type CharMakeHandler struct {
	chars     domain.CharacterRepository
	charSlots uint8
}

// NewCharMakeHandler builds a CH_MAKE_CHAR handler over the character repository.
// charSlots is the per-account slot ceiling (rAthena's sd->char_slots); a slot
// index >= it is rejected with ErrInvalidSlot (-4).
func NewCharMakeHandler(chars domain.CharacterRepository, charSlots uint8) *CharMakeHandler {
	return &CharMakeHandler{chars: chars, charSlots: charSlots}
}

// Handle implements gateway/domain.PacketHandler for CH_MAKE_CHAR.
func (h *CharMakeHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCHMakeChar(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CH_MAKE_CHAR: %w", err)
	}

	if vErr := validateMakeChar(req, h.charSlots); vErr != nil {
		return h.refuse(conn, vErr)
	}

	auth := conn.Auth()
	created, err := h.chars.Create(ctx, domain.CreateCharacter{
		AccountID: auth.AccountID,
		Name:      req.Name,
		Slot:      req.Slot,
		HairColor: req.HairColor,
		HairStyle: req.HairStyle,
		Job:       req.Job,
		Sex:       req.Sex,
	})
	if err != nil {
		return h.refuse(conn, err)
	}

	resp := packet.AcceptMakeCharResponse{Character: MapCharacterInfo(*created)}
	if err := resp.Encode(connWriter{conn}); err != nil {
		return fmt.Errorf("encode HC_ACCEPT_MAKECHAR: %w", err)
	}
	return nil
}

// refuse encodes an HC_REFUSE_MAKECHAR carrying the byte mapped from err and
// returns nil, leaving the connection open so the client can retry — mirroring
// chclif_createnewchar_refuse. Only an encode failure is surfaced as an error.
func (h *CharMakeHandler) refuse(conn gwdomain.Conn, err error) error {
	resp := packet.RefuseMakeCharResponse{Error: mapRefuseError(err)}
	if encErr := resp.Encode(connWriter{conn}); encErr != nil {
		return fmt.Errorf("encode HC_REFUSE_MAKECHAR: %w", encErr)
	}
	return nil
}

// validateMakeChar runs the pure, client-controlled input checks from
// char_check_char_name (char.cpp:1334-1389) and char_make_new_char's slot/sex
// checks (char.cpp:1418-1434). It returns a domain sentinel the handler maps to
// the matching HC_REFUSE_MAKECHAR byte; name-uniqueness and slot-occupancy are
// DB-level and checked by the repository.
func validateMakeChar(req packet.CHMakeCharRequest, charSlots uint8) error {
	name := strings.TrimSpace(req.Name)
	switch {
	case len(name) == 0:
		return domain.ErrInvalidInput
	case len(name) < charNameMinLength:
		return domain.ErrInvalidInput
	case containsControlChar(name):
		return domain.ErrInvalidInput
	case strings.EqualFold(name, wispServerName):
		// Reserved name: char_check_char_name returns -1 (0x00).
		return domain.ErrCharNameTaken
	case name[0] == '#':
		// Channel symbol: char.cpp:1358 returns -2.
		return domain.ErrInvalidInput
	case req.Slot >= charSlots:
		return domain.ErrInvalidSlot
	case req.Sex != 0 && req.Sex != 1:
		return domain.ErrInvalidInput
	case req.Job != jobNovice:
		return domain.ErrInvalidInput
	}
	return nil
}

// containsControlChar mirrors rAthena's remove_control_chars, which flags any
// byte below 0x20 (char.cpp via common/utils). It reports whether name carries
// a control character; it does not mutate its input (validation, not scrubbing).
func containsControlChar(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 {
			return true
		}
	}
	return false
}

// mapRefuseError maps a domain creation sentinel to the HC_REFUSE_MAKECHAR error
// byte (char_clif.cpp chclif_createnewchar_refuse:1208-1235), which negates the
// char_make_new_char return code and stores it as uint8:
//
//	-1 (name taken/reserved) → 0x00
//	-2 (denied/invalid/slot-in-use) → 0xFF
//	-3 (underaged) → 0x01
//	-4 (invalid slot range) → 0x03
//
// Any unrecognized error is treated as a generic denial (0xFF) so a fault never
// reaches the wire as a misleading success.
func mapRefuseError(err error) uint8 {
	switch {
	case errors.Is(err, domain.ErrCharNameTaken):
		return 0x00
	case errors.Is(err, domain.ErrInvalidSlot):
		return 0x03
	case errors.Is(err, domain.ErrSlotOccupied), errors.Is(err, domain.ErrInvalidInput):
		return 0xFF
	default:
		return 0xFF
	}
}
