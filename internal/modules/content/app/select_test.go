//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contentapp "github.com/bouroo/goAthena/internal/modules/content/app"
	contentdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// menuCaptureConn is a minimal gateway.Conn that records every frame the
// scriptHost encodes into a buffer. It mirrors world/app's captureConn but
// lives here so the content dialog test has no cross-module test dependency.
// The buffer is mutex-guarded because the scriptHost writes on its goroutine
// while the test polls on the main goroutine.
type menuCaptureConn struct {
	mu     sync.Mutex
	role   gwdomain.Role
	auth   gwdomain.ConnAuth
	buf    bytes.Buffer
	closed bool
}

func (c *menuCaptureConn) Role() gwdomain.Role         { return c.role }
func (c *menuCaptureConn) SetRole(r gwdomain.Role)     { c.role = r }
func (c *menuCaptureConn) Auth() gwdomain.ConnAuth     { return c.auth }
func (c *menuCaptureConn) SetAuth(a gwdomain.ConnAuth) { c.auth = a }
func (c *menuCaptureConn) RemoteAddr() string          { return "menu-test" }
func (c *menuCaptureConn) Write(p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.buf.Write(p)
	return err
}
func (c *menuCaptureConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// snapshot returns a copy of the captured frames under the lock so the polling
// test reads a consistent slice while the scriptHost goroutine may be writing.
func (c *menuCaptureConn) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, c.buf.Len())
	copy(out, c.buf.Bytes())
	return out
}

const menuTestAccount uint32 = 1
const menuTestNpcID uint32 = 70000

// encodeCZChooseMenu builds the raw 7-byte CZ_CHOOSE_MENU frame the gateway
// would deliver: {0x00b8.W | npcid.L@2 | choice.B@6}. choice is the raw wire
// byte (1..254 option, 255 cancel).
func encodeCZChooseMenu(npcID uint32, choice byte) []byte {
	frame := make([]byte, 7)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCZCHOOSEMENU)
	binary.LittleEndian.PutUint32(frame[2:6], npcID)
	frame[6] = choice
	return frame
}

// waitForMenuFrame polls the conn buffer until a ZC_MENU_LIST (0x00b7) frame
// appears, returning once it does. This is the synchronization point that lets
// the test know scriptHost.Select has encoded the menu and is blocked on the
// dialog channel — after which a choice can be delivered without racing the
// non-blocking send the handler uses.
func waitForMenuFrame(t *testing.T, conn *menuCaptureConn) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		snap := conn.snapshot()
		if len(snap) >= 8 && binary.LittleEndian.Uint16(snap[0:2]) == packet.HeaderZCMENULIST {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("scriptHost.Select did not emit ZC_MENU_LIST within 2s; buf=% x", conn.snapshot())
		default:
		}
		time.Sleep(time.Millisecond)
	}
}

// TestScriptHost_Select_EmitsMenuListAndResumesWithChoice proves the
// application-layer bridge the select feature adds: scriptHost.Select emits
// ZC_MENU_LIST to the client and blocks on the dialog channel until a choice is
// delivered, then returns the chosen 1-based index. The VM-side builtin that
// calls Host.Select is proven separately in pkg/ro/script/vm_test.go.
func TestScriptHost_Select_EmitsMenuListAndResumesWithChoice(t *testing.T) {
	t.Parallel()
	registry := infra.NewMemoryDialogRegistry()
	ch, err := registry.Open(menuTestAccount)
	require.NoError(t, err)
	conn := &menuCaptureConn{role: gwdomain.RoleMap}
	host := contentapp.NewScriptHost(menuTestNpcID, conn, menuTestAccount, 0, ch, nil)

	type selectResult struct{ choice int }
	done := make(chan selectResult, 1)
	go func() {
		// The option list is the flat colon-split the client numbers; the host
		// joins it back to "Stay:Leave" for the wire.
		done <- selectResult{host.Select([]string{"Stay", "Leave"})}
	}()

	waitForMenuFrame(t, conn)

	// Deliver choice 2 via a blocking send on the same dialog channel the
	// ChooseMenuHandler writes. Blocking-send is race-free here even though
	// the handler's real send is non-blocking: Select is blocked on receive
	// once the menu frame was emitted.
	ch <- contentdomain.DialogSignal{Advance: true, Choice: 2}

	res := <-done
	assert.Equal(t, 2, res.choice, "Select returns the chosen 1-based index")

	// The emitted frame is exactly ZC_MENU_LIST {len.W | npcid.L | "Stay:Leave\0"}.
	buf := conn.snapshot()
	require.GreaterOrEqual(t, len(buf), 8, "a ZC_MENU_LIST frame was emitted")
	assert.Equal(t, packet.HeaderZCMENULIST, binary.LittleEndian.Uint16(buf[0:2]))
	assert.Equal(t, menuTestNpcID, binary.LittleEndian.Uint32(buf[4:8]))
	items := string(bytes.TrimRight(buf[8:], "\x00")) // the option string is NUL-terminated on the wire
	assert.Equal(t, "Stay:Leave", items, "options join with ':' on the wire")

	registry.Close(menuTestAccount)
}

