//go:build unit

package service

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// fullViewData returns a ViewData populated with non-zero values for
// every character-derived field. Tests that want to assert a per-field
// mapping build on this baseline so a regression in any single field
// shows up as a precise diff.
func fullViewData() domain.ViewData {
	return domain.ViewData{
		ObjectType:  0, // PC
		AID:         4242,
		GID:         9001,
		Speed:       150,
		Job:         7, // swordsman
		Head:        5,
		Weapon:      1101,
		Shield:      2202,
		Accessory:   5505, // head_bottom
		Accessory2:  3303, // head_top
		Accessory3:  4404, // head_mid
		HeadPalette: 3,
		BodyPalette: 1,
		Robe:        6606,
		Sex:         1, // male
		CLevel:      50,
		MaxHP:       2000,
		HP:          1234,
		Name:        "alpha",
	}
}

func TestParseMapFromSubject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		subject string
		wantMap string
		wantOK  bool
	}{
		{
			name:    "valid single-token map",
			subject: "zone.event.prontera",
			wantMap: "prontera",
			wantOK:  true,
		},
		{
			name:    "prefix only, no map",
			subject: "zone.event.",
			wantMap: "",
			wantOK:  false,
		},
		{
			name:    "wrong prefix",
			subject: "other.thing",
			wantMap: "",
			wantOK:  false,
		},
		{
			name:    "empty subject",
			subject: "",
			wantMap: "",
			wantOK:  false,
		},
		{
			name:    "dotted map name preserved",
			subject: "zone.event.prontera.glast",
			wantMap: "prontera.glast",
			wantOK:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotMap, gotOK := parseMapFromSubject(tc.subject)
			assert.Equal(t, tc.wantMap, gotMap, "mapName")
			assert.Equal(t, tc.wantOK, gotOK, "ok")
		})
	}
}

func TestUnitWalkingFromEvent(t *testing.T) {
	t.Parallel()

	view := fullViewData()
	evt := &zonev1.EntityMoved{
		EntityId:      9001,
		SrcX:          150,
		SrcY:          200,
		DestX:         155,
		DestY:         205,
		MoveStartTime: 1_000_000_000, // fits in uint32 wire slot; smaller value avoids constant-overflow in test
	}

	t.Run("populated view and event maps every field", func(t *testing.T) {
		t.Parallel()
		resp := unitWalkingFromEvent(view, evt)

		assert.Equal(t, view.ObjectType, resp.ObjectType, "ObjectType")
		assert.Equal(t, view.AID, resp.AID, "AID")
		assert.Equal(t, view.GID, resp.GID, "GID")
		assert.Equal(t, view.Speed, resp.Speed, "Speed")
		assert.Equal(t, view.Job, resp.Job, "Job")
		assert.Equal(t, view.Head, resp.Head, "Head")
		assert.Equal(t, view.Weapon, resp.Weapon, "Weapon")
		assert.Equal(t, view.Shield, resp.Shield, "Shield")
		assert.Equal(t, view.Accessory, resp.Accessory, "Accessory")
		assert.Equal(t, view.Accessory2, resp.Accessory2, "Accessory2")
		assert.Equal(t, view.Accessory3, resp.Accessory3, "Accessory3")
		assert.Equal(t, view.HeadPalette, resp.HeadPalette, "HeadPalette")
		assert.Equal(t, view.BodyPalette, resp.BodyPalette, "BodyPalette")
		assert.Equal(t, view.Robe, resp.Robe, "Robe")
		assert.Equal(t, view.Sex, resp.Sex, "Sex")
		assert.Equal(t, view.CLevel, resp.CLevel, "CLevel")
		assert.Equal(t, view.MaxHP, resp.MaxHP, "MaxHP")
		assert.Equal(t, view.HP, resp.HP, "HP")
		assert.Equal(t, view.Name, resp.Name, "Name")

		assert.Equal(t, int16(150), resp.SrcX, "SrcX from event")
		assert.Equal(t, int16(200), resp.SrcY, "SrcY from event")
		assert.Equal(t, int16(155), resp.DestX, "DestX from event")
		assert.Equal(t, int16(205), resp.DestY, "DestY from event")
		assert.Equal(t, uint32(1_000_000_000), resp.MoveStartTime, "MoveStartTime passthrough for uint32-fitting value")

		assert.Equal(t, uint8(5), resp.XSize, "XSize hardcoded to 5")
		assert.Equal(t, uint8(5), resp.YSize, "YSize hardcoded to 5")

		// Zero-fill contract — no source on view or event.
		assert.Equal(t, int16(0), resp.BodyState, "BodyState zero-fill")
		assert.Equal(t, int16(0), resp.HealthState, "HealthState zero-fill")
		assert.Equal(t, int32(0), resp.EffectState, "EffectState zero-fill")
		assert.Equal(t, int16(0), resp.HeadDir, "HeadDir zero-fill")
		assert.Equal(t, uint32(0), resp.GUID, "GUID zero-fill")
		assert.Equal(t, int16(0), resp.GEmblemVer, "GEmblemVer zero-fill")
		assert.Equal(t, int16(0), resp.Honor, "Honor zero-fill")
		assert.Equal(t, int32(0), resp.Virtue, "Virtue zero-fill")
		assert.Equal(t, uint8(0), resp.IsPKModeON, "IsPKModeON zero-fill")
		assert.Equal(t, int16(0), resp.Font, "Font zero-fill")
		assert.Equal(t, uint8(0), resp.IsBoss, "IsBoss zero-fill")
		assert.Equal(t, int16(0), resp.Body, "Body zero-fill")
	})

	t.Run("MoveStartTime truncates uint64 to uint32 low bits", func(t *testing.T) {
		t.Parallel()
		big := &zonev1.EntityMoved{
			EntityId:      9001,
			SrcX:          0,
			SrcY:          0,
			DestX:         0,
			DestY:         0,
			MoveStartTime: 1_700_000_000_000, // exceeds uint32 max; tests truncation
		}
		resp := unitWalkingFromEvent(view, big)
		//nolint:gosec // mirror the wire-side uint64→uint32 truncation at runtime
		want := uint32(uint64(1_700_000_000_000) & 0xFFFFFFFF)
		assert.Equal(t, want, resp.MoveStartTime, "MoveStartTime low bits of uint64")
	})

	t.Run("Size is 114 and Encode matches", func(t *testing.T) {
		t.Parallel()
		resp := unitWalkingFromEvent(view, evt)
		assert.Equal(t, 114, resp.Size(), "Size() must match the wire shape")

		var buf bytes.Buffer
		require.NoError(t, resp.Encode(&buf), "Encode must not fail")
		assert.Equal(t, resp.Size(), buf.Len(), "encoded length must equal Size()")
	})

	t.Run("nil event is safe and yields zero coords", func(t *testing.T) {
		t.Parallel()
		resp := unitWalkingFromEvent(view, nil)

		assert.Equal(t, int16(0), resp.SrcX, "nil-event SrcX")
		assert.Equal(t, int16(0), resp.SrcY, "nil-event SrcY")
		assert.Equal(t, int16(0), resp.DestX, "nil-event DestX")
		assert.Equal(t, int16(0), resp.DestY, "nil-event DestY")
		assert.Equal(t, uint32(0), resp.MoveStartTime, "nil-event MoveStartTime")

		// View-derived fields must still be populated.
		assert.Equal(t, view.GID, resp.GID, "nil-event must not blank view fields")
		assert.Equal(t, view.Name, resp.Name, "nil-event must not blank view Name")
	})
}

