//go:build unit

package app_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
)

func newWorld() *app.WorldService {
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 150001, Account: 2000001,
		Map: "new_1-1", Pos: domain.Position{X: 53, Y: 111},
		Sex: 1, Job: 0, Level: 1, Name: "Hero", HP: 1000, MaxHP: 1000, Speed: 150,
	})
	return app.NewWorldService(repo, slog.Default(), 50)
}

func TestAddEntity_Success(t *testing.T) {
	w := newWorld()
	err := w.AddEntity(domain.Entity{ID: 1, Map: "prontera", Pos: domain.Position{X: 100, Y: 200}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	e, _ := w.Get(1)
	if e.Pos.X != 100 {
		t.Errorf("x = %d", e.Pos.X)
	}
}

func TestAddEntity_Duplicate(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Map: "prontera"})
	err := w.AddEntity(domain.Entity{ID: 1, Map: "prontera"})
	if !errors.Is(err, domain.ErrEntityAlreadyExists) {
		t.Errorf("err = %v, want ErrEntityAlreadyExists", err)
	}
}

func TestRemoveEntity(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Map: "prontera", Pos: domain.Position{X: 50, Y: 50}})
	if err := w.RemoveEntity(1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	_, err := w.Get(1)
	if !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}

func TestMoveEntity(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Map: "prontera", Pos: domain.Position{X: 50, Y: 50}})
	if err := w.MoveEntity(1, domain.Position{X: 60, Y: 60}); err != nil {
		t.Fatalf("move: %v", err)
	}
	e, _ := w.Get(1)
	if e.Pos.X != 60 || e.Pos.Y != 60 {
		t.Errorf("pos = %+v, want {60,60}", e.Pos)
	}
}

func TestQueryVisible_ReturnsNearby(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Map: "prontera", Pos: domain.Position{X: 100, Y: 100}})
	_ = w.AddEntity(domain.Entity{ID: 2, Map: "prontera", Pos: domain.Position{X: 101, Y: 101}})
	visible := w.QueryVisible("prontera", 100, 100)
	if len(visible) < 2 {
		t.Errorf("visible count = %d, want >= 2 (self + neighbor)", len(visible))
	}
}

func TestPlayersNear_ReturnsOnlyNearbyPCs(t *testing.T) {
	w := newWorld()
	// Two nearby PCs (within the 15-cell view range of the anchor).
	_ = w.AddEntity(domain.Entity{ID: 1, Type: domain.EntityTypePC, Map: "prontera", Pos: domain.Position{X: 100, Y: 100}})
	_ = w.AddEntity(domain.Entity{ID: 2, Type: domain.EntityTypePC, Map: "prontera", Pos: domain.Position{X: 101, Y: 101}})
	// A nearby mob and NPC: they own no connection, so they must be excluded.
	_ = w.AddEntity(domain.Entity{ID: 3, Type: domain.EntityTypeMob, Map: "prontera", Pos: domain.Position{X: 102, Y: 102}})
	_ = w.AddEntity(domain.Entity{ID: 4, Type: domain.EntityTypeNPC, Map: "prontera", Pos: domain.Position{X: 100, Y: 101}})
	// A PC far outside the view range: must NOT appear.
	_ = w.AddEntity(domain.Entity{ID: 5, Type: domain.EntityTypePC, Map: "prontera", Pos: domain.Position{X: 200, Y: 200}})

	got := w.PlayersNear("prontera", domain.Position{X: 100, Y: 100})
	want := map[domain.EntityID]bool{1: true, 2: true}
	if len(got) != len(want) {
		t.Fatalf("PlayersNear = %v, want exactly %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("PlayersNear returned %d; mobs/NPCs/far players must be excluded", id)
		}
	}
}

func TestPlayersNear_EmptyMap(t *testing.T) {
	w := newWorld()
	if got := w.PlayersNear("geffen", domain.Position{X: 50, Y: 50}); got != nil {
		t.Errorf("PlayersNear on unknown map = %v, want nil", got)
	}
}

