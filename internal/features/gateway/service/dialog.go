package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/script/vm"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// DialogSession holds the state of a per-connection NPC dialog. It
// pairs a VM (driven by the compiled script) with the buffer that
// accumulates text between mes() calls and the packet writer that
// receives the rendered dialog packets.
//
// The session is created when a CZ_CONTACTNPC arrives (handled by the
// dispatch layer in u2); the VM is stepped on every dispatch tick and
// pauses via vm.StateStop when it hits mes+next / menu / select. The
// dispatcher resumes it once the client sends CZ_REQNEXTSCRIPT or
// CZ_CHOOSE_MENU.
type DialogSession struct {
	VM      *vm.VM
	Scopes  *vm.ScopeStore
	NPCName string
	NpcID   uint32

	textBuf strings.Builder
	done    bool

	// writer forwards every packet the VM emits to the current
	// Responder. It is swappable because the dispatcher creates the
	// session on CZ_CONTACTNPC (with that packet's Responder) but
	// resumes it on subsequent CZ_REQNEXTSCRIPT / CZ_CHOOSE_MENU
	// packets, each of which delivers a fresh Responder. The
	// adapter is built by NewDialogSessionForResponder so the
	// packetio layer doesn't see a per-call closure allocation; a
	// nil writer means the session was constructed via
	// NewDialogSession(io.Writer) for unit tests.
	writer *responderWriter
}

// NewDialogSession wires a VM to the supplied packet writer using
// dialog-aware builtins (mes / next / close / menu / …) layered on
// top of the default registry. The writer receives ZC_SAY_DIALOG2,
// ZC_WAIT_DIALOG2, ZC_CLOSE_DIALOG, and ZC_MENU_LIST packets as the
// script executes; it is normally a domain.Responder adapter but any
// io.Writer works (tests use *bytes.Buffer).
//
// The optional funcs map, when non-nil, lets the VM resolve callfunc
// names against named function scripts from the compiled script set.
// Pass nil to keep the legacy behavior where callfunc is a stub.
func NewDialogSession(code *script.CompiledScript, funcs map[string]*script.CompiledScript, npcID uint32, npcName string, pktWriter io.Writer) *DialogSession {
	scopes := vm.NewScopeStore()
	session := &DialogSession{
		Scopes:  scopes,
		NPCName: npcName,
		NpcID:   npcID,
	}
	registry := dialogBuiltins(session, npcID, pktWriter)
	session.VM = vm.NewWithFuncs(code, funcs, scopes, registry)
	return session
}

// NewDialogSessionForResponder wires a VM to a swappable
// domain.Responder. Equivalent to NewDialogSession but the
// adapter's underlying Responder can be swapped between packets via
// SetResponder so a single VM keeps writing to whichever connection
// triggered the most recent packet. Builtins still receive an
// io.Writer (the responderWriter), so the dialogBuiltins contract is
// unchanged.
func NewDialogSessionForResponder(code *script.CompiledScript, funcs map[string]*script.CompiledScript, npcID uint32, npcName string, resp domain.Responder) *DialogSession {
	w := &responderWriter{resp: resp}
	session := NewDialogSession(code, funcs, npcID, npcName, w)
	session.writer = w
	return session
}

// SetResponder swaps the underlying Responder used for subsequent
// packet emissions. Callers (the dispatch handlers for
// CZ_REQNEXTSCRIPT / CZ_CHOOSE_MENU) invoke this on each resumed
// packet so the VM continues sending to the live connection rather
// than the Responder captured at contact time. No-op when the
// session was constructed via NewDialogSession (no swappable
// writer) — the original io.Writer stays in effect.
func (s *DialogSession) SetResponder(resp domain.Responder) {
	if s.writer == nil {
		return
	}
	s.writer.setResponder(resp)
}

// IsDone reports whether the dialog has terminated (close/close2/end
// reached). The dispatcher checks this after each VM Run/Resume to
// decide whether to drop the session.
func (s *DialogSession) IsDone() bool { return s.done }

// FlushDialogText writes the buffered mes() text as a ZC_SAY_DIALOG2
// and clears the buffer. Returns the result of Encode (or nil if the
// buffer was empty). Helper exposed so callers can flush on
// abnormal exits.
func (s *DialogSession) FlushDialogText(w io.Writer) error {
	if s.textBuf.Len() == 0 {
		return nil
	}
	if err := flushSayDialog(w, s.NpcID, s.textBuf.String()); err != nil {
		return err
	}
	s.textBuf.Reset()
	return nil
}

