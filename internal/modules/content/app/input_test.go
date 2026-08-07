//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
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

// waitForOpenEditDlg polls the conn buffer until a ZC_OPEN_EDITDLG / STR frame
// (6 bytes: cmd + NpcID) appears — the synchronization point that proves
// scriptHost.Input/InputStr has encoded the window and is blocked on the dialog
// channel, after which a value can be delivered without racing the handler's
// non-blocking send.
func waitForOpenEditDlg(t *testing.T, conn *menuCaptureConn, wantCmd uint16) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		snap := conn.snapshot()
		if len(snap) >= 6 && binary.LittleEndian.Uint16(snap[0:2]) == wantCmd {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("scriptHost did not emit 0x%04x within 2s; buf=% x", wantCmd, conn.snapshot())
		default:
		}
		time.Sleep(time.Millisecond)
	}
}

// TestScriptHost_Input_EmitsOpenEditDlgAndResumesWithAmount proves the numeric-
// input bridge: scriptHost.Input emits ZC_OPEN_EDITDLG and blocks on the dialog
// channel until an amount is delivered, then returns it. The VM-side builtin
// that calls Host.Input is proven separately in pkg/ro/script/vm_test.go.
func TestScriptHost_Input_EmitsOpenEditDlgAndResumesWithAmount(t *testing.T) {
	t.Parallel()
	registry := infra.NewMemoryDialogRegistry()
	ch, err := registry.Open(menuTestAccount)
	require.NoError(t, err)
	conn := &menuCaptureConn{role: gwdomain.RoleMap}
	host := contentapp.NewScriptHost(menuTestNpcID, conn, menuTestAccount, 0, ch, nil)

	type inputResult struct {
		amount int64
		ok     bool
	}
	done := make(chan inputResult, 1)
	go func() { a, ok := host.Input(); done <- inputResult{a, ok} }()

	waitForOpenEditDlg(t, conn, packet.HeaderZCOPENEDITDLG)

	// Deliver the numeric value the InputEditDlgHandler would forward.
	ch <- contentdomain.DialogSignal{Advance: true, Amount: 4242}

	res := <-done
	assert.True(t, res.ok, "an advancing signal returns ok=true")
	assert.Equal(t, int64(4242), res.amount, "Input returns the submitted amount")

	// The emitted frame is exactly ZC_OPEN_EDITDLG {cmd.W | npcid.L}.
	buf := conn.snapshot()
	require.GreaterOrEqual(t, len(buf), 6, "a ZC_OPEN_EDITDLG frame was emitted")
	assert.Equal(t, packet.HeaderZCOPENEDITDLG, binary.LittleEndian.Uint16(buf[0:2]))
	assert.Equal(t, menuTestNpcID, binary.LittleEndian.Uint32(buf[2:6]))

	registry.Close(menuTestAccount)
}

// TestScriptHost_InputStr_EmitsOpenEditDlgStrAndResumesWithText proves the
// string-input bridge: scriptHost.InputStr emits ZC_OPEN_EDITDLGSTR and blocks
// until text is delivered, then returns it.
func TestScriptHost_InputStr_EmitsOpenEditDlgStrAndResumesWithText(t *testing.T) {
	t.Parallel()
	registry := infra.NewMemoryDialogRegistry()
	ch, err := registry.Open(menuTestAccount)
	require.NoError(t, err)
	conn := &menuCaptureConn{role: gwdomain.RoleMap}
	host := contentapp.NewScriptHost(menuTestNpcID, conn, menuTestAccount, 0, ch, nil)

	type inputStrResult struct {
		text string
		ok   bool
	}
	done := make(chan inputStrResult, 1)
	go func() { s, ok := host.InputStr(); done <- inputStrResult{s, ok} }()

	waitForOpenEditDlg(t, conn, packet.HeaderZCOPENEDITDLGSTR)

	ch <- contentdomain.DialogSignal{Advance: true, Text: "Kafra"}

	res := <-done
	assert.True(t, res.ok, "an advancing signal returns ok=true")
	assert.Equal(t, "Kafra", res.text, "InputStr returns the submitted text")

	buf := conn.snapshot()
	require.GreaterOrEqual(t, len(buf), 6, "a ZC_OPEN_EDITDLGSTR frame was emitted")
	assert.Equal(t, packet.HeaderZCOPENEDITDLGSTR, binary.LittleEndian.Uint16(buf[0:2]))
	assert.Equal(t, menuTestNpcID, binary.LittleEndian.Uint32(buf[2:6]))

	registry.Close(menuTestAccount)
}