func TestPlayersOnMap_ReturnsAllPCsExcludesOthers(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Type: domain.EntityTypePC, Map: "prontera", Pos: domain.Position{X: 100, Y: 100}})
	_ = w.AddEntity(domain.Entity{ID: 2, Type: domain.EntityTypePC, Map: "prontera", Pos: domain.Position{X: 200, Y: 200}})
	_ = w.AddEntity(domain.Entity{ID: 3, Type: domain.EntityTypeMob, Map: "prontera", Pos: domain.Position{X: 100, Y: 100}})
	// A PC on a different map must not appear.
	_ = w.AddEntity(domain.Entity{ID: 4, Type: domain.EntityTypePC, Map: "izlude", Pos: domain.Position{X: 10, Y: 10}})

	got := w.PlayersOnMap("prontera")
	want := map[domain.EntityID]bool{1: true, 2: true}
	if len(got) != len(want) {
		t.Fatalf("PlayersOnMap = %v, want exactly %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("PlayersOnMap returned %d; mobs and off-map players must be excluded", id)
		}
	}
}

func TestEnterMap_LoadsFromRepo(t *testing.T) {
	w := newWorld()
	e, err := w.EnterMap(context.Background(), 150001)
	if err != nil {
		t.Fatalf("enter map: %v", err)
	}
	if e.Map != "new_1-1" || e.Pos.X != 53 {
		t.Errorf("entity = %+v", e)
	}
	if e.Type != domain.EntityTypePC {
		t.Errorf("type = %d, want PC", e.Type)
	}
	// Entity should now be in the registry.
	got, _ := w.Get(domain.EntityID(150001))
	if got.Name != "Hero" {
		t.Errorf("name = %q", got.Name)
	}
}

func TestEnterMap_NotFound(t *testing.T) {
	w := newWorld()
	_, err := w.EnterMap(context.Background(), 99999)
	if !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}

func TestLeaveMap_RemovesEntity(t *testing.T) {
	w := newWorld()
	_, _ = w.EnterMap(context.Background(), 150001)
	if err := w.LeaveMap(context.Background(), 150001); err != nil {
		t.Fatalf("leave: %v", err)
	}
	_, err := w.Get(domain.EntityID(150001))
	if !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}

func TestStartTick_FiresUpdates(t *testing.T) {
	w := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 1000) // 1ms tick
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var count atomic.Int32
	go w.StartTick(ctx, func(_ context.Context, _ time.Duration) { count.Add(1) })
	time.Sleep(50 * time.Millisecond)
	cancel()
	if count.Load() == 0 {
		t.Error("tick loop never fired update callback")
	}
}

func TestStartTick_IdleWithoutCallback(t *testing.T) {
	w := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 1000) // 1ms tick
	// With no update callback there is no periodic entity-state work to do
	// (spawn/combat are event-driven), so StartTick must return immediately
	// rather than spin the ticker, and Stop must drain idempotently.
	done := make(chan struct{})
	go func() {
		w.StartTick(context.Background(), nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("StartTick with nil callback blocked instead of returning idle")
	}
	w.Stop()
	w.Stop() // idempotent: must not panic on double-close
}

// hpOf/ spOf read a PC entity's current vitals via Get (returns a copy).
func hpOf(t *testing.T, w *app.WorldService, id domain.EntityID) int32 {
	t.Helper()
	e, err := w.Get(id)
	if err != nil {
		t.Fatalf("Get %d: %v", id, err)
	}
	return e.HP
}

func spOf(t *testing.T, w *app.WorldService, id domain.EntityID) int32 {
	t.Helper()
	e, err := w.Get(id)
	if err != nil {
		t.Fatalf("Get %d: %v", id, err)
	}
	return e.SP
}

// newRegenWorld builds an empty world backed by a memory repo, convenient for
// regen tests that seed entities via AddEntity.
func newRegenWorld() *app.WorldService {
	repo := infra.NewMemoryWorldRepository(domain.Entity{ID: 1, Map: "prontera"})
	return app.NewWorldService(repo, slog.Default(), 50)
}

