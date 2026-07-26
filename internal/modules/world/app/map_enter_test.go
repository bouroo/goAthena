//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	accountdomain "github.com/bouroo/goAthena/internal/modules/account/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// captureConn is a gateway/domain.Conn for the map flow: it records writes and
// starts with an empty auth cache (a fresh map-server reconnect carries no
// in-connection auth — CZ_ENTER is what populates it).
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

// fakeAuthenticator is a recording stand-in for account/app.AuthService. It
// mirrors the real VerifySession contract: ctx cancellation propagates as the
// context error (the gateDrop path), and a missing session or loginID1 mismatch
// both yield ErrSessionNotFound (no enumeration oracle). It is NOT
// constant-time — that property is unit-tested on the real AuthService in
// account/app; here only the handler's gateResult triad is under test.
type fakeAuthenticator struct {
	sessions     map[uint32]*accountdomain.Session
	gotAccountID uint32
	gotLoginID1  uint32
}

func (f *fakeAuthenticator) Login(context.Context, accountdomain.LoginRequest) (accountdomain.LoginResult, error) {
	return accountdomain.LoginResult{}, errors.New("fakeAuthenticator: Login not used by the map-enter gate")
}

func (f *fakeAuthenticator) VerifySession(ctx context.Context, accountID, loginID1 uint32) (*accountdomain.Session, error) {
	f.gotAccountID, f.gotLoginID1 = accountID, loginID1
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sess, ok := f.sessions[accountID]; ok && sess.LoginID1 == loginID1 {
		return sess, nil
	}
	return nil, accountdomain.ErrSessionNotFound
}

// mapDispatcher wires a MapEnterHandler at CZ_ENTER over the map dispatch table.
func mapDispatcher(auth accountdomain.Authenticator) *gwdomain.Dispatcher {
	return gwdomain.NewDispatcher(nil, nil, gwdomain.PacketHandlerTable{
		packet.HeaderCZENTER: app.NewMapEnterHandler(auth, app.DefaultSpawn).Handle,
	})
}

// dispatchMap feeds an encoded CZ_ENTER through the real map codec (the same
// MapServerDB + (0,0,0) keys the gateway's map listener builds) and the map
// dispatch table, returning the conn and captured response bytes.
func dispatchMap(t *testing.T, ctx context.Context, disp *gwdomain.Dispatcher, reqFrame []byte) (*captureConn, []byte) {
	t.Helper()
	dec := netcodec.NewMapDecoder(packet.NewMapServerDB(), 0, 0, 0)
	dec.Feed(reqFrame)
	cmd, frame, err := dec.Next()
	require.NoError(t, err)
	conn := &captureConn{role: gwdomain.RoleMap, remote: "127.0.0.1:5121"}
	require.NoError(t, disp.Dispatch(ctx, conn, cmd, frame))
	return conn, conn.buf.Bytes()
}

// encodeCZEnter builds a CZ_ENTER frame carrying the given echoed credentials.
func encodeCZEnter(t *testing.T, req packet.CZEnterRequest) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, req.Encode(&buf), "encode CZ_ENTER")
	return buf.Bytes()
}

