//go:build unit

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
)

// TestPlayerRegistry_RegisterAndLookup asserts the primary by-account index
// and the per-map inverted index agree after a register, and that ByAccount
// returns the same pointer (not a copy) so broadcast fan-out shares state.
func TestPlayerRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()
	r := domain.NewPlayerRegistry()

	p := &domain.Player{AccountID: 1001, CharID: 150000, MapName: "prontera"}
	require.NoError(t, r.Register(p))

	got, ok := r.ByAccount(1001)
	require.True(t, ok)
	assert.Same(t, p, got)

	onMap := r.OnMap("prontera")
	require.Len(t, onMap, 1)
	assert.Same(t, p, onMap[0])
}

// TestPlayerRegistry_RejectsDuplicateAccount asserts the one-PC-per-account
// invariant: a second register for the same account fails AND does not shadow
// the first session (CharID stays the first enter's value).
func TestPlayerRegistry_RejectsDuplicateAccount(t *testing.T) {
	t.Parallel()
	r := domain.NewPlayerRegistry()

	require.NoError(t, r.Register(&domain.Player{AccountID: 1001, CharID: 150000, MapName: "prontera"}))
	err := r.Register(&domain.Player{AccountID: 1001, CharID: 150001, MapName: "prontera"})
	assert.ErrorIs(t, err, domain.ErrPlayerAlreadyRegistered)

	onMap := r.OnMap("prontera")
	require.Len(t, onMap, 1)
	assert.EqualValues(t, 150000, onMap[0].CharID) // first session wins
}

// TestPlayerRegistry_UnregisterIdempotent asserts unregister returns the
// removed player and is a no-op (not a panic) on a missing or already-removed
// account — disconnect cleanup can race with a failed enter.
func TestPlayerRegistry_UnregisterIdempotent(t *testing.T) {
	t.Parallel()
	r := domain.NewPlayerRegistry()

	p := &domain.Player{AccountID: 1001, MapName: "prontera"}
	require.NoError(t, r.Register(p))

	removed := r.Unregister(1001)
	assert.Same(t, p, removed)
	_, ok := r.ByAccount(1001)
	assert.False(t, ok)
	assert.Empty(t, r.OnMap("prontera"))

	assert.Nil(t, r.Unregister(1001)) // second unregister: no-op
	assert.Nil(t, r.Unregister(9999)) // unknown account: no-op
}

// TestPlayerRegistry_OnMapIsDefensiveSnapshot asserts the returned slice is a
// copy — fan-out code can reorder/drop entries without corrupting the
// registry — and that an unknown map yields an empty, non-nil slice.
func TestPlayerRegistry_OnMapIsDefensiveSnapshot(t *testing.T) {
	t.Parallel()
	r := domain.NewPlayerRegistry()

	require.NoError(t, r.Register(&domain.Player{AccountID: 1001, MapName: "prontera"}))
	require.NoError(t, r.Register(&domain.Player{AccountID: 1002, MapName: "prontera"}))

	snap := r.OnMap("prontera")
	require.Len(t, snap, 2)
	snap[0], snap[1] = snap[1], snap[0] // mutate the snapshot
	require.Len(t, r.OnMap("prontera"), 2)

	empty := r.OnMap("nowhere")
	assert.Empty(t, empty)
	assert.NotNil(t, empty)
}

// TestPlayerRegistry_UnregisterCleansEmptyMapIndex asserts the empty map entry
// is deleted on the last unregister so a later re-enter on the same map does
// not find a stale (empty) set lingering in the inverted index.
func TestPlayerRegistry_UnregisterCleansEmptyMapIndex(t *testing.T) {
	t.Parallel()
	r := domain.NewPlayerRegistry()

	require.NoError(t, r.Register(&domain.Player{AccountID: 1001, MapName: "prontera"}))
	r.Unregister(1001)
	assert.Empty(t, r.OnMap("prontera"))
}