func TestSpawnFromView(t *testing.T) {
	t.Parallel()

	view := fullViewData()

	t.Run("populated view and coords map every field", func(t *testing.T) {
		t.Parallel()
		resp := spawnFromView(view, 175, 225)

		assert.Equal(t, view.ObjectType, resp.ObjectType, "ObjectType")
		assert.Equal(t, view.AID, resp.AID, "AID")
		assert.Equal(t, view.GID, resp.GID, "GID")
		assert.Equal(t, view.Speed, resp.Speed, "Speed")
		assert.Equal(t, view.Job, resp.Job, "Job")
		assert.Equal(t, view.Head, resp.Head, "Head")
		assert.Equal(t, view.Weapon, resp.Weapon, "Weapon")
		assert.Equal(t, view.Shield, resp.Shield, "Shield")
		assert.Equal(t, view.Accessory, resp.Accessory, "Accessory")
		assert.Equal(t, view.Accessory2, resp.Accessory2, "Accessory2")
		assert.Equal(t, view.Accessory3, resp.Accessory3, "Accessory3")
		assert.Equal(t, view.HeadPalette, resp.HeadPalette, "HeadPalette")
		assert.Equal(t, view.BodyPalette, resp.BodyPalette, "BodyPalette")
		assert.Equal(t, view.Robe, resp.Robe, "Robe")
		assert.Equal(t, view.Sex, resp.Sex, "Sex")
		assert.Equal(t, view.CLevel, resp.CLevel, "CLevel")
		assert.Equal(t, view.MaxHP, resp.MaxHP, "MaxHP")
		assert.Equal(t, view.HP, resp.HP, "HP")
		assert.Equal(t, view.Name, resp.Name, "Name")

		assert.Equal(t, int16(175), resp.PosX, "PosX from caller")
		assert.Equal(t, int16(225), resp.PosY, "PosY from caller")
		assert.Equal(t, uint8(0), resp.Dir, "Dir hardcoded to 0")
		assert.Equal(t, uint8(5), resp.XSize, "XSize hardcoded to 5")
		assert.Equal(t, uint8(5), resp.YSize, "YSize hardcoded to 5")
	})

	t.Run("Size is 107 and Encode matches", func(t *testing.T) {
		t.Parallel()
		resp := spawnFromView(view, 175, 225)
		assert.Equal(t, 107, resp.Size(), "Size() must match the wire shape")

		var buf bytes.Buffer
		require.NoError(t, resp.Encode(&buf), "Encode must not fail")
		assert.Equal(t, resp.Size(), buf.Len(), "encoded length must equal Size()")
	})
}

func TestVanishFromEvent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		inType   uint32
		wantType uint8
	}{
		{name: "OUT_OF_SIGHT zero", inType: 0, wantType: 0},
		{name: "LOGOUT one", inType: 1, wantType: 1},
		{name: "TELEPORT two", inType: 2, wantType: 2},
		{name: "unknown folds to zero", inType: 99, wantType: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			evt := &zonev1.EntityVanished{
				EntityId: 9001,
				Type:     tc.inType,
			}
			resp := vanishFromEvent(evt)
			assert.Equal(t, uint32(9001), resp.GID, "GID echoes entity_id")
			assert.Equal(t, tc.wantType, resp.Type, "Type mapping")
		})
	}

	t.Run("nil event does not panic", func(t *testing.T) {
		t.Parallel()
		var resp packet.NotifyVanishResponse
		assert.NotPanics(t, func() {
			resp = vanishFromEvent(nil)
		}, "vanishFromEvent(nil) must not panic")
		assert.Equal(t, uint32(0), resp.GID, "nil-event GID is zero")
		assert.Equal(t, uint8(0), resp.Type, "nil-event Type is zero")
	})
}
