//go:build unit

package packet

import "testing"

func TestNewMapServerDB_HasAllEntries(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()

	type expect struct {
		cmd       uint16
		name      string
		length    int
		direction Direction
	}
	// 6 C→S + 16 S→C = 22 entries total. M11 added CZ_ACTION_REQUEST,
	// CZ_GLOBAL_MESSAGE, ZC_NOTIFY_CHAT, and ZC_ACTION_RESPONSE for the
	// chat + sit/stand handlers. M14 added ZC_SET_UNIT_IDLE for NPC
	// entity spawning.
	checks := []expect{
		{HeaderCZENTER, "CZ_ENTER", sizeCZEnter, DirectionClientToServer},
		{HeaderCZREQUESTMOVE, "CZ_REQUEST_MOVE", sizeCZRequestMove, DirectionClientToServer},
		{HeaderCZNOTIFYACTORINIT, "CZ_NOTIFY_ACTORINIT", sizeCZNotifyActorInit, DirectionClientToServer},
		{HeaderCZREQUESTTIME, "CZ_REQUEST_TIME", sizeCZRequestTime, DirectionClientToServer},
		{HeaderCZACTIONREQUEST, "CZ_ACTION_REQUEST", sizeCZActionRequest, DirectionClientToServer},
		{HeaderCZGLOBALMESSAGE, "CZ_GLOBAL_MESSAGE", VariableLength, DirectionClientToServer},

		{HeaderZCREFUSEENTER, "ZC_REFUSE_ENTER", sizeZCRefuseEnter, DirectionServerToClient},
		{HeaderZCACCEPTENTER, "ZC_ACCEPT_ENTER", sizeZCAcceptEnter, DirectionServerToClient},
		{HeaderZCNOTIFYPLAYERMOVE, "ZC_NOTIFY_PLAYERMOVE", sizeZCNotifyPlayerMove, DirectionServerToClient},
		{HeaderZCSPAWNUNIT, "ZC_SPAWN_UNIT", sizeZCSpawnUnit, DirectionServerToClient},
		{HeaderZCMAPPROPERTYR2, "ZC_MAPPROPERTY_R2", sizeZCMapPropertyR2, DirectionServerToClient},
		{HeaderZCNOTIFYTIME, "ZC_NOTIFY_TIME", sizeZCNotifyTime, DirectionServerToClient},
		{HeaderZCSTATUS, "ZC_STATUS", sizeZCStatus, DirectionServerToClient},
		{HeaderZCPARCHANGE, "ZC_PAR_CHANGE", sizeZCParChange, DirectionServerToClient},
		{HeaderZCLONGPARCHANGE, "ZC_LONGPAR_CHANGE", sizeZCLongParChange, DirectionServerToClient},
		{HeaderZCINVENTORYITEMLISTNORMAL, "ZC_INVENTORY_ITEMLIST_NORMAL", VariableLength, DirectionServerToClient},
		{HeaderZCINVENTORYITEMLISTEQUIP, "ZC_INVENTORY_ITEMLIST_EQUIP", VariableLength, DirectionServerToClient},
		{HeaderZCSKILLINFOLIST, "ZC_SKILLINFO_LIST", VariableLength, DirectionServerToClient},
		{HeaderZCSHORTCUTKEYLIST, "ZC_SHORTCUT_KEY_LIST", sizeZCShortcutKeyList, DirectionServerToClient},
		{HeaderZCNOTIFYCHAT, "ZC_NOTIFY_CHAT", VariableLength, DirectionServerToClient},
		{HeaderZCACTIONRESPONSE, "ZC_ACTION_RESPONSE", sizeZCActionResponse, DirectionServerToClient},
		{HeaderZCSETUNITIDLE, "ZC_SET_UNIT_IDLE", sizeZCSetUnitIdle, DirectionServerToClient},
		{HeaderZCUNITWALKING, "ZC_UNIT_WALKING", sizeZCUnitWalking, DirectionServerToClient},
		// P2A: inventory equip/use family — see NewMapServerDB for the
		// rAthena packetdb citations.
		{HeaderCZUSEITEM2, "CZ_USE_ITEM2", sizeCZUseItem2, DirectionClientToServer},
		{HeaderCZREQWEAREQUIPV5, "CZ_REQ_WEAR_EQUIP_V5", sizeCZReqWearEquipV5, DirectionClientToServer},
		{HeaderCZREQTAKEOFFEQUIP, "CZ_REQ_TAKEOFF_EQUIP", sizeCZReqTakeoffEquip, DirectionClientToServer},
		{HeaderZCREQWEAREQUIPACKV5, "ZC_REQ_WEAR_EQUIP_ACK_V5", sizeZCReqWearEquipAckV5, DirectionServerToClient},
		{HeaderZCREQTAKEOFFEQUIPACK, "ZC_REQ_TAKEOFF_EQUIP_ACK", sizeZCReqTakeoffEquipAck, DirectionServerToClient},
		{HeaderZCUSEITEMACK2, "ZC_USE_ITEM_ACK2", sizeZCUseItemAck2, DirectionServerToClient},
		// P2B: shop sell flow — see NewMapServerDB for the rAthena
		// packetdb citations.
		{HeaderCZPCSELLITEMLIST, "CZ_PC_SELL_ITEMLIST", VariableLength, DirectionClientToServer},
		{HeaderZCPCSELLITEMLIST, "ZC_PC_SELL_ITEMLIST", VariableLength, DirectionServerToClient},
		{HeaderZCPCSELLRESULT, "ZC_PC_SELL_RESULT", sizeZCPCSellResult, DirectionServerToClient},
		// P2C: stat allocation + level-up effect — see NewMapServerDB
		// for the rAthena packetdb citations.
		{HeaderCZSTATUSCHANGE, "CZ_STATUS_CHANGE", sizeCZStatusChange, DirectionClientToServer},
		{HeaderZCSTATUSCHANGEACK, "ZC_STATUS_CHANGE_ACK", sizeZCStatusChangeAck, DirectionServerToClient},
		{HeaderZCNOTIFYEFFECT, "ZC_NOTIFY_EFFECT", sizeZCNotifyEffect, DirectionServerToClient},
		// P3c: ground item drop — see NewMapServerDB for the rAthena
		// packetdb citation (clif_packetdb.hpp:1921, opcode 0x0ADD).
		{HeaderZCItemFallEntry, "ZC_ITEM_FALL_ENTRY", sizeZCItemFallEntry, DirectionServerToClient},
	}

	for _, c := range checks {
		def, ok := db.Lookup(c.cmd)
		if !ok {
			t.Errorf("Lookup(0x%04x) missing from map DB", c.cmd)
			continue
		}
		if def.Name != c.name {
			t.Errorf("Lookup(0x%04x).Name = %q, want %q", c.cmd, def.Name, c.name)
		}
		if def.Length != c.length {
			t.Errorf("Lookup(0x%04x).Length = %d, want %d", c.cmd, def.Length, c.length)
		}
		if def.Direction != c.direction {
			t.Errorf("Lookup(0x%04x).Direction = %d, want %d", c.cmd, def.Direction, c.direction)
		}
	}
}