// TestPlayerRegistry_Relocate asserts a warp re-keys the per-map inverted index
// and updates MapName while keeping the player online (by-account unchanged) —
// the invariants scriptWorld.Warp relies on for a cross-map transit. The old
// bucket must drop the player (and clean its empty entry) and the new bucket
// must hold it; ByAccount still resolves the same pointer. A relocate of an
// unknown account is a no-op returning false, not a panic.
func TestPlayerRegistry_Relocate(t *testing.T) {
	t.Parallel()
	r := domain.NewPlayerRegistry()

	p := &domain.Player{AccountID: 1001, CharID: 150000, MapName: "prontera"}
	require.NoError(t, r.Register(p))

	ok := r.Relocate(1001, "izlude")
	assert.True(t, ok, "relocate a live player succeeds")

	// Stays online under the same pointer; map name adopted.
	got, found := r.ByAccount(1001)
	require.True(t, found)
	assert.Same(t, p, got)
	assert.Equal(t, "izlude", p.MapName, "MapName updated to the destination")

	// Per-map index re-keyed: gone from the old map, present on the new one.
	assert.Empty(t, r.OnMap("prontera"), "player left the source map bucket")
	onIzlude := r.OnMap("izlude")
	require.Len(t, onIzlude, 1)
	assert.Same(t, p, onIzlude[0])

	// A relocate to a third map re-keys again (old destination bucket cleaned).
	require.True(t, r.Relocate(1001, "geffen"))
	assert.Empty(t, r.OnMap("izlude"))
	require.Len(t, r.OnMap("geffen"), 1)

	// Unknown account is a no-op, not a panic.
	assert.False(t, r.Relocate(9999, "izlude"), "relocate an unregistered account is a no-op")
}

// TestPlayerRegistry_RegisterNilRejects asserts the nil guard; a nil player
// would otherwise nil-deref the AOI/broadcast paths.
func TestPlayerRegistry_RegisterNilRejects(t *testing.T) {
	t.Parallel()
	r := domain.NewPlayerRegistry()
	assert.Error(t, r.Register(nil))
}

func TestPlayer_HealClampsHPAndSP(t *testing.T) {
	t.Parallel()
	p := &domain.Player{HP: 90, MaxHP: 100, SP: 5, MaxSP: 10}
	p.Heal(25, 8)
	assert.Equal(t, uint32(100), p.HP)
	assert.Equal(t, uint32(10), p.SP)

	p.Heal(-150, -20)
	assert.Zero(t, p.HP)
	assert.Zero(t, p.SP)
}

func TestPlayer_HealLeavesZeroMaximumUnchanged(t *testing.T) {
	t.Parallel()
	p := &domain.Player{HP: 7, SP: 3}
	p.Heal(10, 10)
	assert.Equal(t, uint32(7), p.HP)
	assert.Equal(t, uint32(3), p.SP)
}

// convention the spawnunit-aid-vs-gid memory documents: AID = account_id,
// GID = char_id. The kernel doc comment saying "AID equals GID" is wrong for
// this client; this test pins the correct mapping at the world layer so a
// future kernel doc "fix" cannot silently swap it back.
func TestPlayer_SpawnUnit_AIDGID(t *testing.T) {
	t.Parallel()
	p := &domain.Player{AccountID: 1001, CharID: 150000}
	su := p.SpawnUnit()
	assert.EqualValues(t, 1001, su.AID, "AID must be account_id")
	assert.EqualValues(t, 150000, su.GID, "GID must be char_id")
	assert.NotEqual(t, su.AID, su.GID, "AID != GID for a real account/char pair")
}