// TestRegenTick_HPFormulaAndClamp verifies the pre-renewal standing HP regen
// (floor(MaxHP/200)+floor(Vit/2)+1 per 6 s), that it only fires once the interval
// elapses, and that it clamps at MaxHP.
func TestRegenTick_HPFormulaAndClamp(t *testing.T) {
	w := newRegenWorld()
	// MaxHP 1000, Vit 0 -> regen = 5 + 0 + 1 = 6 per interval.
	pc := domain.Entity{ID: 150001, Type: domain.EntityTypePC, Map: "prontera", HP: 500, MaxHP: 1000, Vit: 0}
	if err := w.AddEntity(pc); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// Sub-interval: no regen.
	w.RegenTick(5 * time.Second)
	if got := hpOf(t, w, 150001); got != 500 {
		t.Fatalf("after sub-interval HP = %d, want 500", got)
	}
	// Interval boundary crossed (5 s + 1 s = 6 s): regen 6.
	w.RegenTick(1 * time.Second)
	if got := hpOf(t, w, 150001); got != 506 {
		t.Fatalf("after interval HP = %d, want 506", got)
	}

	// Clamp: a near-max PC regens up to MaxHP, never past it.
	if err := w.RemoveEntity(150001); err != nil {
		t.Fatalf("RemoveEntity: %v", err)
	}
	if err := w.AddEntity(domain.Entity{ID: 150001, Type: domain.EntityTypePC, Map: "prontera", HP: 998, MaxHP: 1000, Vit: 0}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}
	w.RegenTick(6 * time.Second) // 998 + 6 = 1004 -> clamped to 1000
	if got := hpOf(t, w, 150001); got != 1000 {
		t.Fatalf("clamped HP = %d, want 1000", got)
	}
}

// TestRegenTick_SkipsDeadAndNonPC verifies dead PCs and non-PC entities never
// regen HP, even after the interval elapses.
func TestRegenTick_SkipsDeadAndNonPC(t *testing.T) {
	w := newRegenWorld()
	_ = w.AddEntity(domain.Entity{ID: 10, Type: domain.EntityTypePC, Map: "prontera", HP: 0, MaxHP: 1000, Vit: 10})   // dead
	_ = w.AddEntity(domain.Entity{ID: 20, Type: domain.EntityTypeMob, Map: "prontera", HP: 50, MaxHP: 1000, Vit: 10}) // mob
	_ = w.AddEntity(domain.Entity{ID: 30, Type: domain.EntityTypeNPC, Map: "prontera", HP: 1, MaxHP: 1000, Vit: 10})  // npc

	w.RegenTick(6 * time.Second)
	if got := hpOf(t, w, 10); got != 0 {
		t.Errorf("dead PC HP = %d, want 0 (no regen)", got)
	}
	if got := hpOf(t, w, 20); got != 50 {
		t.Errorf("mob HP = %d, want 50 (mobs do not regen)", got)
	}
	if got := hpOf(t, w, 30); got != 1 {
		t.Errorf("npc HP = %d, want 1 (npcs do not regen)", got)
	}
}

// TestRegenTick_SP verifies SP regen on its 8 s cadence (floor(MaxSP/100)+
// floor(Int/2)+1), independent of the 6 s HP cadence. A full HP PC isolates the
// SP advance.
func TestRegenTick_SP(t *testing.T) {
	w := newRegenWorld()
	// MaxSP 100, Int 0 -> regen = 1 + 0 + 1 = 2 per 8 s interval.
	_ = w.AddEntity(domain.Entity{ID: 150001, Type: domain.EntityTypePC, Map: "prontera", HP: 1000, MaxHP: 1000, SP: 40, MaxSP: 100, Int: 0})

	// 6 s: HP interval elapses but HP is full; SP not yet due (6 s < 8 s).
	w.RegenTick(6 * time.Second)
	if got := spOf(t, w, 150001); got != 40 {
		t.Fatalf("SP = %d, want 40 (8 s interval not yet elapsed)", got)
	}
	// 2 s more (total 8 s): SP interval elapses -> regen 2.
	w.RegenTick(2 * time.Second)
	if got := spOf(t, w, 150001); got != 42 {
		t.Fatalf("SP = %d, want 42", got)
	}
}

// statChange records one RegenTick -> OnStatChange notification for inspection.
type statChange struct {
	charID uint32
	hp, sp int32
}

