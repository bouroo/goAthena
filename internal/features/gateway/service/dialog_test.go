//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/script/compiler"
	"github.com/bouroo/goAthena/internal/features/script/parser"
	"github.com/bouroo/goAthena/internal/features/script/vm"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// compileScript parses and compiles a script source string through the
// real parser/compiler pipeline. This exercises the same bytecode
// path production dialog scripts use, so dialog tests catch
// instruction-emission regressions (e.g. OpFunc ordering, arg-count
// differences) instead of trusting a hand-rolled CompiledScript.
func compileScript(t *testing.T, src string) *script.CompiledScript {
	t.Helper()
	tokens, err := script.Lex([]byte(src))
	require.NoError(t, err)
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	cs, err := compiler.New().Compile("dialog_test", stmts)
	require.NoError(t, err)
	return cs
}

// parseSayDialog2 reads one ZC_SAY_DIALOG2 frame from buf and returns
// its decoded fields + consumed byte count.
func parseSayDialog2(t *testing.T, buf []byte) (npcID uint32, msg string, consumed int) {
	t.Helper()
	if len(buf) < 9 {
		t.Fatalf("parseSayDialog2: frame too short (%d bytes)", len(buf))
	}
	require.Equal(t, packet.HeaderZCSAYDIALOG2, binary.LittleEndian.Uint16(buf[0:2]),
		"expected ZC_SAY_DIALOG2 cmd")
	plen := int(binary.LittleEndian.Uint16(buf[2:4]))
	require.GreaterOrEqual(t, plen, 9, "ZC_SAY_DIALOG2 length too short")
	npcID = binary.LittleEndian.Uint32(buf[4:8])
	raw := buf[9 : plen-1] // strip trailing NUL
	return npcID, string(raw), plen
}

// parseWaitDialog2 reads one ZC_WAIT_DIALOG2 frame.
func parseWaitDialog2(t *testing.T, buf []byte) (npcID uint32, consumed int) {
	t.Helper()
	require.GreaterOrEqual(t, len(buf), 7, "ZC_WAIT_DIALOG2 frame too short")
	require.Equal(t, packet.HeaderZCWAITDIALOG2, binary.LittleEndian.Uint16(buf[0:2]),
		"expected ZC_WAIT_DIALOG2 cmd")
	return binary.LittleEndian.Uint32(buf[2:6]), 7
}

// parseCloseDialog reads one ZC_CLOSE_DIALOG frame.
func parseCloseDialog(t *testing.T, buf []byte) (npcID uint32, consumed int) {
	t.Helper()
	require.GreaterOrEqual(t, len(buf), 6, "ZC_CLOSE_DIALOG frame too short")
	require.Equal(t, packet.HeaderZCCLOSEDIALOG, binary.LittleEndian.Uint16(buf[0:2]),
		"expected ZC_CLOSE_DIALOG cmd")
	return binary.LittleEndian.Uint32(buf[2:6]), 6
}

// parseMenuList reads one ZC_MENU_LIST frame.
func parseMenuList(t *testing.T, buf []byte) (npcID uint32, items string, consumed int) {
	t.Helper()
	require.GreaterOrEqual(t, len(buf), 8, "ZC_MENU_LIST frame too short")
	require.Equal(t, packet.HeaderZCMENULIST, binary.LittleEndian.Uint16(buf[0:2]),
		"expected ZC_MENU_LIST cmd")
	plen := int(binary.LittleEndian.Uint16(buf[2:4]))
	require.GreaterOrEqual(t, plen, 9, "ZC_MENU_LIST length too short")
	npcID = binary.LittleEndian.Uint32(buf[4:8])
	return npcID, string(buf[8 : plen-1]), plen
}

func TestDialogSession_MesAccumulatesAndNextFlushes(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 0x1234abcd
	buf := &bytes.Buffer{}
	src := compileScript(t, `mes "Hello"; mes "World"; next;`)
	session := NewDialogSession(src, npcID, "Test NPC", buf)

	_, err := session.VM.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, vm.StateStop, session.VM.State(),
		"VM should pause after next()")

	out := buf.Bytes()
	npc, msg, n := parseSayDialog2(t, out)
	assert.Equal(t, npcID, npc)
	assert.Equal(t, "Hello\nWorld", msg)
	gotNpc, n2 := parseWaitDialog2(t, out[n:])
	assert.Equal(t, npcID, gotNpc)
	assert.Equal(t, n+n2, len(out), "no extra bytes after ZC_WAIT_DIALOG2")
	assert.False(t, session.IsDone(), "session not done after next()")
}

func TestDialogSession_CloseFlushesAndEnds(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 0xdeadbeef
	buf := &bytes.Buffer{}
	src := compileScript(t, `mes "Bye"; close;`)
	session := NewDialogSession(src, npcID, "Test NPC", buf)
	_, err := session.VM.Run(context.Background())
	require.NoError(t, err)

	out := buf.Bytes()
	npc, msg, n := parseSayDialog2(t, out)
	assert.Equal(t, npcID, npc)
	assert.Equal(t, "Bye", msg)
	cnpc, n2 := parseCloseDialog(t, out[n:])
	assert.Equal(t, npcID, cnpc)
	assert.Equal(t, n+n2, len(out))
	assert.True(t, session.IsDone())
	assert.Equal(t, vm.StateEnd, session.VM.State())
}

