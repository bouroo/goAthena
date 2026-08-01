//go:build unit

package app

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// nameConn is a transport-free Conn that captures Write output and carries a
// fixed auth, so SendNameReply can be exercised without a real socket.
type nameConn struct {
	buf  bytes.Buffer
	auth gwdomain.ConnAuth
}

func (c *nameConn) Role() gwdomain.Role         { return gwdomain.RoleMap }
func (c *nameConn) SetRole(gwdomain.Role)       {}
func (c *nameConn) Auth() gwdomain.ConnAuth     { return c.auth }
func (c *nameConn) SetAuth(a gwdomain.ConnAuth) { c.auth = a }
func (c *nameConn) RemoteAddr() string          { return "test" }
func (c *nameConn) Write(p []byte) error        { _, err := c.buf.Write(p); return err }
func (c *nameConn) Close() error                { return nil }

// nameTestWorld builds a SpawnService over a requester + an in-view target PC,
// mob, and NPC so the name-resolution paths can be exercised. Positions are all
// within nameViewRadius (15) of the requester at (50,50) on "prontera".
func nameTestWorld(t *testing.T) (*SpawnService, uint32, uint32, uint32, uint32) {
	t.Helper()
	players := domain.NewPlayerRegistry()
	// requester: accountID=1, charID=100.
	require.NoError(t, players.Register(&domain.Player{AccountID: 1, CharID: 100, MapName: "prontera", PosX: 50, PosY: 50, Name: "Hero"}))
	// target PC: accountID=2, charID=200, 2 cells away (in view).
	require.NoError(t, players.Register(&domain.Player{AccountID: 2, CharID: 200, MapName: "prontera", PosX: 52, PosY: 50, Name: "Sidekick"}))

	mobs := domain.NewMobRegistry()
	mobEID := mobs.NextEntityID()
	require.NoError(t, mobs.Register(&domain.Mob{EntityID: mobEID, MapName: "prontera", PosX: 51, PosY: 51, Name: "Poring"}))

	npcs := domain.NewNPCRegistry()
	npcEID := npcs.NextEntityID()
	require.NoError(t, npcs.Register(&domain.NPC{EntityID: npcEID, MapName: "prontera", PosX: 53, PosY: 50, Name: "Kafra"}))

	svc := NewSpawnService(nil, nil, players, mobs, npcs, statcalc.PreRenewalSet, nil, nil)
	return svc, 200, uint32(mobEID), uint32(npcEID), 100 // targetCharID, mobEID, npcEID, requesterCharID
}

// TestSendNameReply_PC proves a requested PC GID (charID) yields the 0x0a30
// REQNAMEALL2 reply carrying the target's name, with empty affiliation fields.
func TestSendNameReply_PC(t *testing.T) {
	t.Parallel()
	svc, targetCharID, _, _, _ := nameTestWorld(t)
	conn := &nameConn{}
	require.NoError(t, svc.SendNameReply(context.Background(), conn, 1, targetCharID))

	got := conn.buf.Bytes()
	require.Len(t, got, (packet.ReqNameAll2Response{}).Size()) // 106
	if h := leU16(got[0:2]); h != uint16(packet.HeaderZCACKREQNAMEALL2) {
		t.Fatalf("header = 0x%04x, want 0x0a30", h)
	}
	if s := string(bytes.TrimRight(got[6:6+24], "\x00")); s != "Sidekick" {
		t.Errorf("PC name = %q, want %q", s, "Sidekick")
	}
}

// TestSendNameReply_MobAndNPC proves a requested mob/NPC GID (EntityID) yields
// the 0x0adf REQNAMEALL_NPC reply carrying the entity's name.
func TestSendNameReply_MobAndNPC(t *testing.T) {
	t.Parallel()
	svc, _, mobEID, npcEID, _ := nameTestWorld(t)

	for _, tc := range []struct {
		name string
		gid  uint32
		want string
	}{
		{"mob", mobEID, "Poring"},
		{"npc", npcEID, "Kafra"},
	} {
		conn := &nameConn{}
		require.NoError(t, svc.SendNameReply(context.Background(), conn, 1, tc.gid))
		got := conn.buf.Bytes()
		if h := leU16(got[0:2]); h != uint16(packet.HeaderZCACKREQNAMEALLNPC) {
			t.Fatalf("%s: header = 0x%04x, want 0x0adf", tc.name, h)
		}
		if s := string(bytes.TrimRight(got[10:10+24], "\x00")); s != tc.want {
			t.Errorf("%s: name = %q, want %q", tc.name, s, tc.want)
		}
	}
}

// TestSendNameReply_NoOp covers the silent cases rAthena shares: an unknown GID,
// a target out of view, a target on a different map, and a requester that is not
// online all write nothing.
func TestSendNameReply_NoOp(t *testing.T) {
	t.Parallel()
	svc, targetCharID, mobEID, _, _ := nameTestWorld(t)

	// unknown GID
	c := &nameConn{}
	require.NoError(t, svc.SendNameReply(context.Background(), c, 1, 999999))
	assert.Empty(t, c.buf.Bytes(), "unknown GID: no reply")

	// requester not online
	c = &nameConn{}
	require.NoError(t, svc.SendNameReply(context.Background(), c, 777, targetCharID))
	assert.Empty(t, c.buf.Bytes(), "no requester: no reply")

	// out of view: temporarily move the requester far away via a second world
	// where the target is beyond radius. (Build a far target PC.)
	players := domain.NewPlayerRegistry()
	require.NoError(t, players.Register(&domain.Player{AccountID: 1, CharID: 100, MapName: "prontera", PosX: 50, PosY: 50, Name: "Hero"}))
	require.NoError(t, players.Register(&domain.Player{AccountID: 2, CharID: 300, MapName: "prontera", PosX: 500, PosY: 500, Name: "Far"}))
	farSvc := NewSpawnService(nil, nil, players, domain.NewMobRegistry(), domain.NewNPCRegistry(), statcalc.PreRenewalSet, nil, nil)
	c = &nameConn{}
	require.NoError(t, farSvc.SendNameReply(context.Background(), c, 1, 300))
	assert.Empty(t, c.buf.Bytes(), "out-of-view PC: no reply")

	// different map: requester on prontera, mob (mobEID) reused here is on prontera,
	// so build a mob on another map and request it.
	_ = mobEID
	players2 := domain.NewPlayerRegistry()
	require.NoError(t, players2.Register(&domain.Player{AccountID: 1, CharID: 100, MapName: "prontera", PosX: 50, PosY: 50, Name: "Hero"}))
	mobs2 := domain.NewMobRegistry()
	otherEID := mobs2.NextEntityID()
	require.NoError(t, mobs2.Register(&domain.Mob{EntityID: otherEID, MapName: "geffen", PosX: 50, PosY: 50, Name: "Goblin"}))
	otherSvc := NewSpawnService(nil, nil, players2, mobs2, domain.NewNPCRegistry(), statcalc.PreRenewalSet, nil, nil)
	c = &nameConn{}
	require.NoError(t, otherSvc.SendNameReply(context.Background(), c, 1, uint32(otherEID)))
	assert.Empty(t, c.buf.Bytes(), "different-map mob: no reply")
}

// leU16 reads a little-endian uint16 (avoids a binary import in the test).
func leU16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