// TestScriptHost_Select_CancelReturnsFF proves a terminated dialog (advance=
// false) surfaces to the script as cancel (255), the value rAthena scripts test
// @menu==255 for.
func TestScriptHost_Select_CancelReturnsFF(t *testing.T) {
	t.Parallel()
	registry := infra.NewMemoryDialogRegistry()
	ch, err := registry.Open(menuTestAccount)
	require.NoError(t, err)
	conn := &menuCaptureConn{role: gwdomain.RoleMap}
	host := contentapp.NewScriptHost(menuTestNpcID, conn, menuTestAccount, 0, ch, nil)

	done := make(chan int, 1)
	go func() { done <- host.Select([]string{"A", "B"}) }()
	waitForMenuFrame(t, conn)

	// CloseDialogHandler sends Advance=false.
	ch <- contentdomain.DialogSignal{Advance: false}

	assert.Equal(t, 255, <-done, "a closed dialog resolves as cancel (255)")
	registry.Close(menuTestAccount)
}

// TestChooseMenuHandler_ForwardsChoice proves CZ_CHOOSE_MENU parses the choice
// byte and forwards it as a DialogSignal on the dialog channel. The handler's
// send is non-blocking (a timed-out dialog drops the choice), so a blocking
// reader is started before the call.
func TestChooseMenuHandler_ForwardsChoice(t *testing.T) {
	t.Parallel()
	registry := infra.NewMemoryDialogRegistry()
	ch, err := registry.Open(menuTestAccount)
	require.NoError(t, err)
	defer registry.Close(menuTestAccount)

	conn := &menuCaptureConn{role: gwdomain.RoleMap}
	conn.SetAuth(gwdomain.ConnAuth{AccountID: menuTestAccount})

	// DialogService only needs the registry for the menu handler (Handle reads
	// dialogRegistry.Get); world/scripts/shopNPCs are unused and may be nil.
	dlg := contentapp.NewDialogService(nil, registry, nil, nil)
	handler := contentapp.NewChooseMenuHandler(dlg)

	got := make(chan contentdomain.DialogSignal, 1)
	go func() { got <- <-ch }() // blocking reader must be blocked before the send
	// The handler's send is non-blocking (it drops when no receiver is ready,
	// matching a timed-out dialog), so let the reader goroutine reach its
	// blocked receive before invoking Handle. This is a wait-for-scheduling,
	// not a wait-for-data: there is no concurrent data access to race.
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, handler.Handle(context.Background(), conn, gwdomain.Frame{Raw: encodeCZChooseMenu(menuTestNpcID, 3)}))

	select {
	case sig := <-got:
		assert.True(t, sig.Advance, "a choice advances the dialog")
		assert.Equal(t, byte(3), sig.Choice, "the chosen option byte is forwarded")
	case <-time.After(time.Second):
		t.Fatal("ChooseMenuHandler did not deliver a DialogSignal")
	}
}