// TestRegenTick_OnStatChangeHook verifies OnStatChange is invoked per changed PC
// with the post-regen vitals, is not invoked when nothing is due, and skips
// non-PC/dead entities.
func TestRegenTick_OnStatChangeHook(t *testing.T) {
	w := newRegenWorld()
	_ = w.AddEntity(domain.Entity{ID: 150001, Type: domain.EntityTypePC, Map: "prontera", HP: 500, MaxHP: 1000, Vit: 0, SP: 40, MaxSP: 100, Int: 0})
	_ = w.AddEntity(domain.Entity{ID: 20, Type: domain.EntityTypeMob, Map: "prontera", HP: 50, MaxHP: 1000}) // must not notify

	var got []statChange
	w.OnStatChange = func(charID uint32, hp, sp int32) {
		got = append(got, statChange{charID, hp, sp})
	}

	// 6 s tick: only HP due (SP interval is 8 s). HP 500 -> 506, SP unchanged.
	w.RegenTick(6 * time.Second)
	want := []statChange{{charID: 150001, hp: 506, sp: 40}}
	if !equalNotifs(got, want) {
		t.Fatalf("after 6 s notifications = %+v, want %+v", got, want)
	}

	// Sub-interval: nothing due, no notification.
	got = nil
	w.RegenTick(1 * time.Second) // HP accumulator 1 s, SP 7 s; neither due
	if len(got) != 0 {
		t.Fatalf("sub-interval notifications = %+v, want none", got)
	}
}

// TestRegenTick_SittingHalvesInterval proves sitting halves the regen interval:
// a seated damaged PC regens HP after 3 s, while a standing PC with the same
// damage does not regen until the full 6 s standing interval. MaxHP 1000, Vit 0
// -> 5+0+1 = 6 HP per interval; SP is kept full so only HP advances.
func TestRegenTick_SittingHalvesInterval(t *testing.T) {
	w := newRegenWorld()
	base := domain.Entity{Type: domain.EntityTypePC, Map: "prontera", HP: 500, MaxHP: 1000, Vit: 0, SP: 100, MaxSP: 100}
	sitter := base
	sitter.ID = 150001
	stander := base
	stander.ID = 150002
	if err := w.AddEntity(sitter); err != nil {
		t.Fatalf("AddEntity sitter: %v", err)
	}
	if err := w.AddEntity(stander); err != nil {
		t.Fatalf("AddEntity stander: %v", err)
	}
	if err := w.SetSitting(150001, true); err != nil {
		t.Fatalf("SetSitting: %v", err)
	}

	// 3 s: sitter's halved (3 s) interval elapses -> regen 6; stander's 6 s
	// interval has not elapsed -> no regen.
	w.RegenTick(3 * time.Second)
	if got := hpOf(t, w, 150001); got != 506 {
		t.Fatalf("sitter HP after 3 s = %d, want 506 (sitting halves interval)", got)
	}
	if got := hpOf(t, w, 150002); got != 500 {
		t.Fatalf("stander HP after 3 s = %d, want 500 (standing interval not yet elapsed)", got)
	}

	// 3 s more (6 s total): sitter hits its second interval (-> 512); stander
	// hits its first standing interval (-> 506). Both regen, at different cadences.
	w.RegenTick(3 * time.Second)
	if got := hpOf(t, w, 150001); got != 512 {
		t.Fatalf("sitter HP after 6 s = %d, want 512 (two 3 s intervals)", got)
	}
	if got := hpOf(t, w, 150002); got != 506 {
		t.Fatalf("stander HP after 6 s = %d, want 506 (one 6 s interval)", got)
	}
}

// TestSetSitting verifies the sitting flag is set/cleared and that a sit for an
// entity not in the registry returns ErrEntityNotFound.
func TestSetSitting(t *testing.T) {
	w := newRegenWorld()
	if err := w.AddEntity(domain.Entity{ID: 150001, Type: domain.EntityTypePC, Map: "prontera", HP: 100, MaxHP: 100}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	if err := w.SetSitting(150001, true); err != nil {
		t.Fatalf("SetSitting(true): %v", err)
	}
	if e, _ := w.Get(150001); !e.Sitting {
		t.Error("after SetSitting(true), Sitting = false, want true")
	}

	if err := w.SetSitting(150001, false); err != nil {
		t.Fatalf("SetSitting(false): %v", err)
	}
	if e, _ := w.Get(150001); e.Sitting {
		t.Error("after SetSitting(false), Sitting = true, want false")
	}

	if err := w.SetSitting(999999, true); !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("SetSitting unknown = %v, want ErrEntityNotFound", err)
	}
}

