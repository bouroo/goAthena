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

// ScriptWorld is the world capability the script VM bridge needs for effect
// builtins (warp, heal). It is implemented by the world bounded context and
// injected into the Engine; isolating the port here keeps the content domain
// free of world/app imports. Methods are best-effort: a not-found error means
// the player is not currently on a map, and the builtin drops the effect frame.
type ScriptWorld interface {
	// WarpPlayer persists the player's destination map + tile. The caller emits
	// ZC_NPCACK_MAPMOVE; the client reconnects and re-enters there.
	WarpPlayer(charID uint32, mapName string, x, y int16) error
	// HealPlayer restores HP and SP by hpPct/spPct percent of the player's
	// maximums, clamped to [0, max], and returns the resulting (HP, SP) so the
	// caller can emit the stat-change packets.
	HealPlayer(charID uint32, hpPct, spPct int) (hp, sp int32, err error)
}

// DialogSession is one player's active NPC dialog: the writer to send dialog
// packets to the client, and the channel the VM goroutine blocks on while
// waiting for the client's response. CharID is the player's entity id (char_id)
// so effect builtins can target the player in the world.
type DialogSession struct {
	NpcID  uint32
	CharID uint32
	Writer PacketWriter
	Signal chan DialogSignal
}

// NPCStore resolves an NPC entity GID to its script name (so a click can find
// which script to run) and registers new GID→name mappings as the world
// seeder places NPCs. The world's NPC registry implements this.
type NPCStore interface {
	// ScriptForNPC returns the script name registered for the NPC GID, or false.
	ScriptForNPC(ctx context.Context, npcGID uint32) (string, bool)
	// Register maps an NPC GID to its script name.
	Register(gid uint32, scriptName string)
}