// TestMapEnterHandler_Accept is the M3 gateAllow proof: a CZ_ENTER whose
// AuthCode matches the stored session's LoginID1 verifies, caches the auth on
// the connection sourced from the SESSION (not the client-controlled packet
// fields), and replies ZC_ACCEPT_ENTER (0x02eb) carrying DefaultSpawn.
//
// The Sex byte in the request is deliberately wrong (0 = female) while the
// session is Male — the handler must ignore the packet's Sex and write the
// session's, the same impersonation discipline CH_ENTER applies to AccountID.
func TestMapEnterHandler_Accept(t *testing.T) {
	t.Parallel()
	const aid uint32 = 2000000
	const loginID1 uint32 = 0x11111111
	auth := &fakeAuthenticator{sessions: map[uint32]*accountdomain.Session{
		aid: {AccountID: aid, LoginID1: loginID1, LoginID2: 0x22222222, Sex: accountdomain.SexMale},
	}}
	disp := mapDispatcher(auth)

	conn, resp := dispatchMap(t, context.Background(), disp, encodeCZEnter(t, packet.CZEnterRequest{
		AccountID: aid, CharID: 150000, AuthCode: loginID1, ClientTime: 7, Sex: 0, // Sex 0 is a deliberate lie.
	}))

	// VerifySession received the packet's echoed credentials.
	assert.Equal(t, aid, auth.gotAccountID, "VerifySession called with the packet AccountID")
	assert.Equal(t, loginID1, auth.gotLoginID1, "VerifySession called with the packet AuthCode")

	// Auth is cached from the session, not the packet (impersonation guard).
	cached := conn.Auth()
	assert.Equal(t, aid, cached.AccountID)
	assert.Equal(t, loginID1, cached.LoginID1)
	assert.Equal(t, uint32(0x22222222), cached.LoginID2)
	assert.Equal(t, uint8(1), cached.Sex, "Sex from session (Male→1), not the packet's 0")

	// Full ZC_ACCEPT_ENTER frame for DefaultSpawn, built independently from the
	// kernel encoder so a drift in app.DefaultSpawn is caught.
	var want bytes.Buffer
	require.NoError(t, (packet.MapAcceptEnterResponse{
		StartTime: 0, PosX: 53, PosY: 111, Dir: 0, XSize: 5, YSize: 5,
	}).Encode(&want))
	assert.Equal(t, want.Bytes(), resp, "ZC_ACCEPT_ENTER bytes")
}

// TestMapEnterHandler_Refuse is the M3 gateRefuse proof: an unknown accountID
// (or a loginID1 mismatch — both collapse to ErrSessionNotFound) yields a
// ZC_REFUSE_ENTER (0x0074, 3 bytes, code 0), leaves the auth cache empty, and
// returns no error so the connection stays open for a client retry.
func TestMapEnterHandler_Refuse(t *testing.T) {
	t.Parallel()
	auth := &fakeAuthenticator{sessions: map[uint32]*accountdomain.Session{
		2000000: {AccountID: 2000000, LoginID1: 0x11111111, Sex: accountdomain.SexMale},
	}}
	disp := mapDispatcher(auth)

	// Wrong AuthCode for a known account: real VerifySession would also return
	// ErrSessionNotFound here (no enumeration oracle), so this exercises the
	// same refuse path as a missing session.
	conn, resp := dispatchMap(t, context.Background(), disp, encodeCZEnter(t, packet.CZEnterRequest{
		AccountID: 2000000, CharID: 1, AuthCode: 0xDEADBEEF, ClientTime: 0, Sex: 1,
	}))

	require.Len(t, resp, 3, "ZC_REFUSE_ENTER length")
	assert.Equal(t, packet.HeaderZCREFUSEENTER, binary.LittleEndian.Uint16(resp[0:2]), "expected ZC_REFUSE_ENTER header")
	assert.Equal(t, uint8(0), resp[2], "REFUSE_ENTER_REJECTED code")
	assert.Equal(t, gwdomain.ConnAuth{}, conn.Auth(), "auth cache stays empty on refuse")
}

// TestMapEnterHandler_Drop is the M3 gateDrop proof: when the run context is
// cancelled mid-verify (server shutdown), the handler drops silently — no
// refuse frame, no error — rather than writing to a closing socket.
func TestMapEnterHandler_Drop(t *testing.T) {
	t.Parallel()
	auth := &fakeAuthenticator{sessions: map[uint32]*accountdomain.Session{
		2000000: {AccountID: 2000000, LoginID1: 0x11111111, Sex: accountdomain.SexMale},
	}}
	disp := mapDispatcher(auth)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // shutdown before the verify completes

	conn, resp := dispatchMap(t, ctx, disp, encodeCZEnter(t, packet.CZEnterRequest{
		AccountID: 2000000, CharID: 1, AuthCode: 0x11111111, ClientTime: 0, Sex: 1,
	}))

	assert.Empty(t, resp, "no frame written on shutdown drop")
	assert.Equal(t, gwdomain.ConnAuth{}, conn.Auth(), "auth cache untouched on drop")
}
