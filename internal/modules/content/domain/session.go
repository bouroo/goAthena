// Package domain holds the content bounded context: the script dialog session
// model and the ports the VM-bridge needs. No infrastructure imports.
package domain

import (
	"context"
)

// DialogSignal is the value sent over a dialog session's channel when the client
// responds to a blocking Host call (Next / Select / Input). The VM goroutine,
// blocked in script.Host.Next/Select, receives this and resumes.
type DialogSignal struct {
	// Advance is true when the client clicked Next/OK (CZ_REQ_NEXT_SCRIPT).
	Advance bool
	// Choice is the 1-based menu selection (CZ_CHOOSE_MENU); 255 = cancel.
	Choice uint8
	// Input is the numeric or string value from CZ_INPUT_EDITDLG(_STR).
	Input string
	// Cancel is true when the client closed the dialog (CZ_CLOSE_DIALOG).
	Cancel bool
}

// PacketWriter sends raw packet bytes to the player's connection. gnet's
// AsyncWrite satisfies this; isolating it keeps the content module free of gnet.
type PacketWriter interface {
	WritePacket(data []byte)
}

// DialogSession is one player's active NPC dialog: the writer to send dialog
// packets to the client, and the channel the VM goroutine blocks on while
// waiting for the client's response.
type DialogSession struct {
	NpcID  uint32
	Writer PacketWriter
	Signal chan DialogSignal
}

// NPCStore resolves an NPC entity GID to its script name (so a click can find
// which script to run). The world's NPC registry implements this.
type NPCStore interface {
	// ScriptForNPC returns the script name registered for the NPC GID, or false.
	ScriptForNPC(ctx context.Context, npcGID uint32) (string, bool)
}