// dialogBuiltins returns a BuiltinRegistry whose dialog builtins emit
// real ZC_* packets to pktWriter instead of the stub no-op behavior
// registered by RegisterDefaults. The non-dialog builtins (set, rand,
// gettimetick, warp, …) are inherited from RegisterDefaults unchanged
// so scripts that mix dialog with bookkeeping builtins keep working.
//
// The VM is passed to every builtin as the second argument; builtins
// that pause (next, menu, select) or terminate (close, close2, end)
// flip vm.state accordingly.
func dialogBuiltins(session *DialogSession, npcID uint32, pktWriter io.Writer) *vm.BuiltinRegistry {
	reg := vm.NewBuiltinRegistry()
	reg.RegisterDefaults()

	// mes appends a single line of text to the session buffer. No
	// packet is emitted — the rendered ZC_SAY_DIALOG2 is produced by
	// next()/close() when the script commits the page. The arg
	// filter strips any int 0 left over from a previous builtin
	// call's return value (the VM pushes the result of every
	// builtin onto the stack, including void ones).
	reg.Register("mes", func(_ context.Context, _ *vm.VM, args []vm.Value) (vm.Value, error) {
		text := lastStringArg(args)
		if text == "" {
			return vm.IntValue(0), nil
		}
		if session.textBuf.Len() > 0 {
			session.textBuf.WriteByte('\n')
		}
		session.textBuf.WriteString(text)
		return vm.IntValue(0), nil
	})

	// next flushes the buffered mes() text as ZC_SAY_DIALOG2, emits
	// ZC_WAIT_DIALOG2 so the client shows the "Next" button, and
	// pauses the VM. Resumption happens when the client sends
	// CZ_REQNEXTSCRIPT (handled by the dispatcher in u2).
	reg.Register("next", func(_ context.Context, v *vm.VM, _ []vm.Value) (vm.Value, error) {
		if err := session.FlushDialogText(pktWriter); err != nil {
			return vm.IntValue(0), fmt.Errorf("next: %w", err)
		}
		if err := emitWaitDialog(pktWriter, npcID); err != nil {
			return vm.IntValue(0), fmt.Errorf("next: %w", err)
		}
		v.StateSet(vm.StateStop)
		return vm.IntValue(0), nil
	})

	// close flushes any pending mes() text, emits ZC_CLOSE_DIALOG,
	// marks the session done, and ends the VM.
	reg.Register("close", func(_ context.Context, v *vm.VM, _ []vm.Value) (vm.Value, error) {
		if err := session.FlushDialogText(pktWriter); err != nil {
			return vm.IntValue(0), fmt.Errorf("close: %w", err)
		}
		if err := emitCloseDialog(pktWriter, npcID); err != nil {
			return vm.IntValue(0), fmt.Errorf("close: %w", err)
		}
		session.done = true
		v.StateSet(vm.StateEnd)
		return vm.IntValue(0), nil
	})

	// close2 emits ZC_CLOSE_DIALOG without flushing any pending text
	// and ends the VM (rAthena's close2 semantics: dialog window
	// closes, script terminates).
	reg.Register("close2", func(_ context.Context, v *vm.VM, _ []vm.Value) (vm.Value, error) {
		if err := emitCloseDialog(pktWriter, npcID); err != nil {
			return vm.IntValue(0), fmt.Errorf("close2: %w", err)
		}
		session.done = true
		v.StateSet(vm.StateEnd)
		return vm.IntValue(0), nil
	})

	// end terminates the script without closing the dialog window
	// (the client keeps whatever dialog state it had). The session
	// is marked done so the dispatcher drops it.
	reg.Register("end", func(_ context.Context, v *vm.VM, _ []vm.Value) (vm.Value, error) {
		session.done = true
		v.StateSet(vm.StateEnd)
		return vm.IntValue(0), nil
	})

	// menu emits the prompt strings (every string arg, in order)
	// colon-separated for the client's ZC_MENU_LIST. The script
	// compiler drops the label names as OpName no-ops so only the
	// prompts reach the runtime stack; wire-packet selection
	// routing is the u3 dispatcher's job, not this builtin's.
	reg.Register("menu", func(_ context.Context, v *vm.VM, args []vm.Value) (vm.Value, error) {
		items := allStringArgs(args)
		if err := emitMenuList(pktWriter, npcID, strings.Join(items, ":")); err != nil {
			return vm.IntValue(0), fmt.Errorf("menu: %w", err)
		}
		v.StateSet(vm.StateStop)
		return vm.IntValue(0), nil
	})

	// select is the same wire behavior as menu: prompt strings
	// colon-separated into a single ZC_MENU_LIST. The script
	// compiler emits one stack value per prompt (OpName label
	// references are no-ops at runtime).
	reg.Register("select", func(_ context.Context, v *vm.VM, args []vm.Value) (vm.Value, error) {
		items := allStringArgs(args)
		if err := emitMenuList(pktWriter, npcID, strings.Join(items, ":")); err != nil {
			return vm.IntValue(0), fmt.Errorf("select: %w", err)
		}
		v.StateSet(vm.StateStop)
		return vm.IntValue(0), nil
	})

	return reg
}