// containsID reports whether ids contains id.
func containsID(ids []domain.EntityID, id domain.EntityID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// TestRespawnPlayer_SavePointAndGridReseat verifies a dead PC respawns at its
// save point with vitals restored to full, and the AOI grid is re-seated (removed
// from the death cell, added at the save cell).
func TestRespawnPlayer_SavePointAndGridReseat(t *testing.T) {
	w := newRegenWorld()
	dead := domain.Entity{
		ID: 150001, Type: domain.EntityTypePC,
		Map: "prontera", Pos: domain.Position{X: 50, Y: 50},
		SaveMap: "prontera", SavePos: domain.Position{X: 100, Y: 100},
		HP: 0, MaxHP: 1000, SP: 0, MaxSP: 500,
	}
	if err := w.AddEntity(dead); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}
	if !containsID(w.QueryVisible("prontera", 50, 50), 150001) {
		t.Fatal("pre-respawn: dead PC not visible at death cell")
	}

	if err := w.RespawnPlayer(150001); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	e, err := w.Get(150001)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e.Map != "prontera" || e.Pos != (domain.Position{X: 100, Y: 100}) {
		t.Errorf("after respawn Map/Pos = %q %+v, want prontera {100 100}", e.Map, e.Pos)
	}
	if e.HP != 1000 {
		t.Errorf("HP = %d, want 1000 (full revive)", e.HP)
	}
	if e.SP != 500 {
		t.Errorf("SP = %d, want 500 (full revive)", e.SP)
	}
	if containsID(w.QueryVisible("prontera", 50, 50), 150001) {
		t.Error("death cell still shows the PC; grid reseat failed")
	}
	if !containsID(w.QueryVisible("prontera", 100, 100), 150001) {
		t.Error("save cell does not show the PC; grid reseat failed")
	}
}

// TestRespawnPlayer_CrossMap verifies a PC that dies on one map respawns on its
// save map: it leaves the death map's player set and joins the save map's set.
func TestRespawnPlayer_CrossMap(t *testing.T) {
	w := newRegenWorld()
	dead := domain.Entity{
		ID: 150001, Type: domain.EntityTypePC,
		Map: "dungeon", Pos: domain.Position{X: 10, Y: 10},
		SaveMap: "prontera", SavePos: domain.Position{X: 5, Y: 5},
		HP: 0, MaxHP: 200, MaxSP: 100,
	}
	if err := w.AddEntity(dead); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	if err := w.RespawnPlayer(150001); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	e, err := w.Get(150001)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e.Map != "prontera" || e.Pos != (domain.Position{X: 5, Y: 5}) {
		t.Errorf("after respawn Map/Pos = %q %+v, want prontera {5 5}", e.Map, e.Pos)
	}
	if containsID(w.PlayersOnMap("dungeon"), 150001) {
		t.Error("PC still on death map after respawn")
	}
	if !containsID(w.PlayersOnMap("prontera"), 150001) {
		t.Error("PC not on save map after respawn")
	}
}

// TestRespawnPlayer_NoSavePointFallsBackInPlace documents the bounded default: a
// char with no seeded save point respawns on its current map in place (vitals
// restored, position unchanged). Every production char loads a save point from
// the char table, so this is a safety net, not the normal path.
func TestRespawnPlayer_NoSavePointFallsBackInPlace(t *testing.T) {
	w := newRegenWorld()
	dead := domain.Entity{
		ID: 150001, Type: domain.EntityTypePC,
		Map: "prontera", Pos: domain.Position{X: 30, Y: 30},
		HP: 0, MaxHP: 100, MaxSP: 50,
	}
	if err := w.AddEntity(dead); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	if err := w.RespawnPlayer(150001); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	e, _ := w.Get(150001)
	if e.Map != "prontera" || e.Pos != (domain.Position{X: 30, Y: 30}) {
		t.Errorf("no-save respawn Map/Pos = %q %+v, want prontera {30 30} (in place)", e.Map, e.Pos)
	}
	if e.HP != 100 {
		t.Errorf("HP = %d, want 100 (full revive)", e.HP)
	}
}

