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
	"github.com/bouroo/goAthena/internal/modules/account/app"
	"github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/account/infra"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// captureConn is a gateway/domain.Conn that records writes, role transitions,
// and auth cache writes.
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

// dispatchLogin feeds a raw CA_LOGIN frame through the real codec + real
// dispatcher to the handler, returning the captured response bytes and the conn.
func dispatchLogin(t *testing.T, h *app.CALoginHandler, req packet.CALoginRequest) (*captureConn, []byte) {
	t.Helper()
	var raw bytes.Buffer
	require.NoError(t, req.Encode(&raw))

	dec := netcodec.NewLoginDecoder(packet.NewLoginServerDB())
	dec.Feed(raw.Bytes())
	cmd, frame, err := dec.Next()
	require.NoError(t, err)
	require.Equal(t, packet.HeaderCALOGIN, cmd)

	disp := gwdomain.NewDispatcher(gwdomain.PacketHandlerTable{packet.HeaderCALOGIN: h.Handle}, nil, nil)
	conn := &captureConn{remote: "127.0.0.1:54321"}
	require.NoError(t, disp.Dispatch(context.Background(), conn, cmd, frame))
	return conn, conn.buf.Bytes()
}

// TestCALoginHandler_Accept is the M1-4 L3 proof: CA_LOGIN → codec → dispatcher
// → handler → AuthService → memory repo/session → AC_ACCEPT_LOGIN. It asserts the
// full accept frame matches the rAthena wire layout (loginclif.cpp:114-128).
func TestCALoginHandler_Accept(t *testing.T) {
	t.Parallel()
	acc := &domain.Account{AccountID: 2000000, UserID: "test", UserPass: "test", Sex: domain.SexMale}
	store := infra.NewMemorySessionStore()
	auth := app.NewAuthService(infra.NewMemoryAccountRepository(acc), store, stubClock{baseTime}, nil)
	h := app.NewCALoginHandler(auth, nil)

	conn, resp := dispatchLogin(t, h, packet.CALoginRequest{
		Version: 20250604, Username: "test", Password: "test",
	})

	require.Len(t, resp, packet.AcceptLoginResponse{}.Size(), "accept frame length")
	assert.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint32(2000000), binary.LittleEndian.Uint32(resp[8:12]), "AID")
	assert.Equal(t, uint8(1), resp[46], "sex byte (M -> 1)")
	assert.Equal(t, uint32(0), binary.LittleEndian.Uint32(resp[16:20]), "last_ip hardcoded 0")
	// lastlogin[26] at [20:46] is zero-filled.
	for i := 20; i < 46; i++ {
		assert.Zero(t, resp[i], "lastlogin slot must be zero-padded")
	}
	// Session persisted, and the connection advanced to the char role.
	sess, err := store.Get(context.Background(), 2000000)
	require.NoError(t, err)
	assert.Equal(t, binary.LittleEndian.Uint32(resp[4:8]), sess.LoginID1)
	assert.Equal(t, binary.LittleEndian.Uint32(resp[12:16]), sess.LoginID2)
	assert.Equal(t, gwdomain.RoleChar, conn.role, "connection must advance to char role on accept")
}

func TestCALoginHandler_Refuse(t *testing.T) {
	t.Parallel()
	acc := &domain.Account{AccountID: 2000000, UserID: "test", UserPass: "test", Sex: domain.SexMale}
	auth := app.NewAuthService(infra.NewMemoryAccountRepository(acc), infra.NewMemorySessionStore(), stubClock{baseTime}, nil)
	h := app.NewCALoginHandler(auth, nil)

	conn, resp := dispatchLogin(t, h, packet.CALoginRequest{
		Version: 20250604, Username: "test", Password: "wrong",
	})

	require.Len(t, resp, packet.RefuseLoginResponse{}.Size(), "refuse frame length")
	assert.Equal(t, packet.HeaderACREFUSELOGIN, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint32(domain.AuthInvalidPassword), binary.LittleEndian.Uint32(resp[2:6]), "refuse code")
	// A refused login keeps the connection on the login role.
	assert.Equal(t, gwdomain.RoleLogin, conn.role)
}

func TestCALoginHandler_FemaleSexByte(t *testing.T) {
	t.Parallel()
	acc := &domain.Account{AccountID: 2000001, UserID: "test2", UserPass: "test2", Sex: domain.SexFemale}
	auth := app.NewAuthService(infra.NewMemoryAccountRepository(acc), infra.NewMemorySessionStore(), stubClock{baseTime}, nil)
	h := app.NewCALoginHandler(auth, nil)

	_, resp := dispatchLogin(t, h, packet.CALoginRequest{
		Version: 20250604, Username: "test2", Password: "test2",
	})
	require.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint8(0), resp[46], "sex byte (F -> 0)")
}

func TestCALoginHandler_ParseError(t *testing.T) {
	t.Parallel()
	auth := app.NewAuthService(infra.NewMemoryAccountRepository(), infra.NewMemorySessionStore(), stubClock{baseTime}, nil)
	h := app.NewCALoginHandler(auth, nil)
	// Right cmd, body too short for a CA_LOGIN (55 bytes expected).
	err := h.Handle(context.Background(), &captureConn{}, gwdomain.Frame{
		Cmd: packet.HeaderCALOGIN, Raw: []byte{0x64, 0x00, 0x00},
	})
	require.Error(t, err)
}