// TestScriptHost_Input_CancelReturnsFalse proves a terminated dialog (advance=
// false) surfaces as ok=false so builtinInput ends the script.
func TestScriptHost_Input_CancelReturnsFalse(t *testing.T) {
	t.Parallel()
	registry := infra.NewMemoryDialogRegistry()
	ch, err := registry.Open(menuTestAccount)
	require.NoError(t, err)
	conn := &menuCaptureConn{role: gwdomain.RoleMap}
	host := contentapp.NewScriptHost(menuTestNpcID, conn, menuTestAccount, 0, ch, nil)

	done := make(chan bool, 1)
	go func() { _, ok := host.Input(); done <- ok }()
	waitForOpenEditDlg(t, conn, packet.HeaderZCOPENEDITDLG)

	// CloseDialogHandler sends Advance=false.
	ch <- contentdomain.DialogSignal{Advance: false}

	assert.False(t, <-done, "a closed dialog resolves as ok=false")
	registry.Close(menuTestAccount)
}

// TestInputEditDlgHandler_ForwardsAmount proves CZ_INPUT_EDITDLG parses the
// numeric value and forwards it as a DialogSignal (Amount) on the dialog channel.
func TestInputEditDlgHandler_ForwardsAmount(t *testing.T) {
	t.Parallel()
	registry := infra.NewMemoryDialogRegistry()
	ch, err := registry.Open(menuTestAccount)
	require.NoError(t, err)
	defer registry.Close(menuTestAccount)

	conn := &menuCaptureConn{role: gwdomain.RoleMap}
	conn.SetAuth(gwdomain.ConnAuth{AccountID: menuTestAccount})
	dlg := contentapp.NewDialogService(nil, registry, nil, nil)
	handler := contentapp.NewInputEditDlgHandler(dlg)

	var frame bytes.Buffer
	require.NoError(t, (packet.CZInputEditDlgRequest{NpcID: menuTestNpcID, Value: 123456}).Encode(&frame))

	got := make(chan contentdomain.DialogSignal, 1)
	go func() { got <- <-ch }()
	time.Sleep(50 * time.Millisecond) // let the blocking reader reach its receive before the non-blocking send

	require.NoError(t, handler.Handle(context.Background(), conn, gwdomain.Frame{Raw: frame.Bytes()}))

	select {
	case sig := <-got:
		assert.True(t, sig.Advance, "a value advances the dialog")
		assert.Equal(t, int32(123456), sig.Amount, "the submitted amount is forwarded")
	case <-time.After(time.Second):
		t.Fatal("InputEditDlgHandler did not deliver a DialogSignal")
	}
}

// TestInputEditDlgStrHandler_ForwardsText proves CZ_INPUT_EDITDLGSTR parses the
// text and forwards it as a DialogSignal (Text) on the dialog channel.
func TestInputEditDlgStrHandler_ForwardsText(t *testing.T) {
	t.Parallel()
	registry := infra.NewMemoryDialogRegistry()
	ch, err := registry.Open(menuTestAccount)
	require.NoError(t, err)
	defer registry.Close(menuTestAccount)

	conn := &menuCaptureConn{role: gwdomain.RoleMap}
	conn.SetAuth(gwdomain.ConnAuth{AccountID: menuTestAccount})
	dlg := contentapp.NewDialogService(nil, registry, nil, nil)
	handler := contentapp.NewInputEditDlgStrHandler(dlg)

	var frame bytes.Buffer
	require.NoError(t, (packet.CZInputEditDlgStrRequest{NpcID: menuTestNpcID, Value: "Prontera"}).Encode(&frame))

	got := make(chan contentdomain.DialogSignal, 1)
	go func() { got <- <-ch }()
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, handler.Handle(context.Background(), conn, gwdomain.Frame{Raw: frame.Bytes()}))

	select {
	case sig := <-got:
		assert.True(t, sig.Advance, "a value advances the dialog")
		assert.Equal(t, "Prontera", sig.Text, "the submitted text is forwarded")
	case <-time.After(time.Second):
		t.Fatal("InputEditDlgStrHandler did not deliver a DialogSignal")
	}
}
