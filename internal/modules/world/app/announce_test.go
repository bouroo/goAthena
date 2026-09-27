//go:build unit

package app_test

import (
	"testing"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
)

// announceCapture records the recipient list one broadcast resolved to.
type announceCapture struct {
	recipients []uint32
	msg        string
	flag       int
}

// newAnnounceWorld builds a world with three PCs and one mob: two PCs on
// prontera (one adjacent to the anchor, one far away) and one on izlude.
func newAnnounceWorld(t *testing.T) (*app.WorldService, *[]announceCapture) {
	t.Helper()
	w := newWorld()
	// Anchor: the NPC (or the dialog's player) the broadcast is sourced from.
	_ = w.AddEntity(domain.Entity{ID: 900001, Type: domain.EntityTypeNPC, Map: "prontera", Pos: domain.Position{X: 100, Y: 100}})
	_ = w.AddEntity(domain.Entity{ID: 150001, Type: domain.EntityTypePC, Map: "prontera", Pos: domain.Position{X: 105, Y: 100}})
	_ = w.AddEntity(domain.Entity{ID: 150002, Type: domain.EntityTypePC, Map: "prontera", Pos: domain.Position{X: 300, Y: 300}, Name: "far"})
	_ = w.AddEntity(domain.Entity{ID: 150003, Type: domain.EntityTypePC, Map: "izlude", Pos: domain.Position{X: 100, Y: 100}})
	_ = w.AddEntity(domain.Entity{ID: 20, Type: domain.EntityTypeMob, Map: "prontera", Pos: domain.Position{X: 101, Y: 100}})

	var got []announceCapture
	w.OnAnnounce = func(recipients []uint32, msg string, flag int) {
		got = append(got, announceCapture{recipients: recipients, msg: msg, flag: flag})
	}
	return w, &got
}

func hasRecipient(rs []uint32, want uint32) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}

// BC_SELF addresses only the anchor when the anchor is a player.
func TestAnnounce_Self(t *testing.T) {
	w, got := newAnnounceWorld(t)
	w.Announce(150001, "only me", 3) // BC_SELF
	if len(*got) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(*got))
	}
	if rs := (*got)[0].recipients; len(rs) != 1 || rs[0] != 150001 {
		t.Errorf("recipients = %v, want [150001]", rs)
	}
}

// BC_MAP reaches every PC on the anchor's map and nothing off it.
func TestAnnounce_Map(t *testing.T) {
	w, got := newAnnounceWorld(t)
	w.Announce(900001, "map wide", 1) // BC_MAP, NPC anchor
	if len(*got) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(*got))
	}
	rs := (*got)[0].recipients
	if len(rs) != 2 || !hasRecipient(rs, 150001) || !hasRecipient(rs, 150002) {
		t.Errorf("recipients = %v, want both prontera PCs", rs)
	}
	if hasRecipient(rs, 150003) {
		t.Error("izlude PC received a BC_MAP broadcast for prontera")
	}
}

// BC_ALL is zone-wide: every PC regardless of map, no mobs.
func TestAnnounce_All(t *testing.T) {
	w, got := newAnnounceWorld(t)
	w.Announce(900001, "everyone", 0) // BC_ALL
	if len(*got) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(*got))
	}
	rs := (*got)[0].recipients
	if len(rs) != 3 {
		t.Errorf("recipients = %v, want 3 PCs", rs)
	}
	if hasRecipient(rs, 20) {
		t.Error("a mob received a broadcast; only PCs own connections")
	}
}

// BC_AREA follows the AOI grid: only the neighbour inside view range hears it.
func TestAnnounce_Area(t *testing.T) {
	w, got := newAnnounceWorld(t)
	w.Announce(900001, "nearby", 2) // BC_AREA
	if len(*got) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(*got))
	}
	rs := (*got)[0].recipients
	if len(rs) != 1 || rs[0] != 150001 {
		t.Errorf("recipients = %v, want just the adjacent PC 150001", rs)
	}
}

// A non-PC anchor cannot source a player-anchored broadcast: BC_SELF resolves
// to nobody, and the hook is never called (rAthena returns early on a null
// source, script.cpp:11984).
func TestAnnounce_NonPlayerAnchorSelf(t *testing.T) {
	w, got := newAnnounceWorld(t)
	w.Announce(900001, "nobody", 3) // BC_SELF with an NPC anchor
	if len(*got) != 0 {
		t.Errorf("broadcasts = %d, want 0", len(*got))
	}
}

// AnnounceMap is map-wide and ignores the target bits of the flag.
func TestAnnounceMap(t *testing.T) {
	w, got := newAnnounceWorld(t)
	w.AnnounceMap("izlude", "izlude only", 3) // BC_SELF bits ignored
	if len(*got) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(*got))
	}
	if rs := (*got)[0].recipients; len(rs) != 1 || rs[0] != 150003 {
		t.Errorf("recipients = %v, want [150003]", rs)
	}
}

// An unknown map delivers to nobody.
func TestAnnounceMap_UnknownMap(t *testing.T) {
	w, got := newAnnounceWorld(t)
	w.AnnounceMap("nowhere", "void", 0)
	if len(*got) != 0 {
		t.Errorf("broadcasts = %d, want 0", len(*got))
	}
}

// HealAbs is a flat restore clamped to the player's maxima, and it fires the
// vitals notification the gateway relays.
func TestHealAbs(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 150001, Type: domain.EntityTypePC, Map: "prontera", HP: 500, MaxHP: 1000, SP: 40, MaxSP: 100})
	var got []statChange
	w.OnStatChange = func(charID uint32, hp, sp int32) { got = append(got, statChange{charID, hp, sp}) }

	if err := w.HealAbs(150001, 100, 200); err != nil {
		t.Fatalf("HealAbs: %v", err)
	}
	e, _ := w.Get(150001)
	if e.HP != 600 || e.SP != 100 { // 500+100, 40+200 clamped to MaxSP 100
		t.Errorf("vitals = %d/%d, want 600/100", e.HP, e.SP)
	}
	if len(got) != 1 || got[0] != (statChange{150001, 600, 100}) {
		t.Errorf("notifications = %+v, want one {150001 600 100}", got)
	}
}

// A heal against an entity that is not on the map reports not-found, which the
// content Host turns into a dropped effect.
func TestHealAbs_NotOnMap(t *testing.T) {
	w := newWorld()
	if err := w.HealAbs(999999, 10, 10); err == nil {
		t.Error("HealAbs on an unknown char = nil, want an error")
	}
}
