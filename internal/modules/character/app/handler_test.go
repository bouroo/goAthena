//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/internal/modules/character/app"
	"github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/character/infra"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// captureConn is a gateway/domain.Conn for the char flow: it records writes and
// carries a pre-set auth cache (the char handlers verify against conn.Auth()).
type captureConn struct {
	role   gwdomain.Role
	auth   gwdomain.ConnAuth
	remote string
	buf    bytes.Buffer
}

func (c *captureConn) Role() gwdomain.Role         { return c.role }
func (c *captureConn) SetRole(r gwdomain.Role)     { c.role = r }
func (c *captureConn) Auth() gwdomain.ConnAuth     { return c.auth }
func (c *captureConn) SetAuth(a gwdomain.ConnAuth) { c.auth = a }
func (c *captureConn) RemoteAddr() string          { return c.remote }
func (c *captureConn) Write(p []byte) error        { _, err := c.buf.Write(p); return err }
func (c *captureConn) Close() error                { return nil }

// dispatchChar feeds an encoded char-role request through the real merged
// login+char codec and the char dispatch table, returning the conn and the
// captured response bytes. The codec DB is the same merged set the gateway
// builds, so CH_ENTER / CH_SELECT_CHAR frame correctly on the login stream.
func dispatchChar(t *testing.T, disp *gwdomain.Dispatcher, auth gwdomain.ConnAuth, reqFrame []byte) (*captureConn, []byte) {
	t.Helper()
	db := packet.NewLoginServerDB()
	db.Merge(packet.NewCharServerDB())
	dec := netcodec.NewLoginDecoder(db)
	dec.Feed(reqFrame)
	cmd, frame, err := dec.Next()
	require.NoError(t, err)
	conn := &captureConn{role: gwdomain.RoleChar, auth: auth, remote: "127.0.0.1:54321"}
	require.NoError(t, disp.Dispatch(context.Background(), conn, cmd, frame))
	return conn, conn.buf.Bytes()
}

// validAuth is the auth cache a login-accept for account 2000000 (test, M)
// would have written. CH_ENTER handlers verify the packet's echoed credentials
// against it.
var validAuth = gwdomain.ConnAuth{AccountID: 2000000, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1}

// seedChar is a full domain.Character for account 2000000 slot 0, exercising the
// fields MapCharacterInfo serializes (name, level, GID, map, stats).
func seedChar() domain.Character {
	return domain.Character{
		CharID:    150000,
		AccountID: 2000000,
		Slot:      0,
		Name:      "TestHero",
		Class:     0, // Novice
		BaseLevel: 12,
		JobLevel:  10,
		BaseExp:   500,
		JobExp:    200,
		Zeny:      3000,
		Str:       9, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1,
		HP: 100, MaxHP: 200, SP: 50, MaxSP: 50,
		Hair: 2, HairColor: 1, ClothesColor: 0, Body: 0,
		LastMap: "prontera", LastX: 53, LastY: 111,
		Sex: 1, // M (wire byte)
	}
}