// lastStringArg returns the rightmost string value in args, ignoring
// any int values that may have leaked onto the stack from a previous
// builtin call's return value. Returns "" when no string arg is
// present.
func lastStringArg(args []vm.Value) string {
	for _, a := range slices.Backward(args) {
		if a.IsString {
			return a.Str
		}
	}
	return ""
}

// allStringArgs returns every string value in args in order. Builtins
// that fan multiple prompts into a wire packet (menu, select) use
// this to ignore int return values that survived on the stack.
func allStringArgs(args []vm.Value) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a.IsString {
			out = append(out, a.Str)
		}
	}
	return out
}

// flushSayDialog writes a ZC_SAY_DIALOG2 packet with the given text.
func flushSayDialog(w io.Writer, npcID uint32, msg string) error {
	if err := (packet.SayDialog2Response{NpcID: npcID, Type: 0, Message: msg}).Encode(w); err != nil {
		return fmt.Errorf("dialog: flush say: %w", err)
	}
	return nil
}

// emitWaitDialog writes a ZC_WAIT_DIALOG2 packet.
func emitWaitDialog(w io.Writer, npcID uint32) error {
	if err := (packet.WaitDialog2Response{NpcID: npcID, Type: 0}).Encode(w); err != nil {
		return fmt.Errorf("dialog: emit wait: %w", err)
	}
	return nil
}

// emitCloseDialog writes a ZC_CLOSE_DIALOG packet.
func emitCloseDialog(w io.Writer, npcID uint32) error {
	if err := (packet.CloseDialogResponse{NpcID: npcID}).Encode(w); err != nil {
		return fmt.Errorf("dialog: emit close: %w", err)
	}
	return nil
}

// emitMenuList writes a ZC_MENU_LIST packet with colon-separated items.
func emitMenuList(w io.Writer, npcID uint32, items string) error {
	if err := (packet.MenuListResponse{NpcID: npcID, Items: items}).Encode(w); err != nil {
		return fmt.Errorf("dialog: emit menu: %w", err)
	}
	return nil
}

// Compile-time sanity check: bytes.Buffer implements io.Writer so the
// dialog builtins are usable from tests without a real Responder.
var _ io.Writer = (*bytes.Buffer)(nil)

// responderWriter is a swappable io.Writer that forwards each Write
// as one SendPacket call to the currently-set Responder. The dialog
// session holds this adapter for the VM's lifetime; the dispatcher
// swaps the underlying Responder on each resumed packet so dialog
// packets always reach the active connection.
//
// Each Write copies the buffer first because SendPacket (gnet
// AsyncWrite under the hood) may retain the slice past the call
// return; reusing the caller's buffer would race with the network
// goroutine.
type responderWriter struct {
	resp domain.Responder
}

func (w *responderWriter) Write(p []byte) (int, error) {
	if w.resp == nil {
		return 0, fmt.Errorf("dialog: responder not set")
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	if err := w.resp.SendPacket(buf); err != nil {
		return 0, fmt.Errorf("dialog: send packet: %w", err)
	}
	return len(p), nil
}

func (w *responderWriter) setResponder(r domain.Responder) { w.resp = r }
