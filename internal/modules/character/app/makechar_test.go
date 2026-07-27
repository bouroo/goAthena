//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/character/app"
	"github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/character/infra"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// makeCharDispatcher wires a CharMakeHandler at CH_MAKE_CHAR over the given repo,
// with slots as the per-account slot ceiling.
func makeCharDispatcher(chars domain.CharacterRepository, slots uint8) *gwdomain.Dispatcher {
	return gwdomain.NewDispatcher(nil, gwdomain.PacketHandlerTable{
		packet.HeaderCHMAKECHAR: app.NewCharMakeHandler(chars, slots).Handle,
	}, nil)
}

// makeCharRoundTrip encodes a CH_MAKE_CHAR request, runs it through the real
// merged login+char codec and the make-char dispatch table under validAuth, and
// returns the captured response bytes.
func makeCharRoundTrip(t *testing.T, disp *gwdomain.Dispatcher, req packet.CHMakeCharRequest) []byte {
	t.Helper()
	var frame bytes.Buffer
	require.NoError(t, req.Encode(&frame), "encode CH_MAKE_CHAR")
	_, resp := dispatchChar(t, disp, validAuth, frame.Bytes())
	return resp
}

// assertRefuse asserts the response is a 3-byte HC_REFUSE_MAKECHAR carrying err.
func assertRefuse(t *testing.T, resp []byte, wantErr uint8) {
	t.Helper()
	require.Len(t, resp, 3, "HC_REFUSE_MAKECHAR length")
	assert.Equal(t, packet.HeaderHCREFUSEMAKECHAR, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, wantErr, resp[2], "refuse error byte")
}

// TestCharMakeHandler_Accept is the M2b L2 proof: a valid CH_MAKE_CHAR creates a
// novice-defaults character and replies HC_ACCEPT_MAKECHAR (0x0b6f, 177B) whose
// CHARACTER_INFO carries the assigned GID, name, and sex. It also verifies the
// char persisted under the conn-auth account — the trust-anchor check that the
// owning AccountID came from conn.Auth(), not any client-controlled field
// (CH_MAKE_CHAR carries no AccountID, so a default/zero account would be a bug).
func TestCharMakeHandler_Accept(t *testing.T) {
	t.Parallel()
	chars := infra.NewMemoryCharacterRepository()
	disp := makeCharDispatcher(chars, 15)

	resp := makeCharRoundTrip(t, disp, packet.CHMakeCharRequest{
		Name: "Hero", Slot: 0, HairColor: 1, HairStyle: 2, Job: 0, Sex: 1,
	})

	require.Len(t, resp, 177, "HC_ACCEPT_MAKECHAR length")
	assert.Equal(t, packet.HeaderHCACCEPTMAKECHAR, binary.LittleEndian.Uint16(resp[0:2]))
	// CHARACTER_INFO starts at offset 2: GID[0:4], Name[108:132], Sex[174].
	assert.Equal(t, uint32(150000), binary.LittleEndian.Uint32(resp[2:6]), "GID = first auto-increment char_id")
	assert.Equal(t, "Hero", string(bytes.TrimRight(resp[110:134], "\x00")), "name")
	assert.Equal(t, uint8(1), resp[176], "sex (M -> 1)")

	// Persistence + novice defaults + trust anchor.
	list, err := chars.ListByAccount(context.Background(), validAuth.AccountID)
	require.NoError(t, err)
	require.Len(t, list, 1, "one char persisted for the auth account")
	c := list[0]
	assert.Equal(t, "Hero", c.Name)
	assert.Equal(t, uint32(150000), c.CharID)
	assert.Equal(t, validAuth.AccountID, c.AccountID, "account sourced from conn.Auth()")
	assert.Equal(t, uint16(1), c.BaseLevel, "novice base_level")
	assert.Equal(t, uint32(40), c.HP, "novice hp")
	assert.Equal(t, uint32(48), c.StatusPoint, "novice status_point")
	assert.Equal(t, "prontera", c.LastMap, "novice start map")
	assert.Equal(t, uint8(2), c.Hair, "hair_style -> hair column")
}

// TestCharMakeHandler_Refuse_Validation covers the pure, stateless input checks
// from char_check_char_name / char_make_new_char. Each case uses a fresh empty
// repo so ordering across cases cannot interfere.
func TestCharMakeHandler_Refuse_Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		req     packet.CHMakeCharRequest
		wantErr uint8
	}{
		// -2 paths → 0xFF (denied/invalid).
		{"short name (< 4)", packet.CHMakeCharRequest{Name: "abc", Slot: 0, Job: 0, Sex: 1}, 0xFF},
		{"control char", packet.CHMakeCharRequest{Name: "Her\x01o", Slot: 0, Job: 0, Sex: 1}, 0xFF},
		{"leading hashtag", packet.CHMakeCharRequest{Name: "#Hero", Slot: 0, Job: 0, Sex: 1}, 0xFF},
		{"bad sex", packet.CHMakeCharRequest{Name: "Hero", Slot: 0, Job: 0, Sex: 2}, 0xFF},
		{"non-novice job", packet.CHMakeCharRequest{Name: "Hero", Slot: 0, Job: 1, Sex: 1}, 0xFF},
		// -1 reserved name → 0x00 (name taken bucket).
		{"reserved Server", packet.CHMakeCharRequest{Name: "Server", Slot: 0, Job: 0, Sex: 1}, 0x00},
		// -4 invalid slot range → 0x03.
		{"invalid slot (>= ceiling)", packet.CHMakeCharRequest{Name: "Hero", Slot: 15, Job: 0, Sex: 1}, 0x03},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			disp := makeCharDispatcher(infra.NewMemoryCharacterRepository(), 15)
			resp := makeCharRoundTrip(t, disp, tc.req)
			assertRefuse(t, resp, tc.wantErr)
		})
	}
}

// TestCharMakeHandler_Refuse_DupName verifies the DB-level name-uniqueness guard:
// a second create with a case-insensitively equal name is refused 0x00 even
// though both names individually pass the pure validation. This exercises the
// repository path, not validateMakeChar.
func TestCharMakeHandler_Refuse_DupName(t *testing.T) {
	t.Parallel()
	chars := infra.NewMemoryCharacterRepository()
	disp := makeCharDispatcher(chars, 15)

	_ = makeCharRoundTrip(t, disp, packet.CHMakeCharRequest{Name: "Hero", Slot: 0, Job: 0, Sex: 1})
	resp := makeCharRoundTrip(t, disp, packet.CHMakeCharRequest{Name: "hero", Slot: 1, Job: 0, Sex: 1})
	assertRefuse(t, resp, 0x00)
}

// TestCharMakeHandler_Refuse_SlotTaken verifies the DB-level slot-occupancy guard:
// a slot already holding a character (here, seeded) is refused 0xFF. Slot 0 is a
// valid index, so this reaches the repository, not the validation slot check.
func TestCharMakeHandler_Refuse_SlotTaken(t *testing.T) {
	t.Parallel()
	chars := infra.NewMemoryCharacterRepository(seedChar()) // slot 0 occupied
	disp := makeCharDispatcher(chars, 15)

	resp := makeCharRoundTrip(t, disp, packet.CHMakeCharRequest{Name: "Newcomer", Slot: 0, Job: 0, Sex: 1})
	assertRefuse(t, resp, 0xFF)
}