// TestCharEnterHandler_Accept is the M2a L2 proof: CH_ENTER → verify conn auth
// → list chars → HC_ACCEPT_ENTER (0x006b) with one CHARACTER_INFO carrying the
// seed char's GID, name, level, and sex.
func TestCharEnterHandler_Accept(t *testing.T) {
	t.Parallel()
	chars := infra.NewMemoryCharacterRepository(seedChar())
	enter := app.NewCharEnterHandler(chars)
	disp := gwdomain.NewDispatcher(nil, gwdomain.PacketHandlerTable{
		packet.HeaderCHENTER: enter.Handle,
	}, nil)

	var req bytes.Buffer
	require.NoError(t, packet.CHEnterRequest{
		AccountID: validAuth.AccountID,
		LoginID1:  validAuth.LoginID1,
		LoginID2:  validAuth.LoginID2,
		Sex:       validAuth.Sex,
	}.Encode(&req))

	_, resp := dispatchChar(t, disp, validAuth, req.Bytes())

	// Header 0x006b; length 27 + 175*1 = 202.
	assert.Equal(t, packet.HeaderHCACCEPTENTER, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint16(202), binary.LittleEndian.Uint16(resp[2:4]), "packet length")
	// Char-list header defaults: total=15, premiumStart=15, premiumEnd=15.
	assert.Equal(t, uint8(15), resp[4], "total slots")
	assert.Equal(t, uint8(15), resp[5], "premium start")
	assert.Equal(t, uint8(15), resp[6], "premium end")

	// CHARACTER_INFO at offset 27. GID at +0, Level at +92, Name at +108, Sex at +174.
	info := resp[27:]
	assert.Equal(t, uint32(150000), binary.LittleEndian.Uint32(info[0:4]), "GID")
	assert.Equal(t, int16(12), int16(binary.LittleEndian.Uint16(info[92:94])), "base level")
	assert.Equal(t, "TestHero", string(bytes.TrimRight(info[108:108+24], "\x00")), "name")
	assert.Equal(t, uint8(1), info[174], "sex (M -> 1)")
}

// TestCharEnterHandler_NoChars asserts an account with no characters still gets
// a valid HC_ACCEPT_ENTER (zero CHARACTER_INFO entries): the char-select screen
// shows an empty list, which is a normal "make a char" state, not a fault.
func TestCharEnterHandler_NoChars(t *testing.T) {
	t.Parallel()
	enter := app.NewCharEnterHandler(infra.NewMemoryCharacterRepository())
	disp := gwdomain.NewDispatcher(nil, gwdomain.PacketHandlerTable{
		packet.HeaderCHENTER: enter.Handle,
	}, nil)

	var req bytes.Buffer
	require.NoError(t, packet.CHEnterRequest(validAuth).Encode(&req))

	_, resp := dispatchChar(t, disp, validAuth, req.Bytes())

	assert.Equal(t, packet.HeaderHCACCEPTENTER, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint16(27), binary.LittleEndian.Uint16(resp[2:4]), "header-only length with zero chars")
}

// TestCharEnterHandler_CredentialMismatch proves the trust gate: a CH_ENTER
// whose echoed LoginID2 differs from the cached session token earns an
// HC_REFUSE_ENTER, and the connection is NOT used to query the repo (the cache
// is authoritative, so the mismatch cannot impersonate another account).
func TestCharEnterHandler_CredentialMismatch(t *testing.T) {
	t.Parallel()
	chars := infra.NewMemoryCharacterRepository(seedChar())
	enter := app.NewCharEnterHandler(chars)
	disp := gwdomain.NewDispatcher(nil, gwdomain.PacketHandlerTable{
		packet.HeaderCHENTER: enter.Handle,
	}, nil)

	// Packet echoes the right AccountID but a wrong LoginID2.
	var req bytes.Buffer
	require.NoError(t, packet.CHEnterRequest{
		AccountID: validAuth.AccountID,
		LoginID1:  validAuth.LoginID1,
		LoginID2:  0xDEADBEEF,
		Sex:       validAuth.Sex,
	}.Encode(&req))

	_, resp := dispatchChar(t, disp, validAuth, req.Bytes())

	require.Len(t, resp, 3, "HC_REFUSE_ENTER is a 3-byte frame")
	assert.Equal(t, packet.HeaderHCREFUSEENTER, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint8(0), resp[2], "refuse code = rejected from server")
}