// TestPlayer_SpawnUnit_PCDefaults asserts the PC-specific spawn defaults:
// ObjectType=0, speed=150, xSize=ySize=5, MaxHP=HP=-1 (no HP bar for a PC),
// body=0 (novice), accessory slots map head_bottom/top/mid in rAthena's
// non-guild-flag order.
func TestPlayer_SpawnUnit_PCDefaults(t *testing.T) {
	t.Parallel()
	p := &domain.Player{
		AccountID: 1001, CharID: 150000,
		Job: 0, Head: 1, HeadPalette: 7, BodyPalette: 0,
		HeadBottom: 10, HeadMid: 20, HeadTop: 30,
		Weapon: 0, Shield: 0, Robe: 0,
		Manner: 5, Karma: 0, Option: 0, Sex: 1,
		Name: "Tester", CLevel: 1, MaxHP: 40, HP: 40,
	}
	su := p.SpawnUnit()

	assert.EqualValues(t, 0, su.ObjectType, "PC ObjectType")
	assert.EqualValues(t, domain.PCWalkSpeed, su.Speed, "default PC walk speed")
	assert.EqualValues(t, 5, su.XSize, "PC xSize")
	assert.EqualValues(t, 5, su.YSize, "PC ySize")
	assert.EqualValues(t, -1, su.MaxHP, "PC hides HP bar in spawn")
	assert.EqualValues(t, -1, su.HP, "PC hides HP bar in spawn")
	assert.EqualValues(t, 0, su.Body, "novice LOOK_BODY2")
	assert.EqualValues(t, 1, su.Head, "hair → Head")
	assert.EqualValues(t, 7, su.HeadPalette, "hair_color → HeadPalette")
	assert.EqualValues(t, 10, su.Accessory, "head_bottom → Accessory")
	assert.EqualValues(t, 30, su.Accessory2, "head_top → Accessory2")
	assert.EqualValues(t, 20, su.Accessory3, "head_mid → Accessory3")
	assert.EqualValues(t, 5, su.Honor, "manner → Honor")
	assert.EqualValues(t, 0, su.Virtue, "opt3 = 0 with no status-change")
	assert.EqualValues(t, 0, su.IsPKModeON, "karma=0 → PK mode off")
	assert.EqualValues(t, 1, su.Sex, "sex byte passthrough")
	assert.Equal(t, "Tester", su.Name)
}

// TestPlayer_SpawnUnit_PKModeFromKarma asserts isPKModeON is 1 iff karma is
// non-zero (clif.cpp:1304).
func TestPlayer_SpawnUnit_PKModeFromKarma(t *testing.T) {
	t.Parallel()
	off := (&domain.Player{Karma: 0}).SpawnUnit()
	assert.EqualValues(t, 0, off.IsPKModeON)
	on := (&domain.Player{Karma: 1}).SpawnUnit()
	assert.EqualValues(t, 1, on.IsPKModeON)
}

// TestPlayer_SpawnUnit_OptionToEffectState asserts the option bitmask is
// carried through to EffectState (rAthena's sc->option). gosec G115 notes the
// uint32→int32 conversion is intentional (rAthena stores option in an int32).
func TestPlayer_SpawnUnit_OptionToEffectState(t *testing.T) {
	t.Parallel()
	p := &domain.Player{Option: 0x10} // sitting bit, for example
	su := p.SpawnUnit()
	assert.EqualValues(t, 0x10, su.EffectState)
}

// fakeConn is a minimal gwdomain.Conn for future SpawnService tests: it
// records written bytes so assertions can inspect the exact spawn frames
// broadcast to each player. Defined here so the world/app SpawnService test
// (task #21) can reuse it via the same package boundary.
type fakeConn struct {
	role    gwdomain.Role
	auth    gwdomain.ConnAuth
	written []byte
	addr    string
	closed  bool
}

func (c *fakeConn) Role() gwdomain.Role         { return c.role }
func (c *fakeConn) SetRole(r gwdomain.Role)     { c.role = r }
func (c *fakeConn) Auth() gwdomain.ConnAuth     { return c.auth }
func (c *fakeConn) SetAuth(a gwdomain.ConnAuth) { c.auth = a }
func (c *fakeConn) RemoteAddr() string          { return c.addr }
func (c *fakeConn) Write(p []byte) error        { c.written = append(c.written, p...); return nil }
func (c *fakeConn) Close() error                { c.closed = true; return nil }
