package service

import (
	"strings"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// parseMapFromSubject strips the "zone.event." subject prefix and
// returns the trailing map-name token. Returns ("", false) when the
// subject does not start with the prefix or the remainder is empty —
// the caller must treat both cases as "not a map event" and skip the
// fan-out.
//
// Dots inside the remainder are preserved (only the leading prefix is
// stripped); this is intentional so map names that legitimately contain
// dots still round-trip cleanly. rAthena map names do not contain
// dots, but the gateway must not assume the format is single-token —
// the contract is "everything after the prefix is the map name".
//
//nolint:unused // pure encoder — wired into the fan-out dispatcher in a later slice
func parseMapFromSubject(subject string) (mapName string, ok bool) {
	prefix := natsinfra.SubjectZoneEventPrefix + "."
	if !strings.HasPrefix(subject, prefix) {
		return "", false
	}
	mapName = strings.TrimPrefix(subject, prefix)
	if mapName == "" {
		return "", false
	}
	return mapName, true
}

// unitWalkingFromEvent builds the ZC_UNIT_WALKING (0x09fd) observer
// broadcast from the mover's cached ViewData and the movement event.
// The view supplies every character-derived appearance field; the
// event supplies the source/destination cells and the move-start tick.
//
// Fields with no source on either view or event (BodyState,
// HealthState, EffectState, HeadDir, GUID, GEmblemVer, Honor, Virtue,
// IsPKModeON, Font, IsBoss, Body) are left at their zero value —
// matching the self-spawn zero-fill contract documented on
// domain.ViewData. XSize/YSize are hardcoded to 5/5 (PC collision
// size, matching the self-spawn).
//
//nolint:unused // pure encoder — wired into the fan-out dispatcher in a later slice
func unitWalkingFromEvent(view domain.ViewData, e *zonev1.EntityMoved) packet.UnitWalkingResponse {
	// Guard against nil event — recurse with a zero event so the
	// caller can call this unconditionally from the fan-out dispatcher.
	if e == nil {
		e = &zonev1.EntityMoved{}
	}
	return packet.UnitWalkingResponse{
		ObjectType:    view.ObjectType,
		AID:           view.AID,
		GID:           view.GID,
		Speed:         view.Speed,
		Job:           view.Job,
		Head:          view.Head,
		Weapon:        view.Weapon,
		Shield:        view.Shield,
		Accessory:     view.Accessory,
		MoveStartTime: uint32(e.GetMoveStartTime()), //nolint:gosec // move_start_time fits uint32 wire slot (ms epoch low bits)
		Accessory2:    view.Accessory2,
		Accessory3:    view.Accessory3,
		HeadPalette:   view.HeadPalette,
		BodyPalette:   view.BodyPalette,
		Robe:          view.Robe,
		Sex:           view.Sex,
		SrcX:          int16(e.GetSrcX()),  //nolint:gosec // map coords fit int16 wire slot
		SrcY:          int16(e.GetSrcY()),  //nolint:gosec // map coords fit int16 wire slot
		DestX:         int16(e.GetDestX()), //nolint:gosec // map coords fit int16 wire slot
		DestY:         int16(e.GetDestY()), //nolint:gosec // map coords fit int16 wire slot
		XSize:         5,
		YSize:         5,
		CLevel:        view.CLevel,
		MaxHP:         view.MaxHP,
		HP:            view.HP,
		Name:          view.Name,
	}
}

// spawnFromView builds the ZC_SPAWN_UNIT (0x09fe) on-enter spawn
// packet from the entity's cached ViewData and the spawn cell
// coordinates. The view supplies every character-derived appearance
// field; the caller supplies the cell coordinates (the spawn source
// is not part of the view — it depends on the spawn reason).
//
// Fields with no source on the view (BodyState, HealthState,
// EffectState, HeadDir, GUID, GEmblemVer, Honor, Virtue, IsPKModeON,
// Font, IsBoss, Body) are left at their zero value — matching the
// self-spawn zero-fill contract documented on domain.ViewData.
// XSize/YSize are hardcoded to 5/5 (PC collision size).
//
//nolint:unused // pure encoder — wired into the fan-out dispatcher in a later slice
func spawnFromView(view domain.ViewData, posX, posY int16) packet.SpawnUnitResponse {
	return packet.SpawnUnitResponse{
		ObjectType:  view.ObjectType,
		AID:         view.AID,
		GID:         view.GID,
		Speed:       view.Speed,
		Job:         view.Job,
		Head:        view.Head,
		Weapon:      view.Weapon,
		Shield:      view.Shield,
		Accessory:   view.Accessory,
		Accessory2:  view.Accessory2,
		Accessory3:  view.Accessory3,
		HeadPalette: view.HeadPalette,
		BodyPalette: view.BodyPalette,
		Robe:        view.Robe,
		Sex:         view.Sex,
		PosX:        posX,
		PosY:        posY,
		Dir:         0,
		XSize:       5,
		YSize:       5,
		CLevel:      view.CLevel,
		MaxHP:       view.MaxHP,
		HP:          view.HP,
		Name:        view.Name,
	}
}

// vanishFromEvent builds the ZC_NOTIFY_VANISH (0x0080) broadcast
// from an EntityVanished event. The wire slot carries only the
// vanishing entity's GID and the vanish reason (OUT_OF_SIGHT=0,
// LOGOUT=1, TELEPORT=2 — rAthena's clr_type enum, mapped verbatim).
// Unknown type values fold to OUT_OF_SIGHT (0) so a malformed event
// cannot crash the fan-out.
//
//nolint:unused // pure encoder — wired into the fan-out dispatcher in a later slice
func vanishFromEvent(e *zonev1.EntityVanished) packet.NotifyVanishResponse {
	if e == nil {
		e = &zonev1.EntityVanished{}
	}
	return packet.NotifyVanishResponse{
		GID:  e.GetEntityId(),
		Type: vanishType(e.GetType()),
	}
}

// vanishType folds a ZoneEvent vanish type into the on-wire uint8
// (0=OUT_OF_SIGHT, 1=LOGOUT, 2=TELEPORT; unknown → 0). The default
// arm absorbs unknown values so a malformed event cannot propagate a
// garbage reason byte to observer clients.
//
//nolint:unused // pure encoder — wired into the fan-out dispatcher in a later slice
func vanishType(t uint32) uint8 {
	switch t {
	case 1, 2:
		return uint8(t) //nolint:gosec // vanish type fits uint8 wire slot
	default:
		return 0
	}
}