// TestCharSelectHandler_Redirect proves the zone redirect: CH_SELECT_CHAR(slot)
// resolves the char from the cached account and emits HC_NOTIFY_ZONESVR (0x0ac5)
// carrying the char's GID, last_map, and the configured zone IP/port.
func TestCharSelectHandler_Redirect(t *testing.T) {
	t.Parallel()
	chars := infra.NewMemoryCharacterRepository(seedChar())
	zone := app.ZoneAddr{IP: 0x0100007F, Port: 5121} // 127.0.0.1:5121
	sel := app.NewCharSelectHandler(chars, zone)
	disp := gwdomain.NewDispatcher(nil, gwdomain.PacketHandlerTable{
		packet.HeaderCHSELECTCHAR: sel.Handle,
	}, nil)

	var req bytes.Buffer
	require.NoError(t, packet.CHSelectCharRequest{Slot: 0}.Encode(&req))

	_, resp := dispatchChar(t, disp, validAuth, req.Bytes())

	require.Len(t, resp, 156, "HC_NOTIFY_ZONESVR is a 156-byte frame")
	assert.Equal(t, packet.HeaderHCNOTIFYZONESVR, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint32(150000), binary.LittleEndian.Uint32(resp[2:6]), "CID = char GID")
	assert.Equal(t, "prontera", string(bytes.TrimRight(resp[6:6+16], "\x00")), "map name")
	assert.Equal(t, uint16(5121), binary.LittleEndian.Uint16(resp[26:28]), "zone port")
}

// TestCharSelectHandler_NotFound proves an empty slot earns HC_REFUSE_ENTER
// (the connection stays open for another pick) rather than an error.
func TestCharSelectHandler_NotFound(t *testing.T) {
	t.Parallel()
	sel := app.NewCharSelectHandler(infra.NewMemoryCharacterRepository(), app.ZoneAddr{})
	disp := gwdomain.NewDispatcher(nil, gwdomain.PacketHandlerTable{
		packet.HeaderCHSELECTCHAR: sel.Handle,
	}, nil)

	var req bytes.Buffer
	require.NoError(t, packet.CHSelectCharRequest{Slot: 5}.Encode(&req))

	_, resp := dispatchChar(t, disp, validAuth, req.Bytes())

	require.Len(t, resp, 3)
	assert.Equal(t, packet.HeaderHCREFUSEENTER, binary.LittleEndian.Uint16(resp[0:2]))
}

// TestMapCharacterInfo asserts the domain→wire field mapping is faithful to
// rAthena's char.cpp builder: option rides in EffectState, clothes_color in
// BodyPalette, hair_color in both HeadPalette and HairColor, and a rename quota
// drives the inverted IsChangedCharName / ChrNameChangeCnt pair.
func TestMapCharacterInfo(t *testing.T) {
	t.Parallel()
	c := seedChar()
	c.Option = 0x10
	c.ClothesColor = 7
	c.Rename = 1

	info := app.MapCharacterInfo(c)

	assert.Equal(t, uint32(150000), info.GID)
	assert.Equal(t, int16(12), info.Level)
	assert.Equal(t, int32(10), info.JobLevel)
	assert.Equal(t, int32(0x10), info.EffectState, "option → effectstate")
	assert.Equal(t, int32(0), info.BodyState, "bodystate hardcoded 0")
	assert.Equal(t, int32(0), info.HealthState, "healthstate hardcoded 0")
	assert.Equal(t, int16(7), info.BodyPalette, "clothes_color → bodypalette")
	assert.Equal(t, int16(1), info.HeadPalette, "hair_color → headpalette")
	assert.Equal(t, uint8(1), info.HairColor, "hair_color → hairColor (u8)")
	assert.Equal(t, int16(0), info.IsChangedCharName, "rename>0 ⇒ 0 (changeable)")
	assert.Equal(t, int32(1), info.ChrNameChangeCnt, "rename>0 ⇒ 1")
	assert.Equal(t, "prontera", info.MapName)
	assert.Equal(t, uint8(1), info.Sex)
}

// TestParseZoneAddr_RoundTrip verifies the IPv4→uint32 convention the
// HC_NOTIFY_ZONESVR encoder expects (inet_addr/htonl: 127.0.0.1 → 0x0100007F).
func TestParseZoneAddr_RoundTrip(t *testing.T) {
	t.Parallel()
	zone, err := app.ParseZoneAddr("127.0.0.1:5121")
	require.NoError(t, err)
	assert.Equal(t, uint32(0x0100007F), zone.IP)
	assert.Equal(t, uint16(5121), zone.Port)
}