func TestNewMapServerDB_Size(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()
	// 18 C→S + 31 S→C = 49 (P2A adds the inventory equip/use
	// family: CZ_USE_ITEM2, CZ_REQ_WEAR_EQUIP_V5, CZ_REQ_TAKEOFF_EQUIP,
	// ZC_REQ_WEAR_EQUIP_ACK_V5, ZC_REQ_TAKEOFF_EQUIP_ACK,
	// ZC_USE_ITEM_ACK2). P2B adds 3 sell-flow entries
	// (CZ_PC_SELL_ITEMLIST, ZC_PC_SELL_ITEMLIST, ZC_PC_SELL_RESULT).
	// P2C adds 3 stats entries (CZ_STATUS_CHANGE, ZC_STATUS_CHANGE_ACK,
	// ZC_NOTIFY_EFFECT). P3b-2 adds 3 skill-usage entries
	// (CZ_USE_SKILL2, ZC_NOTIFY_SKILL, ZC_ACK_TOUSESKILL). P3c adds
	// ZC_ITEM_FALL_ENTRY (0x0ADD) for a grand total of 59.
	// P4b adds 2 menu entries (CZ_CHOOSE_MENU, ZC_MENU_LIST) → 61.
	const want = 61
	if db.Size() != want {
		t.Errorf("NewMapServerDB Size() = %d, want %d", db.Size(), want)
	}
}