// TestRespawnPlayer_UnknownEntity verifies the not-found sentinel.
func TestRespawnPlayer_UnknownEntity(t *testing.T) {
	w := newRegenWorld()
	if err := w.RespawnPlayer(999999); !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("RespawnPlayer unknown = %v, want ErrEntityNotFound", err)
	}
}

// TestRespawnPlayer_OnStatChangeHook verifies RespawnPlayer collects the revive
// notification under the lock and dispatches OnStatChange off it with the
// restored vitals (so the gateway's HP/SP-bar emit never runs on the world mutex).
func TestRespawnPlayer_OnStatChangeHook(t *testing.T) {
	w := newRegenWorld()
	if err := w.AddEntity(domain.Entity{
		ID: 150001, Type: domain.EntityTypePC, Map: "prontera",
		SaveMap: "prontera", SavePos: domain.Position{X: 1, Y: 1},
		HP: 0, MaxHP: 1000, SP: 0, MaxSP: 500,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}
	var got []statChange
	w.OnStatChange = func(charID uint32, hp, sp int32) {
		got = append(got, statChange{charID, hp, sp})
	}

	if err := w.RespawnPlayer(150001); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	if !equalNotifs(got, []statChange{{charID: 150001, hp: 1000, sp: 500}}) {
		t.Fatalf("OnStatChange = %+v, want {{150001 1000 500}}", got)
	}
}

// TestRespawnPlayer_OnRespawnHook verifies RespawnPlayer fires OnRespawn (off the
// world mutex) with the revived PC's charID, after OnStatChange, so the gateway's
// save-point appear burst runs only once the world state has settled.
func TestRespawnPlayer_OnRespawnHook(t *testing.T) {
	w := newRegenWorld()
	if err := w.AddEntity(domain.Entity{
		ID: 150001, Type: domain.EntityTypePC, Map: "prontera",
		SaveMap: "prontera", SavePos: domain.Position{X: 1, Y: 1},
		HP: 0, MaxHP: 1000, SP: 0, MaxSP: 500,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}
	var seq []string
	w.OnStatChange = func(_ uint32, _, _ int32) { seq = append(seq, "stat") }
	w.OnRespawn = func(charID uint32) {
		if charID != 150001 {
			t.Errorf("OnRespawn charID = %d, want 150001", charID)
		}
		seq = append(seq, "respawn")
	}

	if err := w.RespawnPlayer(150001); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	if len(seq) != 2 || seq[0] != "stat" || seq[1] != "respawn" {
		t.Fatalf("hook order = %v, want [stat respawn] (stat settles before appear)", seq)
	}
}

// TestArmRespawn_FiresAfterDelay verifies ArmRespawn arms a timer that respawns
// the dead PC after the delay. Polled with a generous deadline so it is not
// timing-sensitive.
func TestArmRespawn_FiresAfterDelay(t *testing.T) {
	w := newRegenWorld()
	if err := w.AddEntity(domain.Entity{
		ID: 150001, Type: domain.EntityTypePC, Map: "prontera",
		SaveMap: "prontera", SavePos: domain.Position{X: 1, Y: 1},
		HP: 0, MaxHP: 1000,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	w.ArmRespawn(150001, 10*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hpOf(t, w, 150001) == 1000 {
			return // respawned
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("ArmRespawn timer never fired within deadline; HP still 0")
}

// TestArmRespawn_StopCancels verifies Stop drains an in-flight respawn timer: an
// armed timer with a long delay never fires after Stop.
func TestArmRespawn_StopCancels(t *testing.T) {
	w := newRegenWorld()
	if err := w.AddEntity(domain.Entity{
		ID: 150001, Type: domain.EntityTypePC, Map: "prontera",
		SaveMap: "prontera", SavePos: domain.Position{X: 1, Y: 1},
		HP: 0, MaxHP: 1000,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	w.ArmRespawn(150001, time.Hour)
	w.Stop()
	time.Sleep(50 * time.Millisecond)
	if got := hpOf(t, w, 150001); got != 0 {
		t.Fatalf("HP = %d after Stop, want 0 (timer should have been cancelled)", got)
	}
}

func equalNotifs(a, b []statChange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
