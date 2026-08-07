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
	// Core entries from the M-series + later phases. This list is a
	// representative subset (not exhaustive); TestNewMapServerDB_Size
	// pins the authoritative total. A3 adds the ZC_INVENTORY_START /
	// ZC_INVENTORY_END bracket frames.
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
		{HeaderZCLONGLONGPARCHANGE, "ZC_LONGLONGPAR_CHANGE", sizeZCLongLongParChange, DirectionServerToClient},
		{HeaderZCINVENTORYITEMLISTNORMAL, "ZC_INVENTORY_ITEMLIST_NORMAL", VariableLength, DirectionServerToClient},
		{HeaderZCINVENTORYITEMLISTEQUIP, "ZC_INVENTORY_ITEMLIST_EQUIP", VariableLength, DirectionServerToClient},
		// A3: inventory bracket frames wrapping the item lists for
		// MAIN@20250604 (clif.cpp clif_inventoryStart/End).
		{HeaderZCINVENTORYSTART, "ZC_INVENTORY_START", sizeZCInventoryStart, DirectionServerToClient},
		{HeaderZCINVENTORYEND, "ZC_INVENTORY_END", sizeZCInventoryEnd, DirectionServerToClient},
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
		// A4: five C→S drop aliases share one CZ_ITEM_DROP layout (the
		// effective 20250604 opcode is 0x0363, clif_shuffle.hpp:4733; 0x0438 is
		// CZ_USE_SKILL2 and 0x0362 is the modern pickup opcode, both excluded).
		{HeaderCZDROPITEM0363, "CZ_ITEM_DROP", sizeCZDropItem, DirectionClientToServer},
		{HeaderCZDROPITEM0885, "CZ_ITEM_DROP", sizeCZDropItem, DirectionClientToServer},
		{HeaderCZDROPITEM02C4, "CZ_ITEM_DROP", sizeCZDropItem, DirectionClientToServer},
		{HeaderCZDROPITEM0891, "CZ_ITEM_DROP", sizeCZDropItem, DirectionClientToServer},
		{HeaderCZDROPITEM089E, "CZ_ITEM_DROP", sizeCZDropItem, DirectionClientToServer},
		// A4: the modern pickup alias 0x0362 (clif_shuffle.hpp:4732 TakeItem)
		// shares the CZ_ITEM_PICKUP parser with the legacy 0x009f.
		{HeaderCZITEMTAKE0362, "CZ_ITEM_PICKUP", sizeCZItemPickup, DirectionClientToServer},
		// A4: server→client floor-item + ack frames.
		{HeaderZCItemEntry, "ZC_ITEM_ENTRY", sizeZCItemEntry, DirectionServerToClient},
		{HeaderZCItemDisappear, "ZC_ITEM_DISAPPEAR", sizeZCItemDisappear, DirectionServerToClient},
		{HeaderZCItemThrowAck, "ZC_ITEM_THROW_ACK", sizeZCItemThrowAck, DirectionServerToClient},
		{HeaderZCItemPickupAck, "ZC_ITEM_PICKUP_ACK", sizeZCItemPickupAck, DirectionServerToClient},
		// M14c: NPC input dialogs (clif.cpp:13378/13397, packets.hpp:769/775/1837/1844).
		{HeaderCZINPUTEDITDLG, "CZ_INPUT_EDITDLG", sizeCZInputEditDlg, DirectionClientToServer},
		{HeaderCZINPUTEDITDLGSTR, "CZ_INPUT_EDITDLGSTR", VariableLength, DirectionClientToServer},
		{HeaderZCOPENEDITDLG, "ZC_OPEN_EDITDLG", sizeZCOpenEditDlg, DirectionServerToClient},
		{HeaderZCOPENEDITDLGSTR, "ZC_OPEN_EDITDLGSTR", sizeZCOpenEditDlg, DirectionServerToClient},
		// M14d: ground-target skills (clif_packetdb.hpp:1905, packets_struct.hpp:4696).
		{HeaderCZUSESKILLTOPOS, "CZ_USE_SKILL_TOPOS", sizeCZUseSkillToPos, DirectionClientToServer},
		{HeaderZCNOTIFYGROUNDSKILL, "ZC_NOTIFY_GROUNDSKILL", sizeZCNotifyGroundSkill, DirectionServerToClient},
		// S1: trade handshake family (clif_packetdb.hpp:87,89,93; packets.hpp:373,387; :1062).
		{HeaderCZTRADEREQUEST, "CZ_TRADE_REQUEST", sizeCZTradeRequest, DirectionClientToServer},
		{HeaderCZTRADEACK, "CZ_TRADE_ACK", sizeCZTradeAck, DirectionClientToServer},
		{HeaderCZTRADECANCEL, "CZ_TRADE_CANCEL", sizeCZTradeCancel, DirectionClientToServer},
		{HeaderZCREQEXCHANGEITEM, "ZC_REQ_EXCHANGE_ITEM", sizeZCReqExchange, DirectionServerToClient},
		{HeaderZCACKEXCHANGEITEM, "ZC_ACK_EXCHANGE_ITEM", sizeZCAckExchange, DirectionServerToClient},
		{HeaderZCCANCELEXCHANGEITEM, "ZC_CANCEL_EXCHANGE_ITEM", sizeZCCancelExchange, DirectionServerToClient},
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
	// A3 adds the inventory bracket (ZC_INVENTORY_START, ZC_INVENTORY_END) → 63.
	// A4 adds 5 C→S drop aliases (CZ_ITEM_DROP) + the modern pickup alias
	// 0x0362 (CZ_ITEM_PICKUP) + 4 S→C floor-item/ack frames
	// (ZC_ITEM_ENTRY/DISAPPEAR/THROW_ACK/PICKUP_ACK) → 73.
	// M7c adds ZC_LONGLONGPAR_CHANGE (0x0acb) for the 64-bit exp parameters at
	// PACKETVER >= 20170830 → 74.
	// M10a adds the legacy CZ_ITEM_PICKUP (0x009f) → 75. Name-resolution adds
	// two S→C name replies (ZC_ACK_REQNAMEALL2 0x0a30, ZC_ACK_REQNAMEALL_NPC
	// 0x0adf) → 77. P2A adds ZC_SPRITE_CHANGE (0x01d7) for the equip/unequip
	// look-sprite broadcast → 78. P2c adds the whisper family (CZ_WHISPER
	// 0x0096, ZC_WHISPER 0x09de, ZC_ACK_WHISPER 0x09df) → 81. M14c adds the
	// NPC input-dialog family (CZ_INPUT_EDITDLG 0x0143, CZ_INPUT_EDITDLGSTR
	// 0x01d5, ZC_OPEN_EDITDLG 0x0142, ZC_OPEN_EDITDLGSTR 0x01d4) → 85. M14d adds
	// the ground-skill family (CZ_USE_SKILL_TOPOS 0x0AF4, ZC_NOTIFY_GROUNDSKILL
	// 0x0117) → 87. S1 adds the trade-handshake family (CZ_TRADE_REQUEST 0x00e4,
	// CZ_TRADE_ACK 0x00e6, CZ_TRADE_CANCEL 0x00ed, ZC_REQ_EXCHANGE_ITEM 0x01f4,
	// ZC_ACK_EXCHANGE_ITEM 0x01f5, ZC_CANCEL_EXCHANGE_ITEM 0x00ee) → 93.
	const want = 93
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
		{HeaderZCLONGLONGPARCHANGE, sizeZCLongLongParChange},
		{HeaderZCINVENTORYITEMLISTNORMAL, VariableLength},
		{HeaderZCINVENTORYITEMLISTEQUIP, VariableLength},
		{HeaderZCSKILLINFOLIST, VariableLength},
		{HeaderZCSHORTCUTKEYLIST, sizeZCShortcutKeyList},
		{HeaderZCNOTIFYCHAT, VariableLength},
		{HeaderZCACTIONRESPONSE, sizeZCActionResponse},
		{HeaderZCCHANGEDIR, sizeZCChangeDir},
		{HeaderZCEMOTION, sizeZCEmotion},
		{HeaderZCSPRITECHANGE, sizeZCSpriteChange},
		{HeaderZCACKREQNAME, sizeZCAckReqName},
		{HeaderZCSETUNITIDLE, sizeZCSetUnitIdle},
		{HeaderZCUNITWALKING, sizeZCUnitWalking},
		// P2A: inventory equip/use family.
		{HeaderCZITEMPICKUP, sizeCZItemPickup},
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
		// P3c: ground item drop.
		{HeaderZCItemFallEntry, sizeZCItemFallEntry},
		// A4: five C→S drop aliases + the modern pickup alias + four S→C
		// floor-item/ack frames.
		{HeaderCZDROPITEM0363, sizeCZDropItem},
		{HeaderCZDROPITEM0885, sizeCZDropItem},
		{HeaderCZDROPITEM02C4, sizeCZDropItem},
		{HeaderCZDROPITEM0891, sizeCZDropItem},
		{HeaderCZDROPITEM089E, sizeCZDropItem},
		{HeaderCZITEMTAKE0362, sizeCZItemPickup},
		{HeaderZCItemEntry, sizeZCItemEntry},
		{HeaderZCItemDisappear, sizeZCItemDisappear},
		{HeaderZCItemThrowAck, sizeZCItemThrowAck},
		{HeaderZCItemPickupAck, sizeZCItemPickupAck},
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