func TestDialogSession_CloseWithoutPendingText(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 7
	buf := &bytes.Buffer{}
	src := compileScript(t, `close;`)
	session := NewDialogSession(src, npcID, "Test NPC", buf)
	_, err := session.VM.Run(context.Background())
	require.NoError(t, err)

	out := buf.Bytes()
	cnpc, n := parseCloseDialog(t, out)
	assert.Equal(t, npcID, cnpc)
	assert.Equal(t, n, len(out), "only ZC_CLOSE_DIALOG should be emitted")
	assert.True(t, session.IsDone())
}

func TestDialogSession_Close2OmitsFlush(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 42
	buf := &bytes.Buffer{}
	src := compileScript(t, `mes "ignored"; close2;`)
	session := NewDialogSession(src, npcID, "Test NPC", buf)
	_, err := session.VM.Run(context.Background())
	require.NoError(t, err)

	out := buf.Bytes()
	cnpc, n := parseCloseDialog(t, out)
	assert.Equal(t, npcID, cnpc)
	assert.Equal(t, n, len(out))
	assert.True(t, session.IsDone())
	assert.Equal(t, vm.StateEnd, session.VM.State())
}

func TestDialogSession_EndMarksDoneWithoutClosePacket(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 99
	buf := &bytes.Buffer{}
	src := compileScript(t, `end;`)
	session := NewDialogSession(src, npcID, "Test NPC", buf)
	_, err := session.VM.Run(context.Background())
	require.NoError(t, err)

	assert.Empty(t, buf.Bytes(), "end() must not emit any packet")
	assert.True(t, session.IsDone())
	assert.Equal(t, vm.StateEnd, session.VM.State())
}

func TestDialogSession_MenuEmitsColonSeparated(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 0xabcd1234
	buf := &bytes.Buffer{}
	src := compileScript(t, `menu "Buy", L_Buy, "Sell", L_Sell, "Cancel", L_Cancel;`)
	session := NewDialogSession(src, npcID, "Shop NPC", buf)
	_, err := session.VM.Run(context.Background())
	require.NoError(t, err)

	out := buf.Bytes()
	npc, items, n := parseMenuList(t, out)
	assert.Equal(t, npcID, npc)
	assert.Equal(t, "Buy:Sell:Cancel", items)
	assert.Equal(t, n, len(out))
	assert.False(t, session.IsDone(), "menu pauses; session not done")
	assert.Equal(t, vm.StateStop, session.VM.State())
}

func TestDialogSession_SelectEmitsColonSeparated(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 1
	buf := &bytes.Buffer{}
	src := compileScript(t, `select "Foo", L_Foo, "Bar", L_Bar, "Baz", L_Baz;`)
	session := NewDialogSession(src, npcID, "Test NPC", buf)
	_, err := session.VM.Run(context.Background())
	require.NoError(t, err)

	out := buf.Bytes()
	_, items, n := parseMenuList(t, out)
	assert.Equal(t, "Foo:Bar:Baz", items)
	assert.Equal(t, n, len(out))
	assert.Equal(t, vm.StateStop, session.VM.State())
}

func TestDialogSession_NonDialogBuiltinsStillWork(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 1
	buf := &bytes.Buffer{}
	src := compileScript(t, `.@x = 7;`)
	session := NewDialogSession(src, npcID, "Test NPC", buf)
	_, err := session.VM.Run(context.Background())
	require.NoError(t, err)
	got, ok := session.Scopes.Get(".@x")
	require.True(t, ok)
	assert.Equal(t, int64(7), got.AsInt())
}

func TestDialogSession_ResumeAfterNext(t *testing.T) {
	t.Parallel()

	const npcID uint32 = 0xfeedbeef
	buf := &bytes.Buffer{}
	src := compileScript(t, `mes "First"; next; mes "Second"; close;`)
	session := NewDialogSession(src, npcID, "Test NPC", buf)

	state, err := session.VM.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, vm.StateStop, state)

	// First run: emitted ZC_SAY_DIALOG2("First") + ZC_WAIT_DIALOG2.
	first := buf.Bytes()
	_, msg, n := parseSayDialog2(t, first)
	assert.Equal(t, "First", msg)
	_, n2 := parseWaitDialog2(t, first[n:])
	require.Equal(t, n+n2, len(first))

	// Resume — should flush "Second" and close.
	state, err = session.VM.Resume(context.Background())
	require.NoError(t, err)
	require.Equal(t, vm.StateEnd, state)

	// Read buffer again — Resume appended SayDialog2("Second") + CloseDialog.
	after := buf.Bytes()
	rest := after[n+n2:]
	_, msg, n3 := parseSayDialog2(t, rest)
	assert.Equal(t, "Second", msg)
	_, n4 := parseCloseDialog(t, rest[n3:])
	assert.Equal(t, n3+n4, len(rest))
	assert.True(t, session.IsDone())
}