func TestNewMapServerDB_LengthLookup(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()

	cases := []struct {
		cmd  uint16
		want int
	}{
		{HeaderCZENTER, sizeCZEnter},
		{HeaderCZREQUESTMOVE, sizeCZRequestMove},
		{HeaderCZNOTIFYACTORINIT, sizeCZNotifyActorInit},
		{HeaderCZREQUESTTIME, sizeCZRequestTime},
		{HeaderCZACTIONREQUEST, sizeCZActionRequest},
		{HeaderCZGLOBALMESSAGE, VariableLength},
		{HeaderCZCHANGEDIR, sizeCZChangeDir},
		{HeaderCZREQEMOTION, sizeCZReqEmotion},
		{HeaderCZGETCHARNAMEREQUEST, sizeCZGetCharNameRequest},
		{HeaderCZRESTART, sizeCZRestart},
		{HeaderZCACCEPTENTER, sizeZCAcceptEnter},
		{HeaderZCREFUSEENTER, sizeZCRefuseEnter},
		{HeaderZCNOTIFYPLAYERMOVE, sizeZCNotifyPlayerMove},
		{HeaderZCSPAWNUNIT, sizeZCSpawnUnit},
		{HeaderZCMAPPROPERTYR2, sizeZCMapPropertyR2},
		{HeaderZCNOTIFYTIME, sizeZCNotifyTime},
		{HeaderZCSTATUS, sizeZCStatus},
		{HeaderZCPARCHANGE, sizeZCParChange},
		{HeaderZCLONGPARCHANGE, sizeZCLongParChange},
		{HeaderZCINVENTORYITEMLISTNORMAL, VariableLength},
		{HeaderZCINVENTORYITEMLISTEQUIP, VariableLength},
		{HeaderZCSKILLINFOLIST, VariableLength},
		{HeaderZCSHORTCUTKEYLIST, sizeZCShortcutKeyList},
		{HeaderZCNOTIFYCHAT, VariableLength},
		{HeaderZCACTIONRESPONSE, sizeZCActionResponse},
		{HeaderZCCHANGEDIR, sizeZCChangeDir},
		{HeaderZCEMOTION, sizeZCEmotion},
		{HeaderZCACKREQNAME, sizeZCAckReqName},
		{HeaderZCSETUNITIDLE, sizeZCSetUnitIdle},
		{HeaderZCUNITWALKING, sizeZCUnitWalking},
		// P2A: inventory equip/use family.
		{HeaderCZUSEITEM2, sizeCZUseItem2},
		{HeaderCZREQWEAREQUIPV5, sizeCZReqWearEquipV5},
		{HeaderCZREQTAKEOFFEQUIP, sizeCZReqTakeoffEquip},
		{HeaderZCREQWEAREQUIPACKV5, sizeZCReqWearEquipAckV5},
		{HeaderZCREQTAKEOFFEQUIPACK, sizeZCReqTakeoffEquipAck},
		{HeaderZCUSEITEMACK2, sizeZCUseItemAck2},
		// P2B: shop sell flow.
		{HeaderCZPCSELLITEMLIST, VariableLength},
		{HeaderZCPCSELLITEMLIST, VariableLength},
		{HeaderZCPCSELLRESULT, sizeZCPCSellResult},
		// P2C: stat allocation + level-up effect.
		{HeaderCZSTATUSCHANGE, sizeCZStatusChange},
		{HeaderZCSTATUSCHANGEACK, sizeZCStatusChangeAck},
		{HeaderZCNOTIFYEFFECT, sizeZCNotifyEffect},
	}
	for _, c := range cases {
		got, ok := db.Length(c.cmd)
		if !ok {
			t.Errorf("Length(0x%04x) ok = false, want true", c.cmd)
			continue
		}
		if got != c.want {
			t.Errorf("Length(0x%04x) = %d, want %d", c.cmd, got, c.want)
		}
	}
}

func TestNewMapServerDB_NoDuplicateIDs(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()
	seen := make(map[uint16]struct{}, db.Size())
	for _, def := range mapDBEntries() {
		if _, dup := seen[def.ID]; dup {
			t.Errorf("duplicate ID 0x%04x (%s) in map DB", def.ID, def.Name)
		}
		seen[def.ID] = struct{}{}
	}
	if len(seen) != db.Size() {
		t.Errorf("distinct IDs (%d) != db.Size() (%d)", len(seen), db.Size())
	}
}

// mapDBEntries returns the entries NewMapServerDB registers, for
// invariant checking without exposing internals.
func mapDBEntries() []Definition {
	db := NewMapServerDB()
	out := make([]Definition, 0, db.Size())
	for _, def := range db.entries {
		out = append(out, def)
	}
	return out
}
