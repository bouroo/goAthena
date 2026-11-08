package packet

// Map-server packet header IDs for PACKETVER 20250604 (Thai Classic pre-Renewal).
//
// Sources cited per constant; the map-server packet database in rAthena is
// build-time generated from rathena/src/map/clif_packetdb.hpp + the per-PACKETVER
// shuffle files. Until codegen lands (deferred to P1.2b) we hand-register the
// minimal set required for the M3a map-server handshake:
//
//	ZC_ACCEPT_ENTER      (S→C, sent on successful CZ_ENTER)
//	ZC_REFUSE_ENTER      (S→C, sent on rejected CZ_ENTER)
//	ZC_NOTIFY_PLAYERMOVE (S→C, sent on accepted CZ_REQUEST_MOVE)
//	ZC_SPAWN_UNIT        (S→C, sent after ZC_ACCEPT_ENTER for own entity)
//	ZC_MAPPROPERTY_R2    (S→C, sent on CZ_NOTIFY_ACTORINIT after map load)
//	ZC_NOTIFY_TIME       (S→C, sent on CZ_REQUEST_TIME ping)
//	CZ_ENTER             (C→S, client requests to enter the map server)
//	CZ_REQUEST_MOVE      (C→S, client requests a single step in a cardinal dir)
//	CZ_NOTIFY_ACTORINIT  (C→S, client signals map load complete)
//	CZ_REQUEST_TIME      (C→S, client requests server tick for latency)

// C→S — client → map server.
// HeaderCZENTER is the source pack's own opcode for this packet.
func HeaderCZENTER() uint16 {
	return opcodeForHandler("clif_parse_WantToConnection", sizeCZEnter(), []int{2, 6, 10, 14, 18})
}

// HeaderCZREQUESTMOVE is the source pack's own opcode for this packet.
func HeaderCZREQUESTMOVE() uint16 {
	return opcodeForHandler("clif_parse_WalkToXY", sizeCZRequestMove(), []int{2})
}

// HeaderCZNOTIFYACTORINIT is the source pack's own opcode for this packet.
func HeaderCZNOTIFYACTORINIT() uint16 {
	return opcodeForHandler("clif_parse_LoadEndAck", sizeCZNotifyActorInit(), nil)
}

// HeaderCZREQUESTTIME is the source pack's own opcode for this packet.
func HeaderCZREQUESTTIME() uint16 {
	return opcodeForHandler("clif_parse_TickSend", sizeCZRequestTime(), []int{2})
}

// CZ_ACTION_REQUEST (0x0089) — sit/stand/attack request. rathena/src/map/
// clif_packetdb.hpp:38 (`parseable_packet(0x0089,7,clif_parse_ActionRequest,2,6)`).
// Fixed 7 bytes: [2:cmd][4:targetGID][1:action].
// HeaderCZACTIONREQUEST is the source pack's own opcode for this packet.
func HeaderCZACTIONREQUEST() uint16 {
	return opcodeForHandler("clif_parse_ActionRequest", sizeCZActionRequest(), []int{2, 6})
}

// CZ_GLOBAL_MESSAGE (0x008c) — public chat. rathena/src/map/
// clif_packetdb.hpp:40 (`parseable_packet(0x008c,-1,clif_parse_GlobalMessage,2,4)`).
// Variable length: [2:cmd][2:packetLength][n:text+null].
// HeaderCZGLOBALMESSAGE is the source pack's own opcode for this packet.
func HeaderCZGLOBALMESSAGE() uint16 {
	return opcodeForHandler("clif_parse_GlobalMessage", -1, []int{2, 4})
}

// CZ_WHISPER (0x0096) — private message to a named target. rathena/src/map/
// clif_packetdb.hpp:46 (`parseable_packet(0x0096,-1,clif_parse_WisMessage,2,4,28)`).
// Variable length: [2:cmd][2:packetLength][24:targetNick char[24]][n:message+null];
// fixed min 28 bytes (header + 24-byte name + ≥1 message byte).
// HeaderCZWHISPER is the source pack's own opcode for this packet.
func HeaderCZWHISPER() uint16 {
	return opcodeForHandler("clif_parse_WisMessage", -1, []int{2, 4, 28})
}

// CZ_CHANGE_DIRECTION (0x009b) — direction change request. rathena/src/map/
// clif_packetdb.hpp:48 (`parseable_packet(0x009b,5,clif_parse_ChangeDir,2,4)`).
// Fixed 5 bytes: [2:cmd][2:headDir uint16][1:dir uint8]. The pos array
// [2, 4] documents headDir at offset 2 and dir at offset 4 — the
// handler reads headDir via RFIFOB on this PACKETVER (clif.cpp:11613).
// HeaderCZCHANGEDIR is the source pack's own opcode for this packet.
func HeaderCZCHANGEDIR() uint16 {
	return opcodeForHandler("clif_parse_ChangeDir", sizeCZChangeDir(), []int{2, 4})
}

// CZ_REQ_EMOTION (0x00bf) — emotion request. rathena/src/map/
// clif_packetdb.hpp:66 (`parseable_packet(HEADER_CZ_REQ_EMOTION,
// sizeof(PACKET_CZ_REQ_EMOTION), clif_parse_Emotion, 0)`).
// Fixed 3 bytes: [2:cmd][1:emotion_type]. The struct definition lives
// at rathena/src/map/packets.hpp:1406-1409.
// HeaderCZREQEMOTION is the source pack's own opcode for this packet.
func HeaderCZREQEMOTION() uint16 { return opcode("CZ_REQ_EMOTION") }

// CZ_PMIgnore (0x00cf) — add/remove one name from this player's whisper
// ignore list (/ex and /in). rathena/src/map/clif_packetdb.hpp:78
// (`parseable_packet(0x00cf,27,clif_parse_PMIgnore,2,26)`).
// Fixed 27 bytes: [2:cmd][24:name char[24]][1:type 0=block 1=unblock].
// HeaderCZPMIGNORE is the source pack's own opcode for this packet.
func HeaderCZPMIGNORE() uint16 {
	return opcodeForHandler("clif_parse_PMIgnore", sizeCZPMIgnore(), []int{2, 26})
}

// CZ_SETTING_WHISPER_STATE (0x00d0) — allow/deny ALL whispers (/exall and
// /inall). rathena/src/map/clif_packetdb.hpp:79. Fixed 3 bytes:
// [2:cmd][1:type 0=deny-all 1=allow-all].
// HeaderCZSETTINGWHISPERSTATE is the source pack's own opcode for this packet.
func HeaderCZSETTINGWHISPERSTATE() uint16 { return opcode("CZ_SETTING_WHISPER_STATE") }

// CZ_REQ_WHISPER_LIST (0x00d3) — request this player's ignore list (/wl).
// rathena/src/map/clif_packetdb.hpp:80 (`parseable_packet(0x00d3,2,...)`).
// Fixed 2 bytes: [2:cmd].
// HeaderCZREQWHISPERLIST is the source pack's own opcode for this packet.
func HeaderCZREQWHISPERLIST() uint16 {
	return opcodeForHandler("clif_parse_PMIgnoreList", 2, nil)
}

// S→C — map server → client.
// HeaderZCACCEPTENTER is the source pack's own opcode for this packet.
func HeaderZCACCEPTENTER() uint16 { return opcode("ZC_ACCEPT_ENTER") }

// HeaderZCREFUSEENTER is the source pack's own opcode for this packet.
func HeaderZCREFUSEENTER() uint16 { return opcode("ZC_REFUSE_ENTER") }

// HeaderZCNOTIFYPLAYERMOVE is the source pack's own opcode for this packet.
func HeaderZCNOTIFYPLAYERMOVE() uint16 { return opcode("ZC_NOTIFY_PLAYERMOVE") }

// HeaderZCSPAWNUNIT is the source pack's own opcode for this packet.
func HeaderZCSPAWNUNIT() uint16 { return opcode("spawn_unitType") }

// HeaderZCSETUNITIDLE is the source pack's own opcode for this packet.
func HeaderZCSETUNITIDLE() uint16 { return opcode("idle_unitType") }

// HeaderZCUNITWALKING is the source pack's own opcode for this packet.
func HeaderZCUNITWALKING() uint16 { return opcode("unit_walkingType") }

// HeaderZCMAPPROPERTYR2 is the source pack's own opcode for this packet.
func HeaderZCMAPPROPERTYR2() uint16 { return opcode("maptypeproperty2Type") }

// HeaderZCNOTIFYTIME is the source pack's own opcode for this packet.
func HeaderZCNOTIFYTIME() uint16 { return opcode("ZC_NOTIFY_TIME") }

// ZC_NOTIFY_CHAT (0x008d) — chat echo. rathena/src/map/packets_struct.hpp:2337
// (`PACKET_ZC_NOTIFY_CHAT { int16 PacketType; int16 PacketLength; uint32 GID; char Message[] }`).
// Variable length: [2:cmd][2:packetLength][4:GID][n:text+null].
// HeaderZCNOTIFYCHAT is the source pack's own opcode for this packet.
func HeaderZCNOTIFYCHAT() uint16 { return opcode("ZC_NOTIFY_CHAT") }

// ZC_BROADCAST (0x009a) — server announcement. rathena/src/map/packets.hpp:199
// (`PACKET_ZC_BROADCAST { int16 packetType; int16 PacketLength; char message[] }`).
// Variable length: [2:cmd][2:packetLength][n:text+null]. The colour is not a
// field: clif_broadcast (clif.cpp:6725) prefixes the text with "blue" for
// BC_BLUE or "ssss" for BC_WOE, which the client recognises.
// HeaderZCBROADCAST is the source pack's own opcode for this packet.
func HeaderZCBROADCAST() uint16 { return opcode("ZC_BROADCAST") }

// ZC_CHANGE_DIRECTION (0x009c) — direction echo. rathena/src/map/
// packets.hpp:688-694 (`PACKET_ZC_CHANGE_DIRECTION { int16 packetType;
// uint32 srcId; uint16 headDir; uint8 dir }`). Fixed 9 bytes:
// [2:cmd][4:srcId][2:headDir uint16][1:dir uint8].
// HeaderZCCHANGEDIR is the source pack's own opcode for this packet.
func HeaderZCCHANGEDIR() uint16 { return opcode("ZC_CHANGE_DIRECTION") }

// ZC_EMOTION (0x00c0) — emotion echo. rathena/src/map/packets.hpp:1973-1978
// (`PACKET_ZC_EMOTION { int16 packetType; int32 GID; uint8 type }`).
// Fixed 7 bytes: [2:cmd][4:GID int32][1:type uint8].
// HeaderZCEMOTION is the source pack's own opcode for this packet.
func HeaderZCEMOTION() uint16 { return opcode("ZC_EMOTION") }

// ZC_SETTING_WHISPER_PC (0x00d1) — result of /ex or /in (single-name ignore
// state change). rathena/src/map/packets.hpp PACKET_ZC_SETTING_WHISPER_PC.
// Fixed 4 bytes: [2:cmd][1:type echoing request][1:result 0=ok 1=fail 2=too-many].
// HeaderZCSETTINGWHISPERPC is the source pack's own opcode for this packet.
func HeaderZCSETTINGWHISPERPC() uint16 { return opcode("ZC_SETTING_WHISPER_PC") }

// ZC_SETTING_WHISPER_STATE (0x00d2) — result of /exall or /inall.
// rathena/src/map/packets.hpp PACKET_ZC_SETTING_WHISPER_STATE.
// Fixed 4 bytes: [2:cmd][1:type][1:result 0=ok 1=fail].
// HeaderZCSETTINGWHISPERSTATE is the source pack's own opcode for this packet.
func HeaderZCSETTINGWHISPERSTATE() uint16 { return opcode("ZC_SETTING_WHISPER_STATE") }

// ZC_WHISPER_LIST (0x00d4) — the player's ignore list (/wl reply).
// rathena/src/map/packets.hpp PACKET_ZC_WHISPER_LIST.
// Variable: [2:cmd][2:packetSize]{24-byte NUL-padded names}*.
// HeaderZCWHISPERLIST is the source pack's own opcode for this packet.
func HeaderZCWHISPERLIST() uint16 { return opcode("ZC_WHISPER_LIST") }

// CZ_GETCHARNAMEREQUEST (0x0094) — client requests a character name by GID.
// rathena/src/map/clif_packetdb.hpp:45 (`parseable_packet(0x0094,6,clif_parse_GetCharNameRequest,2)`).
// Fixed 6 bytes: [2:cmd][4:GID int32].
// HeaderCZGETCHARNAMEREQUEST is the source pack's own opcode for this packet.
func HeaderCZGETCHARNAMEREQUEST() uint16 {
	return opcodeForHandler("clif_parse_GetCharNameRequest", sizeCZGetCharNameRequest(), []int{2})
}

// ZC_ACK_REQNAME (0x0095) — server responds with a character name.
// rathena/src/map/clif.cpp:9923 (`0095 <id>.L <char name>.24B`).
// Fixed 30 bytes: [2:cmd][4:GID int32][24:name char[24]].
// HeaderZCACKREQNAME is the source pack's own opcode for this packet.
func HeaderZCACKREQNAME() uint16 { return opcode("reqName") }

// ZC_ACK_REQNAMEALL2 (0x0a30) — full name reply for a PC. At PACKETVER
// MAIN_NUM>=20150225 (ClientROThailand 20250604 qualifies) clif_name emits
// this — NOT the legacy 0x0095/0x0195 — carrying char/party/guild/position
// names plus a title id (packets_struct.hpp:3562-3573).
// Fixed 106 bytes: [2:cmd][4:GID][24:name][24:party][24:guild][24:position][4:titleID].
// HeaderZCACKREQNAMEALL2 is the source pack's own opcode for this packet.
func HeaderZCACKREQNAMEALL2() uint16 { return opcode("ZC_ACK_REQNAMEALL") }

// ZC_ACK_REQNAMEALL_NPC (0x0adf) — name reply for an NPC/mob/pet/hom/mer/elem.
// At PACKETVER MAIN_NUM>=20180207 (20250604 qualifies) clif_name emits this
// carrying groupId, name, and title (packets_struct.hpp:3586-3594).
// Fixed 58 bytes: [2:cmd][4:GID][4:groupId][24:name][24:title].
// HeaderZCACKREQNAMEALLNPC is the source pack's own opcode for this packet.
func HeaderZCACKREQNAMEALLNPC() uint16 { return opcode("ZC_ACK_REQNAMEALL_NPC") }

// ZC_WHISPER (0x09de) — delivered private message to the recipient. At
// PACKETVER MAIN_NUM>=20131204 (ClientROThailand 20250604 qualifies) clif
// emits this 0x09de branch (packets_struct.hpp:5347-5356), NOT the legacy
// 0x0097. Variable length:
// [2:cmd][2:packetLength][4:senderGID uint32][24:sender char[]][1:isAdmin][n:message+null].
// HeaderZCWHISPER is the source pack's own opcode for this packet.
func HeaderZCWHISPER() uint16 { return opcode("ZC_WHISPER") }

// ZC_ACK_WHISPER (0x09df) — whisper result ack to the sender. At PACKETVER
// >= 20131223 (20250604 qualifies; packets.hpp:1225-1231), NOT legacy 0x0098.
// Fixed 7 bytes: [2:cmd][1:result][4:CID uint32=sender char_id]. result:
// 0=success, 1=target offline (2/3 ignored = not implemented, no ignore list).
// HeaderZCACKWHISPER is the source pack's own opcode for this packet.
func HeaderZCACKWHISPER() uint16 { return opcode("ZC_ACK_WHISPER") }

// CZ_RESTART (0x00b2) — client requests respawn or return to char select.
// rathena/src/map/clif_packetdb.hpp:61 (`parseable_packet(0x00b2,3,clif_parse_Restart,2)`).
// Fixed 3 bytes: [2:cmd][1:type uint8] (0=respawn, 1=return to char select).
// HeaderCZRESTART is the source pack's own opcode for this packet.
func HeaderCZRESTART() uint16 {
	return opcodeForHandler("clif_parse_Restart", sizeCZRestart(), []int{2})
}

// ZC_RESTART_ACK (0x00b3) — server acknowledges a CZ_RESTART. Built by
// rAthena's clif_charselectok (clif.cpp:846) in response to the char-server's
// 0x2b03 reply to a char-select request (chrif.cpp:1800). The type byte is an
// OK/refuse flag, NOT the request's selector: 1 = char-select allowed (client
// disconnects the map conn and reconnects to the char server), 0 = refused.
// Wire: rathena/src/map/packets.hpp:598-602
// (`struct PACKET_ZC_RESTART_ACK { int16 packetType; uint8 type; }`).
// HeaderZCRESTARTACK is the source pack's own opcode for this packet.
func HeaderZCRESTARTACK() uint16 { return opcode("ZC_RESTART_ACK") }

// CZ_CONTACTNPC (0x0090) — client clicks an NPC. rathena/src/map/
// clif_packetdb.hpp:42 (`parseable_packet(0x0090,7,clif_parse_NpcClicked,2,6)`).
// Fixed 7 bytes: [2:cmd][4:AID uint32][1:type uint8] (1=click).
// HeaderCZCONTACTNPC is the source pack's own opcode for this packet.
func HeaderCZCONTACTNPC() uint16 { return opcode("CZ_CONTACTNPC") }

// ZC_NPCACK_MAPMOVE (0x0091) — server tells the client to change maps after
// a warp/teleport (rAthena clif_changemap, clif.cpp). The client loads the
// named map and reconnects with a fresh CZ_ENTER; the map name carries no
// ".gat" extension (the client appends it). Wire:
// rathena/src/map/packets.hpp PACKET_ZC_NPCACK_MAPMOVE —
// `int16 packetType; char mapName[16]; uint16 xPos; uint16 yPos;` = 22 bytes.
// HeaderZCNPCACKMAPMOVE is the source pack's own opcode for this packet.
func HeaderZCNPCACKMAPMOVE() uint16 { return opcode("ZC_NPCACK_MAPMOVE") }

// ZC_NPCACK_SERVERMOVE (0x0ac7) — cross-map-server relocation directive
// (rAthena clif_changemapserver, clif.cpp:2168): like ZC_NPCACK_MAPMOVE but
// the client reconnects to the given ip:port zone instead of this one.
// At PACKETVER >= 20170315 the packet is named SERVERMOVE_DOMAIN and the
// opcode moves 0x0092 → 0x0ac7 (rathena/src/map/packets.hpp:700-726).
// HeaderZCNPCACKSERVERMOVE is the source pack's own opcode for this packet.
func HeaderZCNPCACKSERVERMOVE() uint16 { return opcode("ZC_NPCACK_SERVERMOVE") }

// CZ_REQNEXTSCRIPT (0x00b9) — client clicks "Next" in a dialog.
// rathena/src/map/clif_packetdb.hpp:60 (`parseable_packet(0x00b9,6,clif_parse_ScriptContinue,2)`).
// Fixed 6 bytes: [2:cmd][4:NpcID uint32].
// HeaderCZREQNEXTSCRIPT is the source pack's own opcode for this packet.
func HeaderCZREQNEXTSCRIPT() uint16 {
	return opcodeForHandler("clif_parse_NpcNextClicked", 6, []int{2})
}

// P2A: CZ_ITEM_PICKUP (0x009f) — client requests to pick up a floor item.
// rathena/src/map/clif_packetdb.hpp:50
// (`parseable_packet(0x009f,6,clif_parse_TakeItem,2)`); this is the band
// PACKETVER 20250604 resolves to (the higher-line 0x009f entries are older
// PACKETVER bands that do not apply). Fixed 6 bytes:
// [2:cmd=0x009f][4:map_object_id uint32] — the floor item's ground id (the
// same id ZC_ITEM_FALL_ENTRY carries). clif_parse_TakeItem reads it with
// RFIFOL(fd,2); EVERY failure branch falls through to a fail ack, so the
// pickup handler must always answer (see map_item_drop.go
// ItemPickupAckResponse).
//
// At PACKETVER 20250604 the effective C→S pickup opcode is 0x0362, NOT
// 0x009f: clif_shuffle.hpp:4732 maps 0x0362 to clif_parse_TakeItem (it
// overrides clif_packetdb.hpp:1561's DropItem binding because clif_shuffle
// is included after clif_packetdb and packetdb_addpacket last-writes-wins),
// while 0x009f's final active binding is UseItem (clif_packetdb.hpp:1205).
// Both share the 6-byte <cmd>.<W><ground id>.L layout, so dispatch accepts
// 0x009f (legacy) and 0x0362 (modern) through the same parser.
// HeaderCZITEMPICKUP is the source pack's own opcode for this packet.
func HeaderCZITEMPICKUP() uint16 {
	return opcodeForHandler("clif_parse_TakeItem", sizeCZItemPickup(), []int{2})
}

// HeaderCZITEMTAKEOpcode is the modern pickup alias at PACKETVER 20250604
// (clif_shuffle.hpp:4732 — clif_parse_TakeItem). See HeaderCZITEMPICKUP().
// HeaderCZITEMTAKE0362 is the source pack's own opcode for this packet.
func HeaderCZITEMTAKE0362() uint16 {
	return opcodeForHandler("clif_parse_TakeItem", sizeCZItemPickup(), []int{2})
}

// P2A: CZ_USE_ITEM2 (0x0439) — client requests to use a consumable
// item. rathena/src/map/clif_packetdb.hpp:1151
// (`parseable_packet(0x0439,8,clif_parse_UseItem,2,4)`).
// Fixed 8 bytes: [2:cmd=0x0439][2:inventory index uint16]
// [4:AID uint32]. The 2-byte "item id" rAthena reads at
// packet_db[..].pos[0] is the inventory index, not the item DB
// nameid — clif_parse_UseItem uses it to look up the row in
// `inventory` and then resolves nameid from there.
// HeaderCZUSEITEM2 is the source pack's own opcode for this packet.
func HeaderCZUSEITEM2() uint16 {
	return opcodeForHandler("clif_parse_UseItem", sizeCZUseItem2(), []int{2, 4})
}

// P2A: CZ_REQ_WEAR_EQUIP_V5 (0x0998) — client requests to equip
// an item. rathena/src/map/packets.hpp:1504-1509
// (PACKET_CZ_REQ_WEAR_EQUIP, PACKETVER >= 20120925 branch).
// Fixed 10 bytes: [2:cmd=0x0998][2:inventory index uint16]
// [4:equip position uint32 — EQP_* bitmask]. The 32-bit position
// field is what makes this the "V5" shape; pre-20120925 uses
// uint16 position with opcode 0x00a9.
// HeaderCZREQWEAREQUIPV5 is the source pack's own opcode for this packet.
func HeaderCZREQWEAREQUIPV5() uint16 { return opcode("CZ_REQ_WEAR_EQUIP") }

// P2A: CZ_REQ_TAKEOFF_EQUIP (0x00ab) — client requests to
// unequip an item. rathena/src/map/clif_packetdb.hpp:59
// (`parseable_packet(0x00ab,4,clif_parse_UnequipItem,2)`).
// Fixed 4 bytes: [2:cmd=0x00ab][2:inventory index uint16]. The
// unequip handler ignores the equip-position field the client
// sends; the server derives the position from the row's
// `inventory.equip` column.
// HeaderCZREQTAKEOFFEQUIP is the source pack's own opcode for this packet.
func HeaderCZREQTAKEOFFEQUIP() uint16 {
	return opcodeForHandler("clif_parse_UnequipItem", 4, []int{2})
}

// CZ_CLOSE_DIALOG (0x0146) — client clicks "Close" in a dialog.
// rathena/src/map/clif_packetdb.hpp:72 (`parseable_packet(0x0146,6,clif_parse_CloseDialog,2)`).
// Fixed 6 bytes: [2:cmd][4:GID uint32].
// HeaderCZCLOSEDIALOG is the source pack's own opcode for this packet.
func HeaderCZCLOSEDIALOG() uint16 { return opcode("CZ_CLOSE_DIALOG") }

// ZC_SAY_DIALOG2 (0x0972) — server sends dialog text (PACKETVER >= 20220504).
// rathena/src/map/packets_struct.hpp: ZC_SAY_DIALOG2.
// Variable length: [2:cmd][2:packetLength][4:NpcID][1:type][n:message+null].
// HeaderZCSAYDIALOG2 is the source pack's own opcode for this packet.
func HeaderZCSAYDIALOG2() uint16 { return opcode("ZC_SAY_DIALOG2") }

// ZC_WAIT_DIALOG2 (0x0973) — server shows "Next" button (PACKETVER >= 20220504).
// rathena/src/map/packets_struct.hpp: ZC_WAIT_DIALOG2.
// Fixed 7 bytes: [2:cmd][4:NpcID][1:type].
// HeaderZCWAITDIALOG2 is the source pack's own opcode for this packet.
func HeaderZCWAITDIALOG2() uint16 { return opcode("ZC_WAIT_DIALOG2") }

// ZC_CLOSE_DIALOG (0x00b6) — server shows "Close" button.
// rathena/src/map/clif_packetdb.hpp:58 (`packet(0x00b6,6,clif_parse_CloseDialog,0)`).
// Fixed 6 bytes: [2:cmd][4:NpcID].
// HeaderZCCLOSEDIALOG is the source pack's own opcode for this packet.
func HeaderZCCLOSEDIALOG() uint16 { return opcode("ZC_CLOSE_DIALOG") }

// ZC_MENU_LIST (0x00b7) — server sends menu items for selection.
// rathena/src/map/packets_struct.hpp: PACKET_ZC_MENU_LIST.
// Variable length: [2:cmd][2:packetLength][4:NpcID][n:menu items colon-separated + NUL].
// HeaderZCMENULIST is the source pack's own opcode for this packet.
func HeaderZCMENULIST() uint16 { return opcode("ZC_MENU_LIST") }

// CZ_CHOOSE_MENU (0x00b8) — client selects a menu option.
// rathena/src/map/clif_packetdb.hpp:62 (`parseable_packet(0x00b8,7,clif_parse_NpcSelectMenu,2,6)`).
// Fixed 7 bytes: [2:cmd][4:NpcID][1:selected byte].
// HeaderCZCHOOSEMENU is the source pack's own opcode for this packet.
func HeaderCZCHOOSEMENU() uint16 {
	return opcodeForHandler("clif_parse_NpcSelectMenu", sizeCZChooseMenu(), []int{2, 6})
}

// ZC_OPEN_EDITDLG (0x0142) — server opens the numeric-input dialog window.
// rathena/src/map/packets.hpp:769 PACKET_ZC_OPEN_EDITDLG.
// Fixed 6 bytes: [2:cmd][4:NpcID].
// HeaderZCOPENEDITDLG is the source pack's own opcode for this packet.
func HeaderZCOPENEDITDLG() uint16 { return opcode("ZC_OPEN_EDITDLG") }

// ZC_OPEN_EDITDLGSTR (0x01d4) — server opens the string-input dialog window.
// rathena/src/map/packets.hpp:775 PACKET_ZC_OPEN_EDITDLGSTR.
// Fixed 6 bytes: [2:cmd][4:NpcID] (same layout as ZC_OPEN_EDITDLG).
// HeaderZCOPENEDITDLGSTR is the source pack's own opcode for this packet.
func HeaderZCOPENEDITDLGSTR() uint16 { return opcode("ZC_OPEN_EDITDLGSTR") }

// CZ_INPUT_EDITDLG (0x0143) — client sends a numeric input value.
// rathena/src/map/clif_packetdb.hpp:134
// (`parseable_packet(HEADER_CZ_INPUT_EDITDLG, sizeof(PACKET_CZ_INPUT_EDITDLG), clif_parse_NpcAmountInput, 0)`).
// Fixed 10 bytes: [2:cmd][4:GID/NpcID][4:int32 value].
// HeaderCZINPUTEDITDLG is the source pack's own opcode for this packet.
func HeaderCZINPUTEDITDLG() uint16 { return opcode("CZ_INPUT_EDITDLG") }

// CZ_INPUT_EDITDLGSTR (0x01d5) — client sends a string input value.
// rathena/src/map/clif_packetdb.hpp:225
// (`parseable_packet(HEADER_CZ_INPUT_EDITDLGSTR, -1, clif_parse_NpcStringInput, 0)`).
// Variable length: [2:cmd][2:packetLength][4:GID/NpcID][n:value + NUL].
// HeaderCZINPUTEDITDLGSTR is the source pack's own opcode for this packet.
func HeaderCZINPUTEDITDLGSTR() uint16 { return opcode("CZ_INPUT_EDITDLGSTR") }

// M16: NPC shop interaction. CZ_ACK_SELECT_DEALTYPE / ZC_SELECT_DEALTYPE
// carry the deal-type selection (Buy / Sell / Cancel) that follows
// CZ_CONTACTNPC for shop-type NPCs. CZ_PC_PURCHASE_ITEMLIST /
// ZC_PC_PURCHASE_ITEMLIST carry the buy item list and the player's
// purchase request. ZC_PC_PURCHASE_RESULT acknowledges the purchase
// outcome (success / not enough zeny / not enough slots / overweight).
// Sources cited per constant; the modern pre-Renewal / Renewal shop
// opcodes are in rathena/src/map/clif_packetdb.hpp.
// HeaderCZACKSELECTDEALTYPE is the source pack's own opcode for this packet.
func HeaderCZACKSELECTDEALTYPE() uint16 { return opcode("CZ_ACK_SELECT_DEALTYPE") }

// HeaderCZPCPURCHASEITEMLIST is the source pack's own opcode for this packet.
func HeaderCZPCPURCHASEITEMLIST() uint16 {
	return opcodeForHandler("clif_parse_NpcBuyListSend", -1, []int{2, 4})
}

// HeaderZCSELECTDEALTYPE is the source pack's own opcode for this packet.
func HeaderZCSELECTDEALTYPE() uint16 { return opcode("ZC_SELECT_DEALTYPE") }

// HeaderZCPCPURCHASEITEMLIST is the source pack's own opcode for this packet.
func HeaderZCPCPURCHASEITEMLIST() uint16 { return opcode("ZC_PC_PURCHASE_ITEMLIST") }

// HeaderZCPCPURCHASERESULT is the source pack's own opcode for this packet.
func HeaderZCPCPURCHASERESULT() uint16 { return opcode("ZC_PC_PURCHASE_RESULT") }

// P2B: shop sell flow. CZ_PC_SELL_ITEMLIST (0x00c9, C→S) carries
// the player's sell list (per-entry inventory index + amount);
// ZC_PC_SELL_ITEMLIST (0x00c7, S→C) is the server's priced list
// of every item the player currently owns at sell-list time
// (per-entry index + sell price + overcharge price), and
// ZC_PC_SELL_RESULT (0x00cb, S→C) is the per-transaction ack.
// rathena/src/map/clif_packetdb.hpp + clif.cpp:buy_sell_selection /
// clif_parse_PurchaseItem / clif_purchaseitemlist for shape.
// HeaderCZPCSELLITEMLIST is the source pack's own opcode for this packet.
func HeaderCZPCSELLITEMLIST() uint16 { return opcode("CZ_PC_SELL_ITEMLIST") }

// HeaderZCPCSELLITEMLIST is the source pack's own opcode for this packet.
func HeaderZCPCSELLITEMLIST() uint16 { return opcode("ZC_PC_SELL_ITEMLIST") }

// HeaderZCPCSELLRESULT is the source pack's own opcode for this packet.
func HeaderZCPCSELLRESULT() uint16 { return opcode("ZC_PC_SELL_RESULT") }

// HeaderZCSTATUS is the source pack's own opcode for this packet.
func HeaderZCSTATUS() uint16 { return opcode("ZC_STATUS") }

// HeaderZCPARCHANGE is the source pack's own opcode for this packet.
func HeaderZCPARCHANGE() uint16 { return opcode("ZC_PAR_CHANGE") }

// HeaderZCLONGPARCHANGE is the source pack's own opcode for this packet.
func HeaderZCLONGPARCHANGE() uint16 { return opcode("ZC_LONGPAR_CHANGE") }

// HeaderZCLONGLONGPARCHANGE is the source pack's own opcode for this packet.
func HeaderZCLONGLONGPARCHANGE() uint16 { return opcode("ZC_LONGLONGPAR_CHANGE") }

// TODO(B1): these opcodes + the 5-byte invType header resolve
// per-PACKETVER from the inventorylistnormalType /
// inventorylistequipType aliases (clif_packetdb.hpp) once the
// PacketRegistry resolves symbolic *Type entries (packetdb N1.1).
// The values below are the resolved MAIN@20250604 forms.
// HeaderZCINVENTORYITEMLISTNORMAL is the source pack's own opcode for this packet.
func HeaderZCINVENTORYITEMLISTNORMAL() uint16 { return opcode("storageListNormalType") }

// HeaderZCINVENTORYITEMLISTEQUIP is the source pack's own opcode for this packet.
func HeaderZCINVENTORYITEMLISTEQUIP() uint16 { return opcode("storageListEquipType") }

// Inventory bracket frames that wrap the item lists for
// MAIN_NUM>=20181002 (clif.cpp clif_inventoryStart/End).
// TODO(B1): resolve via PacketRegistry (packetdb N1.1).
// HeaderZCINVENTORYSTART is the source pack's own opcode for this packet.
func HeaderZCINVENTORYSTART() uint16 { return opcode("ZC_INVENTORY_START") }

// HeaderZCINVENTORYEND is the source pack's own opcode for this packet.
func HeaderZCINVENTORYEND() uint16 { return opcode("ZC_INVENTORY_END") }

// HeaderCZMOVEITEMFROMBODYTOSTORE is the source pack's own opcode for the
// client's warehouse deposit: clif_parse_MoveToKafra (0x0364).
//
// The source registers a packet with packetdb_addpacket, which overwrites
// the opcode's whole entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the source's
// RUNTIME table — not the first block of clif_packetdb.hpp — is what the
// client speaks. The helper resolves the runtime registration.
func HeaderCZMOVEITEMFROMBODYTOSTORE() uint16 {
	return opcodeForHandler("clif_parse_MoveToKafra", 8, []int{2, 4})
}

// HeaderCZMOVEITEMFROMSTORETOBODY is the source pack's own opcode for the
// client's warehouse withdrawal: clif_parse_MoveFromKafra (0x0365).
//
// The source registers a packet with packetdb_addpacket, which overwrites
// the opcode's whole entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the source's
// RUNTIME table — not the first block of clif_packetdb.hpp — is what the
// client speaks. The helper resolves the runtime registration.
func HeaderCZMOVEITEMFROMSTORETOBODY() uint16 {
	return opcodeForHandler("clif_parse_MoveFromKafra", 8, []int{2, 4})
}

// HeaderCZCLOSESTORE is the source pack's own opcode for the client's
// warehouse-close request: clif_parse_CloseKafra (0x0193).
//
// The source registers a packet with packetdb_addpacket, which overwrites
// the opcode's whole entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the source's
// RUNTIME table — not the first block of clif_packetdb.hpp — is what the
// client speaks. The helper resolves the runtime registration.
func HeaderCZCLOSESTORE() uint16 {
	return opcodeForHandler("clif_parse_CloseKafra", 2, nil)
}

// HeaderZCSTORECLOSE is the source pack's own opcode for the server's
// warehouse-close notice: ZC_CLOSE_STORE (0xf8).
// third_party/rathenaThailand/src/map/packets.hpp:1032-1035.
func HeaderZCSTORECLOSE() uint16 { return opcode("ZC_CLOSE_STORE") }

// HeaderZCDELETEITEMFROMSTORE is the source pack's own opcode for the
// server's warehouse-row-removal notice: ZC_DELETE_ITEM_FROM_STORE (0xf6).
// third_party/rathenaThailand/src/map/packets.hpp:1037-1042.
func HeaderZCDELETEITEMFROMSTORE() uint16 { return opcode("ZC_DELETE_ITEM_FROM_STORE") }

// HeaderZCNOTIFYSTOREITEMCOUNTINFO is the source pack's own opcode for the
// server's warehouse item-count notice: ZC_NOTIFY_STOREITEM_COUNTINFO (0xf2).
// third_party/rathenaThailand/src/map/packets.hpp:1044-1048.
func HeaderZCNOTIFYSTOREITEMCOUNTINFO() uint16 { return opcode("ZC_NOTIFY_STOREITEM_COUNTINFO") }

// HeaderZCSTOREITEMLISTNORMAL is the source pack's own opcode for the
// warehouse's stackable item list: storageListNormalType (0xb09).
// third_party/rathenaThailand/src/map/packets_struct.hpp:163-166 and
// third_party/rathenaThailand/src/map/packets_struct.hpp:1205-1214.
func HeaderZCSTOREITEMLISTNORMAL() uint16 { return opcode("storageListNormalType") }

// HeaderZCSTOREITEMLISTEQUIP is the source pack's own opcode for the
// warehouse's equipment item list: storageListEquipType (0xb39).
// third_party/rathenaThailand/src/map/packets_struct.hpp:176-179 and
// third_party/rathenaThailand/src/map/packets_struct.hpp:1250-1259.
func HeaderZCSTOREITEMLISTEQUIP() uint16 { return opcode("storageListEquipType") }

// P2A: ZC_REQ_WEAR_EQUIP_ACK_V5 (0x0999) — server ack for
// CZ_REQ_WEAR_EQUIP_V5. rathena/src/map/packets_struct.hpp:1269-1276
// (PACKETVER_MAIN_NUM >= 20121205 branch). Fixed 11 bytes:
// [2:cmd=0x0999][2:inventory index uint16][4:wearLocation uint32]
// [2:wItemSpriteNumber uint16][1:result uint8] (result 0=fail,
// 1=ok, 2=low-level fail per clif.cpp:4306-4309).
// HeaderZCREQWEAREQUIPACKV5 is the source pack's own opcode for this packet.
func HeaderZCREQWEAREQUIPACKV5() uint16 { return opcode("ZC_REQ_WEAR_EQUIP_ACK") }

// P2A: ZC_REQ_TAKEOFF_EQUIP_ACK (0x99a) — server ack for
// CZ_REQ_TAKEOFF_EQUIP. rathena/src/map/packets.hpp:1007-1013
// (PACKETVER >= 20130000 branch, which covers 20250604). Fixed 8
// bytes: [2:cmd=0x99a][2:inventory index uint16]
// [4:wearLocation uint32][1:flag uint8] (inverted for
// PACKETVER >= 20110824 per clif.cpp:4338 — success becomes
// 0 on the wire).
// HeaderZCREQTAKEOFFEQUIPACK is the source pack's own opcode for this packet.
func HeaderZCREQTAKEOFFEQUIPACK() uint16 { return opcode("ZC_REQ_TAKEOFF_EQUIP_ACK") }

// P2A: ZC_SPRITE_CHANGE2 (0x01d7) — look-sprite update broadcast to the actor
// + AOI neighbors on equip/unequip (rAthena clif_sprite_change /
// clif_changelook AREA). Fixed 15 bytes for PACKETVER 20250604
// (PACKETVER_MAIN_NUM>=20181121 branch of packets_struct.hpp:2591):
// [2:cmd=0x01d7][4:GID uint32][1:type uint8][4:val uint32][4:val2 uint32].
// The packetdb static registration is 11 (clif_packetdb.hpp:226) but clif_send
// emits sizeof(struct)=15; see SpriteChangeResponse.
// HeaderZCSPRITECHANGE is the source pack's own opcode for this packet.
func HeaderZCSPRITECHANGE() uint16 { return opcode("sendLookType") }

// P2A: ZC_USE_ITEM_ACK2 (0x01c8) — server ack for
// CZ_USE_ITEM2. rathena/src/map/packets_struct.hpp:312
// (useItemAckType = 0x1c8, PACKETVER >= 3). Fixed 13 bytes:
// [2:cmd=0x01c8][2:index int16][2:itemId uint16][4:AID uint32]
// [2:amount int16][1:result uint8] (PACKETVER 20250604 uses
// uint16 itemId; uint32 is reserved for PACKETVER >=
// 20181121). For PACKETVER 20250604, index is +2 from the
// server-side row index (clif.cpp:4482).
// HeaderZCUSEITEMACK2 is the source pack's own opcode for this packet.
func HeaderZCUSEITEMACK2() uint16 { return opcode("PACKET_ZC_USE_ITEM_ACK") }

// HeaderZCSKILLINFOLIST is the source pack's own opcode for this packet.
func HeaderZCSKILLINFOLIST() uint16 { return opcode("ZC_SKILLINFO_LIST") }

// P3b-2: skill usage family. The 20250604 PACKETVER falls into the
// PACKETVER_RE_NUM >= 20190904 / PACKETVER_MAIN_NUM >= 20190904
// branch of rathena/src/map/clif_shuffle.hpp:4750, which binds
// opcode 0x0438 to clif_parse_UseSkillToId with field offsets 2,4,6
// (the older "to pos" 0x0438 variant is for earlier PACKETVERs and
// shares the same opcode+length but parses 4 fields). Layouts are
// pinned to rathena/src/map/packets_struct.hpp.
// HeaderCZSKILLUP is the source pack's own opcode for this packet.
func HeaderCZSKILLUP() uint16 {
	return opcodeForHandler("clif_parse_SkillUp", SizeCZSkillUp(), []int{2})
}

// HeaderZCSKILLINFOUPDATE is the source pack's own opcode for this packet.
func HeaderZCSKILLINFOUPDATE() uint16 { return opcode("ZC_SKILLINFO_UPDATE") }

// HeaderCZUSESKILL is the source pack's own opcode for this packet.
func HeaderCZUSESKILL() uint16 {
	return opcodeForHandler("clif_parse_UseSkillToId", sizeCZUseSkill2(), []int{2, 4, 6})
}

// HeaderZCNOTIFYSKILL is the source pack's own opcode for this packet.
func HeaderZCNOTIFYSKILL() uint16 { return opcode("ZC_NOTIFY_SKILL") }

// HeaderZCACKTOUSESKILL is the source pack's own opcode for this packet.
func HeaderZCACKTOUSESKILL() uint16 { return opcode("ZC_ACK_TOUSESKILL") }

// CZ_USE_SKILL_TOPOS (0x0AF4) — client casts a ground-target skill.
// rathena/src/map/clif_packetdb.hpp:1905
// (`#if PACKETVER >= 20180207 parseable_packet(0x0AF4,11,clif_parse_UseSkillToPos,2,4,6,8,10)`),
// the last UseSkillToPos binding (no rebind after). Fixed 11 bytes:
// [2:cmd][2:skillLv][2:skillID][2:xPos][2:yPos][1:moreinfo]. The trailing
// moreinfo byte is wire-present but server-ignored (rAthena clif.cpp:13137
// RFIFOB is commented out, passes -1), so goAthena consumes then discards it.
// HeaderCZUSESKILLTOPOS is the source pack's own opcode for this packet.
func HeaderCZUSESKILLTOPOS() uint16 {
	return opcodeForHandler("clif_parse_UseSkillToPos", 11, []int{2, 4, 6, 8, 10})
}

// ZC_NOTIFY_GROUNDSKILL (0x0117) — server broadcasts a ground skill's
// animation. rathena/src/map/packets_struct.hpp:4696 PACKET_ZC_NOTIFY_GROUNDSKILL.
// Fixed 18 bytes: [2:cmd][2:SKID][4:AID caster][2:level][2:xPos][2:yPos][4:startTime].
// HeaderZCNOTIFYGROUNDSKILL is the source pack's own opcode for this packet.
func HeaderZCNOTIFYGROUNDSKILL() uint16 { return opcode("ZC_NOTIFY_GROUNDSKILL") }

// P2C: stats & leveling — stat allocation + level-up effect.
// CZ_STATUS_CHANGE (0x00bb) is the client request to raise a base
// stat (rathena/src/map/clif.cpp:12714 clif_parse_StatusChange).
// HeaderCZSTATUSCHANGE is the source pack's own opcode for this packet.
func HeaderCZSTATUSCHANGE() uint16 {
	return opcodeForHandler("clif_parse_StatusUp", sizeCZStatusChange(), []int{2, 4})
}

// HeaderZCSTATUSCHANGEACK is the source pack's own opcode for this packet.
func HeaderZCSTATUSCHANGEACK() uint16 { return opcode("ZC_STATUS_CHANGE_ACK") }

// HeaderZCNOTIFYEFFECT is the source pack's own opcode for this packet.
func HeaderZCNOTIFYEFFECT() uint16 { return opcode("ZC_NOTIFY_EFFECT") }

// TODO(B1): opcode + the 38-slot/rotate/tab struct resolve
// per-PACKETVER from HEADER_ZC_SHORTCUT_KEY_LIST once the
// PacketRegistry resolves symbolic entries (packetdb N1.1). The
// value below is the resolved MAIN@20250604 form
// (PACKETVER_MAIN_NUM >= 20190522, 38 slots).
// HeaderZCSHORTCUTKEYLIST is the source pack's own opcode for this packet.
func HeaderZCSHORTCUTKEYLIST() uint16 { return opcode("ZC_SHORTCUT_KEY_LIST") }

// ZC_NOTIFY_ACT (0x08c8) — damage / action notification. rathena/src/map/
// packets.hpp:1426 (PACKETVER >= 20131223). Fixed 34 bytes.
// HeaderZCNOTIFYACT is the source pack's own opcode for this packet.
func HeaderZCNOTIFYACT() uint16 { return opcode("ZC_NOTIFY_ACT") }

// ZC_NOTIFY_VANISH (0x0080) — entity vanish notification. rathena/src/map/
// packets.hpp:609. Fixed 7 bytes.
// HeaderZCNOTIFYVANISH is the source pack's own opcode for this packet.
func HeaderZCNOTIFYVANISH() uint16 { return opcode("ZC_NOTIFY_VANISH") }

// A4: CZ drop-item opcodes. rAthena registers seven modern DropItem
// aliases (clif_packetdb.hpp:1385-1606), all sharing the 6-byte
// <index>.W <amount>.W layout; which one a given client sends is
// build-specific. The gateway accepts the five FREE aliases below; 0x0438 is
// excluded (clif_shuffle.hpp:4750 re-binds it to CZ_USE_SKILL2), and 0x0362
// is the modern pickup opcode (see HeaderCZITEMTAKE0362()), NOT a drop alias.
// HeaderCZDROPITEM0363 is the source pack's own opcode for this packet.
func HeaderCZDROPITEM0363() uint16 {
	return opcodeForHandler("clif_parse_DropItem", sizeCZDropItem(), []int{2, 4})
}

// HeaderCZDROPITEM0885 is the source pack's own opcode for this packet.
func HeaderCZDROPITEM0885() uint16 {
	return opcodeForHandler("clif_parse_HomMenu", 5, []int{2, 4})
}

// HeaderCZDROPITEM02C4 is the source pack's own opcode for this packet.
func HeaderCZDROPITEM02C4() uint16 { return opcode("CZ_PARTY_JOIN_REQ") }

// HeaderCZDROPITEM0891 is the source pack's own opcode for this packet.
func HeaderCZDROPITEM0891() uint16 {
	return opcodeForHandler("clif_parse_DropItem", sizeCZDropItem(), []int{2, 4})
}

// HeaderCZDROPITEM089E is the source pack's own opcode for this packet.
func HeaderCZDROPITEM089E() uint16 {
	return opcodeForHandler("clif_parse_DropItem", sizeCZDropItem(), []int{2, 4})
}

// SP_* status parameter IDs from rathena/src/map/map.hpp:498-505.
// Used as the varID field in ZC_PAR_CHANGE / ZC_LONGPAR_CHANGE packets.
const (
	SPSpeed       uint16 = 0  // SP_SPEED
	SPBaseExp     uint16 = 1  // SP_BASEEXP
	SPJobExp      uint16 = 2  // SP_JOBEXP
	SPKarma       uint16 = 3  // SP_KARMA
	SPManner      uint16 = 4  // SP_MANNER
	SPHP          uint16 = 5  // SP_HP
	SPMaxHP       uint16 = 6  // SP_MAXHP
	SPSP          uint16 = 7  // SP_SP
	SPMaxSP       uint16 = 8  // SP_MAXSP
	SPStatusPoint uint16 = 9  // SP_STATUSPOINT
	SPBaseLevel   uint16 = 11 // SP_BASELEVEL
	SPSkillPoint  uint16 = 12 // SP_SKILLPOINT
	SPStr         uint16 = 13 // SP_STR
	SPAgi         uint16 = 14 // SP_AGI
	SPVit         uint16 = 15 // SP_VIT
	SPInt         uint16 = 16 // SP_INT
	SPDex         uint16 = 17 // SP_DEX
	SPLuk         uint16 = 18 // SP_LUK
	SPZeny        uint16 = 20 // SP_ZENY
	SPWeight      uint16 = 24 // SP_WEIGHT
	SPMaxWeight   uint16 = 25 // SP_MAXWEIGHT
	SPJobLevel    uint16 = 55 // SP_JOBLEVEL
)

// Fixed on-wire byte lengths derived from the packed struct layouts in
// rathena/src/map/clif.cpp (CZ_*, parsed from the per-packet comment) and
// rathena/src/map/packets.hpp (ZC_*).
const (
	// sizeZCSpawnUnit = uint16 packetType + uint16 packetLength +
	// uint8 objectType + uint32 AID + uint32 GID + int16 speed + int16 bodyState
	// + int16 healthState + int32 effectState + int16 job + uint16 head
	// + uint32 weapon + uint32 shield + uint16 accessory + uint16 accessory2
	// + uint16 accessory3 + int16 headPalette + int16 bodyPalette + int16 headDir
	// + uint16 robe + uint32 GUID + int16 GEmblemVer + int16 honor
	// + int32 virtue + uint8 isPKModeON + uint8 sex + uint8 posDir[3]
	// + uint8 xSize + uint8 ySize + int16 clevel + int16 font
	// + int32 maxHP + int32 HP + uint8 isBoss + int16 body + char name[24]
	// = 2+2+1+4+4+2+2+2+4+2+2+4+4+2+2+2+2+2+2+2+4+2+2+4+1+1+3+1+1+2+2+4+4+1+2+24
	// = 107 (rathena/src/map/packets.hpp ZC_SPAWN_UNIT, PACKETVER >= 20150513).
	sizeZCSpawnUnit = 107
	// sizeZCSetUnitIdle is identical to sizeZCSpawnUnit — ZC_SET_UNIT_IDLE
	// (0x09ff) shares the same struct layout as ZC_SPAWN_UNIT (0x09fe) for
	// PACKETVER 20250604. rAthena's clif_set_unit_idle writes the same
	// packet_idle_unit struct with the same field order and size.
	sizeZCSetUnitIdle = 107
	// sizeZCUnitWalking = uint16 packetType + uint16 packetLength +
	// uint8 objectType + uint32 AID + uint32 GID + int16 speed + int16 bodyState
	// + int16 healthState + int32 effectState + int16 job + uint16 head
	// + uint32 weapon + uint32 shield + uint16 accessory + uint32 moveStartTime
	// + uint16 accessory2 + uint16 accessory3 + int16 headPalette + int16 bodyPalette
	// + int16 headDir + uint16 robe + uint32 GUID + int16 GEmblemVer + int16 honor
	// + int32 virtue + uint8 isPKModeON + uint8 sex + uint8 moveData[6]
	// + uint8 xSize + uint8 ySize + int16 clevel + int16 font
	// + int32 maxHP + int32 HP + uint8 isBoss + int16 body + char name[24]
	// = 2+2+1+4+4+2+2+2+4+2+2+4+4+2+4+2+2+2+2+2+2+4+2+2+4+1+1+6+1+1+2+2+4+4+1+2+24
	// = 114 (rathena/src/map/packets_struct.hpp:758-830, PACKETVER >= 20150513
	// branch). Compared to ZC_SPAWN_UNIT the moveStartTime field is inserted
	// after accessory and the single posDir[3] slot is replaced by a 6-byte
	// src+dest pair (total +7 bytes: 107 → 114).
	sizeZCUnitWalking = 114
	// sizeSpawnUnitName is the on-wire name field width in
	// ZC_SPAWN_UNIT (rathena/src/map/packets.hpp ZC_SPAWN_UNIT::name).
	sizeSpawnUnitName = 24
	// sizeEmptyInventoryList = int16 packetType + int16 packetLength = 2+2 = 4.
	// Used for ZC_SKILLINFO_LIST when the list is empty (count=0). The
	// trailing SKILLDATA flexible array is omitted entirely; rAthena's
	// clif_skillinfoblock (~:5694) writes only the header in that case.
	//
	// The inventory normal/equip lists NO LONGER share this constant:
	// for MAIN@20250604 they carry a trailing uint8 invType (5-byte
	// header — see sizeEmptyInventoryListNormal). And rAthena
	// suppresses their empty frames entirely (clif_inventorylist guards
	// on `if (normal)` / `if (equip)`), so the empty-inventory path
	// emits only the START/END bracket, never an empty list frame.
	sizeEmptyInventoryList = 4
	// sizeEmptyInventoryListNormal = int16 packetType + int16
	// packetLength + uint8 invType = 2+2+1 = 5 for MAIN@20250604
	// (packet_itemlist_normal carries invType when MAIN_NUM>=20181002).
	// TODO(B1): resolve per-PACKETVER via PacketRegistry (packetdb N1.1).
	sizeEmptyInventoryListNormal = 5
	// sizeStorageListHeader = int16 packetType + int16 packetLength +
	// uint8 invType = 2+2+1 = 5 for ZC_STORE_ITEMLIST_NORMAL and
	// ZC_STORE_ITEMLIST_EQUIP. The warehouse list packets DO carry the
	// invType byte: PACKETVER_RE_NUM >= 20180912 selects it
	// (third_party/rathenaThailand/src/map/packets_struct.hpp:1205-1214 and
	// :1250-1259).
	sizeStorageListHeader = 5
	// sizeZCAckReqName = int16 packetType + int32 GID + char name[24] = 2+4+24 = 30
	// (rathena/src/map/packets_struct.hpp:3556-3560).
	sizeZCAckReqName = 30
	// sizeZCAckReqNameName is the on-wire name field width in ZC_ACK_REQNAME
	// (rathena/src/common/mmo.hpp:154 — NAME_LENGTH = 23+1 = 24).
	sizeZCAckReqNameName = 24
	// npcInputStrMax is rAthena's CHATBOX_SIZE (map.hpp:286, 70+1): the inputstr
	// value buffer. clif_parse_NpcStringInput safestrncpy's the packet value into
	// a buffer of this size (clif.cpp:13412), so a value longer than
	// npcInputStrMax-1 visible bytes is truncated. ParseCZInputEditDlgStr mirrors
	// that cap for parity and to bound a malicious oversized frame.
	npcInputStrMax = 71
	// sizeShopBuyItem is the per-item size in ZC_PC_PURCHASE_ITEMLIST:
	// uint32 itemId + uint32 price + uint32 discountPrice +
	// uint8 itemType + uint16 viewSprite + uint32 location = 4+4+4+1+2+4 = 19
	// (rathena/src/map/packets_struct.hpp: PACKET_ZC_PC_PURCHASE_ITEMLIST
	// / ITEM_INFO entry, PACKETVER >= 20210203).
	sizeShopBuyItem = 19
	// sizeShopBuyEntry is the per-entry size in CZ_PC_PURCHASE_ITEMLIST:
	// uint32 itemId + uint16 amount = 4+2 = 6 (rathena/src/map/
	// clif_packetdb.hpp — CZ_PC_PURCHASE_ITEMLIST).
	sizeShopBuyEntry = 6
	// sizeNormalItem is the per-item size of NORMALITEM_INFO for
	// PACKETVER 20250604 (rathena/src/map/packets_struct.hpp:418-448):
	// int16 index + uint16 ITID + uint8 type + int16 count +
	// uint32 WearState + EQUIPSLOTINFO(8) + int32 HireExpireDate +
	// uint16 bindOnEquipType + Flag(1) = 2+2+1+2+4+8+4+2+1 = 26.
	sizeNormalItem = 26
	// sizeEquipItem is the per-item size of EQUIPITEM_INFO for
	// PACKETVER 20250604 (rathena/src/map/packets_struct.hpp:457-507):
	// int16 index + uint16 ITID + uint8 type + uint32 location +
	// uint32 WearState + uint8 RefiningLevel + EQUIPSLOTINFO(8) +
	// int32 HireExpireDate + uint16 bindOnEquipType +
	// uint16 wItemSpriteNumber + uint8 option_count +
	// 5*ItemOptions(25) + Flag(1) = 2+2+1+4+4+1+8+4+2+2+1+25+1 = 57.
	sizeEquipItem = 57
)

// sizeZCAcceptEnter() = int16 packetType + uint32 startTime +
// uint8 posDir[3] + uint8 xSize + uint8 ySize + uint16 font =
// 2+4+3+1+1+2 = 13 (rathena/src/map/packets.hpp:562-571).
// sizeZCAcceptEnter is the source pack's own length for this packet.
func sizeZCAcceptEnter() int { return layout("PACKET_ZC_ACCEPT_ENTER") }

// sizeZCRefuseEnter() = int16 packetType + uint8 errorCode = 2+1 = 3
// (rathena/src/map/packets.hpp:585-589, static_assert at :589).
// sizeZCRefuseEnter is the source pack's own length for this packet.
func sizeZCRefuseEnter() int { return layout("PACKET_ZC_REFUSE_ENTER") }

// sizeZCNPCAckServerMove() = int16 packetType + char mapName[24] +
// uint16 xPos + uint16 yPos + uint32 ip + uint16 port + char domain[128]
// = 2+24+2+2+4+2+128 = 156 (rathena/src/map/packets.hpp:706-715,
// PACKET_ZC_NPCACK_SERVERMOVE at PACKETVER >= 20170315; the packed struct
// has no padding). MAP_NAME_LENGTH_EXT is 24, not the 16 the pre-2017
// name slot used.
// sizeZCNPCAckServerMove is the source pack's own length for this packet.
func sizeZCNPCAckServerMove() int { return layout("PACKET_ZC_NPCACK_SERVERMOVE") }

// sizeZCNPCAckMapMove() = int16 packetType + char mapName[16] + uint16 xPos +
// uint16 yPos = 2+16+2+2 = 22 (rathena/src/map/packets.hpp
// PACKET_ZC_NPCACK_MAPMOVE).
// sizeZCNPCAckMapMove is the source pack's own length for this packet.
func sizeZCNPCAckMapMove() int { return layout("PACKET_ZC_NPCACK_MAPMOVE") }

// sizeCZEnter() = int16 packetType + uint32 AID + uint32 CID +
// uint32 authCode + uint32 clientTime + uint8 sex = 2+4+4+4+4+1 = 19
// (rathena/src/map/clif.cpp:10642 + the WantToConnection handler
// reading RFIFO* at the documented offsets).
// sizeCZEnter is the source pack's own length for this packet.
func sizeCZEnter() int { return packetLengthOf(2184) }

// sizeCZRequestMove() = int16 packetType + uint8 dest[3] = 2+3 = 5
// (rathena/src/map/clif.cpp:11374; the WalkToXY handler calls RFIFOPOS
// at packet_db[..].pos[0], which is at offset 2 right after the cmd).
// sizeCZRequestMove is the source pack's own length for this packet.
func sizeCZRequestMove() int { return packetLengthOf(863) }

// sizeZCNotifyPlayerMove() = int16 packetType + uint32 moveStartTime +
// uint8 srcPos[3] + uint8 destPos[3] = 2+4+3+3 = 12
// (rathena/src/map/packets.hpp ZC_NOTIFY_PLAYERMOVE).
// sizeZCNotifyPlayerMove is the source pack's own length for this packet.
func sizeZCNotifyPlayerMove() int { return layout("PACKET_ZC_NOTIFY_PLAYERMOVE") }

// sizeCZNotifyActorInit() = int16 packetType = 2 (cmd-only packet, no payload).
// rathena/src/map/clif.cpp:10744-10746.
// sizeCZNotifyActorInit is the source pack's own length for this packet.
func sizeCZNotifyActorInit() int { return packetLengthOf(125) }

// sizeCZRequestTime() = int16 packetType + uint32 clientTick = 2+4 = 6
// (rathena/src/map/clif.cpp:11198-11206).
// sizeCZRequestTime is the source pack's own length for this packet.
func sizeCZRequestTime() int { return packetLengthOf(864) }

// sizeZCMapPropertyR2() = int16 packetType + int16 propertyType + uint32 flags = 2+2+4 = 8
// (rathena/src/map/clif.cpp:6869-6902, PACKETVER >= 20121010 branch).
// sizeZCMapPropertyR2 is the source pack's own length for this packet.
func sizeZCMapPropertyR2() int { return packetLengthOf(2459) }

// sizeZCNotifyTime() = int16 packetType + uint32 time = 2+4 = 6
// (rathena/src/map/clif.cpp:11186-11193).
// sizeZCNotifyTime is the source pack's own length for this packet.
func sizeZCNotifyTime() int { return layout("PACKET_ZC_NOTIFY_TIME") }

// sizeZCStatus() = int16 packetType + uint16 point + 6*(uint8 stat +
// uint8 need) + 12*int16 derived = 2+2+12+24 = 44
// (rathena/src/map/packets.hpp:909-938).
// sizeZCStatus is the source pack's own length for this packet.
func sizeZCStatus() int { return layout("PACKET_ZC_STATUS") }

// sizeZCParChange() = int16 packetType + uint16 varID + int32 count = 2+2+4 = 8
// (rathena/src/map/packets_struct.hpp:354-358).
// sizeZCParChange is the source pack's own length for this packet.
func sizeZCParChange() int { return layout("PACKET_ZC_PAR_CHANGE") }

// sizeZCLongParChange() = int16 packetType + uint16 varID + int32 amount = 2+2+4 = 8
// (rathena/src/map/packets_struct.hpp:361-365).
// sizeZCLongParChange is the source pack's own length for this packet.
func sizeZCLongParChange() int { return layout("PACKET_ZC_LONGPAR_CHANGE") }

// sizeZCLongLongParChange() = int16 packetType + uint16 varID + int64 amount = 2+2+8 = 12
// (rathena/src/map/packets_struct.hpp:399-404, ZC_LONGLONGPAR_CHANGE). PACKETVER
// >= 20170830 routes SP_BASEEXP/SP_JOBEXP/SP_NEXTBASEEXP/SP_NEXTJOBEXP through
// this 64-bit variant instead of ZC_LONGPAR_CHANGE (clif.cpp:3735-3747).
// sizeZCLongLongParChange is the source pack's own length for this packet.
func sizeZCLongLongParChange() int { return layout("PACKET_ZC_LONGLONGPAR_CHANGE") }

// P2C: stat allocation + level-up effect sizes.
// sizeCZStatusChange() = int16 packetType + uint16 statusID + uint8 amount = 2+2+1 = 5
// (rathena/src/map/clif.cpp:12714 clif_parse_StatusChange).
// sizeCZStatusChange is the source pack's own length for this packet.
func sizeCZStatusChange() int { return packetLengthOf(187) }

// sizeZCStatusChangeAck() = int16 packetType + uint16 statusID + uint8 result
// + uint8 value = 2+2+1+1 = 6 (rathena/src/map/clif.cpp:4283).
// sizeZCStatusChangeAck is the source pack's own length for this packet.
func sizeZCStatusChangeAck() int { return layout("PACKET_ZC_STATUS_CHANGE_ACK") }

// sizeZCNotifyEffect() = int16 packetType + uint32 AID + uint32 effectID = 2+4+4 = 10
// (rathena/src/map/packets.hpp:1120 ZC_NOTIFY_EFFECT).
// sizeZCNotifyEffect is the source pack's own length for this packet.
func sizeZCNotifyEffect() int { return layout("PACKET_ZC_NOTIFY_EFFECT") }

// sizeZCShortcutKeyList() = int16 packetType + int8 rotate +
// int16 tab + 38 * hotkey_data = 2 + 1 + 2 + 38*(int8 isSkill +
// uint32 id + int16 count) = 5 + 38*7 = 271
// (rathena/src/map/packets_struct.hpp:1584-1590 — the
// PACKETVER_MAIN_NUM>=20190522 branch that gives opcode 0x0b20 with
// MAX_HOTKEYS_PACKET=38). All 38 slots are zero-filled for a fresh
// character. hotkey_data is declared at
// rathena/src/map/packets_struct.hpp:1576-1580.
// TODO(B1): resolve per-PACKETVER via PacketRegistry (packetdb N1.1).
// sizeZCShortcutKeyList is the source pack's own length for this packet.
func sizeZCShortcutKeyList() int { return layout("PACKET_ZC_SHORTCUT_KEY_LIST") }

// sizeZCInventoryStart() = the minimum on-wire size of a
// ZC_INVENTORY_START frame: int16 packetType + int16 packetLength +
// uint8 invType + char name[1] = 2+2+1+1 = 6. clif_inventoryStart
// always sends at least one NUL name byte (safestrnlen("",24)+1=1),
// so the empty-name frame is 6 bytes with packetLength=6.
// TODO(B1): resolve per-PACKETVER via PacketRegistry (packetdb N1.1).
// sizeZCInventoryStart is the source pack's own length for this packet.
//
// The source's clif_inventoryStart (src/map/clif.cpp:3020-3045) sends the
// struct plus the name's length including its NUL terminator, so the
// frame is one byte longer than the struct for an empty name.
func sizeZCInventoryStart() int { return layout("PACKET_ZC_INVENTORY_START") + 1 }

// sizeZCInventoryEnd() = int16 packetType + uint8 invType + char flag
// = 2+1+1 = 4 (NO length field — clif_inventoryEnd sends sizeof(p)).
// TODO(B1): resolve per-PACKETVER via PacketRegistry (packetdb N1.1).
// sizeZCInventoryEnd is the source pack's own length for this packet.
func sizeZCInventoryEnd() int { return layout("PACKET_ZC_INVENTORY_END") }

// sizeCZActionRequest() = int16 packetType + uint32 targetGID +
// uint8 action = 2+4+1 = 7 (rathena/src/map/clif_packetdb.hpp:38 —
// `parseable_packet(0x0089,7,clif_parse_ActionRequest,2,6)`). The pos
// array [2, 6] documents targetGID at offset 2 and action at offset 6.
// sizeCZActionRequest is the source pack's own length for this packet.
func sizeCZActionRequest() int { return packetLengthOf(1079) }

// sizeZCActionResponse() = int16 packetType + uint32 GID + uint8 action +
// uint32 targetGID = 2+4+1+4 = 11. The wire shape is the compact
// pre-Renewal echo for 0x008b — rAthena's modern clif uses
// ZC_NOTIFY_ACT (0x008a) for the same broadcast but the gateway emits
// 0x008b for the single-player echo path.
// sizeCZChangeDir() = int16 packetType + uint16 headDir + uint8 dir =
// 2+2+1 = 5 (rathena/src/map/clif_packetdb.hpp:48 —
// `parseable_packet(0x009b,5,clif_parse_ChangeDir,2,4)`). The pos
// array [2, 4] documents headDir at offset 2 and dir at offset 4.
// rAthena's clif_parse_ChangeDir reads headDir via RFIFOB (single
// byte) at clif.cpp:11613 — the upper byte of the uint16 is
// effectively reserved on this PACKETVER and ignored by the handler.
// sizeCZChangeDir is the source pack's own length for this packet.
func sizeCZChangeDir() int { return packetLengthOf(865) }

// sizeZCChangeDir() = int16 packetType + uint32 srcId + uint16 headDir +
// uint8 dir = 2+4+2+1 = 9 (rathena/src/map/packets.hpp:688-694).
// sizeZCChangeDir is the source pack's own length for this packet.
func sizeZCChangeDir() int { return layout("PACKET_ZC_CHANGE_DIRECTION") }

// sizeCZReqEmotion() = int16 packetType + uint8 emotion_type = 2+1 = 3
// (rathena/src/map/packets.hpp:1406-1410).
// sizeCZReqEmotion is the source pack's own length for this packet.
func sizeCZReqEmotion() int { return packetLengthOf(191) }

// sizeCZPMIgnore() = int16 packetType + char name[24] + uint8 type = 2+24+1 = 27
// sizeCZPMIgnore is the source pack's own length for this packet.
func sizeCZPMIgnore() int { return packetLengthOf(207) }

// sizeCZSettingWhisperState() = int16 packetType + uint8 type = 2+1 = 3
// sizeCZSettingWhisperState is the source pack's own length for this packet.
func sizeCZSettingWhisperState() int { return packetLengthOf(208) }

// sizeZCSettingWhisperPC() = ZC_SETTING_WHISPER_PC (0x00d1):
// int16 packetType + uint8 type + uint8 result = 2+1+1 = 4
// sizeZCSettingWhisperPC is the source pack's own length for this packet.
func sizeZCSettingWhisperPC() int { return layout("PACKET_ZC_SETTING_WHISPER_PC") }

// sizeZCSettingWhisperState() = ZC_SETTING_WHISPER_STATE (0x00d2): same
// 4-byte layout as 0x00d1 (type + result).
// sizeZCSettingWhisperState is the source pack's own length for this packet.
func sizeZCSettingWhisperState() int { return layout("PACKET_ZC_SETTING_WHISPER_STATE") }

// sizeZCWhisperListName() is the per-entry width in ZC_WHISPER_LIST (0x00d4).
// sizeZCWhisperListName is the source pack's own length for this packet.
func sizeZCWhisperListName() int { return layout("PACKET_ZC_WHISPER_LIST_sub") }

// sizeZCEmotion() = int16 packetType + int32 GID + uint8 type = 2+4+1 = 7
// (rathena/src/map/packets.hpp:1973-1978).
// sizeZCEmotion is the source pack's own length for this packet.
func sizeZCEmotion() int { return layout("PACKET_ZC_EMOTION") }

// sizeZCSpriteChange() = int16 packetType + uint32 GID + uint8 type +
// uint32 val + uint32 val2 = 2+4+1+4+4 = 15 (rathenaThailand/src/map/
// packets_struct.hpp:2591, PACKETVER_MAIN_NUM>=20181121 branch which
// PACKETVER 20250604 selects).
// sizeZCSpriteChange is the source pack's own length for this packet.
func sizeZCSpriteChange() int { return layout("PACKET_ZC_SPRITE_CHANGE") }

// sizeCZGetCharNameRequest() = int16 packetType + int32 GID = 2+4 = 6
// (rathena/src/map/clif_packetdb.hpp:45).
// sizeCZGetCharNameRequest is the source pack's own length for this packet.
func sizeCZGetCharNameRequest() int { return packetLengthOf(872) }

// sizeZCAckReqNameAll2() = ZC_ACK_REQNAMEALL2 (0x0a30): cmd(2)+GID(4)+name(24)+
// party(24)+guild(24)+position(24)+titleID(4) = 106 (packets_struct.hpp:3564-3572).
// sizeZCAckReqNameAll2 is the source pack's own length for this packet.
func sizeZCAckReqNameAll2() int { return layout("PACKET_ZC_ACK_REQNAMEALL") }

// sizeZCAckReqNameAllNPC() = ZC_ACK_REQNAMEALL_NPC (0x0adf): cmd(2)+GID(4)+
// groupId(4)+name(24)+title(24) = 58 (packets_struct.hpp:3587-3593).
// sizeZCAckReqNameAllNPC is the source pack's own length for this packet.
func sizeZCAckReqNameAllNPC() int { return layout("PACKET_ZC_ACK_REQNAMEALL_NPC") }

// sizeZCAckWhisper() = ZC_ACK_WHISPER (0x09df): cmd(2)+result(1)+CID(4) = 7
// (rathena/src/map/packets.hpp:1225-1231).
// sizeZCAckWhisper is the source pack's own length for this packet.
func sizeZCAckWhisper() int { return layout("PACKET_ZC_ACK_WHISPER") }

// sizeZCWhisperName() is the on-wire sender-name field width in ZC_WHISPER
// (NAME_LENGTH = 24, same as ZC_ACK_REQNAME).
// sizeZCWhisperName is the source pack's own length for this packet.
func sizeZCWhisperName() int { return layout("PACKET_ZC_WHISPER_LIST_sub") }

// sizeCZRestart() = int16 packetType + uint8 type = 2+1 = 3
// (rathena/src/map/clif_packetdb.hpp:61).
// sizeCZRestart is the source pack's own length for this packet.
func sizeCZRestart() int { return packetLengthOf(178) }

// sizeZCRestartAck() = int16 packetType + uint8 type = 2+1 = 3
// (rathena/src/map/packets.hpp:598-602 — PACKET_ZC_RESTART_ACK).
// sizeZCRestartAck is the source pack's own length for this packet.
func sizeZCRestartAck() int { return layout("PACKET_ZC_RESTART_ACK") }

// sizeCZContactNPC() = int16 packetType + uint32 AID + uint8 type = 2+4+1 = 7
// (rathena/src/map/clif_packetdb.hpp:42).
// sizeCZContactNPC is the source pack's own length for this packet.
func sizeCZContactNPC() int { return packetLengthOf(144) }

// sizeCZReqNextScript() = int16 packetType + uint32 NpcID = 2+4 = 6
// (rathena/src/map/clif_packetdb.hpp:60).
// sizeCZReqNextScript is the source pack's own length for this packet.
func sizeCZReqNextScript() int { return packetLengthOf(185) }

// sizeCZCloseDialog() = int16 packetType + uint32 GID = 2+4 = 6
// (rathena/src/map/clif_packetdb.hpp:72).
// sizeCZCloseDialog is the source pack's own length for this packet.
func sizeCZCloseDialog() int { return packetLengthOf(326) }

// sizeZCWaitDialog2() = int16 packetType + uint32 NpcID + uint8 type = 2+4+1 = 7
// (rathena/src/map/packets_struct.hpp: ZC_WAIT_DIALOG2).
// sizeZCWaitDialog2 is the source pack's own length for this packet.
func sizeZCWaitDialog2() int { return layout("PACKET_ZC_WAIT_DIALOG2") }

// sizeZCCloseDialog() = int16 packetType + uint32 NpcID = 2+4 = 6
// (rathena/src/map/clif_packetdb.hpp:58).
// sizeZCCloseDialog is the source pack's own length for this packet.
func sizeZCCloseDialog() int { return layout("PACKET_ZC_CLOSE_DIALOG") }

// sizeCZChooseMenu() = int16 packetType + uint32 NpcID + uint8 selected = 2+4+1 = 7
// (rathena/src/map/clif_packetdb.hpp:62).
// sizeCZChooseMenu is the source pack's own length for this packet.
func sizeCZChooseMenu() int { return packetLengthOf(184) }

// sizeZCOpenEditDlg() = int16 packetType + uint32 NpcID = 2+4 = 6
// (rathena/src/map/packets.hpp:769). ZC_OPEN_EDITDLGSTR shares this 6-byte
// layout (packets.hpp:775).
// sizeZCOpenEditDlg is the source pack's own length for this packet.
func sizeZCOpenEditDlg() int { return layout("PACKET_ZC_OPEN_EDITDLG") }

// sizeCZInputEditDlg() = int16 packetType + uint32 GID + int32 value = 2+4+4 = 10
// (rathena/src/map/packets.hpp:1837 PACKET_CZ_INPUT_EDITDLG, fixed).
// sizeCZInputEditDlg is the source pack's own length for this packet.
func sizeCZInputEditDlg() int { return packetLengthOf(323) }

// sizeCZInputEditDlgStrMin() = int16 packetType + uint16 packetLength + uint32 GID = 2+2+4 = 8
// (rathena/src/map/packets.hpp:1844 PACKET_CZ_INPUT_EDITDLGSTR). The header
// size before the variable-length value bytes; rAthena rejects a frame whose
// declared length does not exceed this (clif.cpp:13405).
// sizeCZInputEditDlgStrMin is the source pack's own length for this packet.
func sizeCZInputEditDlgStrMin() int { return layout("PACKET_CZ_INPUT_EDITDLGSTR") }

// sizeZCSelectDealtype() = int16 packetType + uint32 NpcID = 2+4 = 6
// (rathena/src/map/packets.hpp: ZC_SELECT_DEALTYPE).
// sizeZCSelectDealtype is the source pack's own length for this packet.
func sizeZCSelectDealtype() int { return layout("PACKET_ZC_SELECT_DEALTYPE") }

// sizeCZAckSelectDealtype() = int16 packetType + uint32 NpcID +
// uint8 type = 2+4+1 = 7 (rathena/src/map/clif_packetdb.hpp —
// CZ_ACK_SELECT_DEALTYPE).
// sizeCZAckSelectDealtype is the source pack's own length for this packet.
func sizeCZAckSelectDealtype() int { return packetLengthOf(197) }

// sizeZCPCPurchaseResult() = int16 packetType + uint8 result = 2+1 = 3
// (rathena/src/map/packets.hpp: ZC_PC_PURCHASE_RESULT).
// sizeZCPCPurchaseResult is the source pack's own length for this packet.
func sizeZCPCPurchaseResult() int { return packetLengthOf(202) }

// sizeZCPCSellResult() = int16 packetType + uint8 result = 2+1 = 3
// (rathena/src/map/packets.hpp: ZC_PC_SELL_RESULT).
// sizeZCPCSellResult is the source pack's own length for this packet.
func sizeZCPCSellResult() int { return packetLengthOf(203) }

// sizeCZPCSellItemListEntry() is the per-entry size in
// CZ_PC_SELL_ITEMLIST: uint16 index + uint16 amount = 2+2 = 4
// (rathena/src/map/clif_packetdb.hpp — CZ_PC_SELL_ITEMLIST).
// sizeCZPCSellItemListEntry is the source pack's own length for this packet.
func sizeCZPCSellItemListEntry() int { return layout("PACKET_CZ_PC_SELL_ITEMLIST") }

// sizeZCPCSellItemListItem() is the per-item size in
// ZC_PC_SELL_ITEMLIST: uint16 index + uint32 price +
// uint32 overcharge = 2+4+4 = 10
// (rathena/src/map/packets_struct.hpp: ZC_PC_SELL_ITEMLIST
// PACKET_ZC_PC_SELL_ITEMLIST entry).
// sizeZCPCSellItemListItem is the source pack's own length for this packet.
func sizeZCPCSellItemListItem() int { return layout("PACKET_ZC_PC_SELL_ITEMLIST_sub") }

// sizeZCNotifyAct() = int16 packetType + int32 srcID + int32 targetID +
// int32 serverTick + int32 srcSpeed + int32 dmgSpeed + int32 damage +
// int8 isSPDamage + uint16 div + uint8 type + int32 damage2
// = 2+4+4+4+4+4+4+1+2+1+4 = 34 (rathena/src/map/packets.hpp:1413-1425,
// PACKETVER >= 20131223 branch).
// sizeZCNotifyAct is the source pack's own length for this packet.
func sizeZCNotifyAct() int { return layout("PACKET_ZC_NOTIFY_ACT") }

// HeaderZCNotifyAct is the source pack's own opcode for this packet.
//
// The source registers 0x008b as a two-byte packet with no handler
// (src/map/clif_packetdb.hpp:39), so the source's client has no
// ZC_ACTION_RESPONSE to read: the source broadcasts an action as
// PACKET_ZC_NOTIFY_ACT (src/map/packets.hpp:1413), 0x8c8 at PACKETVER
// 20250604.
func HeaderZCNotifyAct() uint16 { return opcode("ZC_NOTIFY_ACT") }

// sizeZCNotifyVanish() = int16 packetType + uint32 gid + uint8 type = 2+4+1 = 7
// (rathena/src/map/packets.hpp:604-608).
// sizeZCNotifyVanish is the source pack's own length for this packet.
func sizeZCNotifyVanish() int { return layout("PACKET_ZC_NOTIFY_VANISH") }

// sizeCZSkillUp = int16 packetType + uint16 skillID = 2+2 = 4
// (rathena/src/map/clif_packetdb.hpp:110, no PACKETVER guard).
// SizeCZSkillUp is the source pack's own length for this packet.
func SizeCZSkillUp() int { return packetLengthOf(274) }

// sizeZC_SKILLINFO_UPDATE = int16 packetType + uint16 skillId + uint16 level +
// uint16 sp + uint16 range2 + uint8 upgradableFlag = 2+2+2+2+2+1 = 11
// (rathena/src/map/packets.hpp:489-497).
// SizeZCSkillInfoUpdate is the source pack's own length for this packet.
func SizeZCSkillInfoUpdate() int { return layout("PACKET_ZC_SKILLINFO_UPDATE") }

// sizeCZUseSkill2() = int16 packetType + int16 skillLv + uint16 skillID +
// uint32 targetID = 2+2+2+4 = 10 (clif_shuffle.hpp:4750 binds
// opcode 0x0438 to length 10 for PACKETVER_RE_NUM >= 20190904).
// sizeCZUseSkill2 is the source pack's own length for this packet.
func sizeCZUseSkill2() int { return packetLengthOf(1080) }

// sizeCZUseSkillToPos() = int16 packetType + int16 skillLv + uint16 skillID +
// uint16 xPos + uint16 yPos + uint8 moreinfo = 2+2+2+2+2+1 = 11
// (rathena/src/map/clif_packetdb.hpp:1905, PACKETVER >= 20180204 branch).
// sizeCZUseSkillToPos is the source pack's own length for this packet.
func sizeCZUseSkillToPos() int { return packetLengthOf(870) }

// sizeZCNotifyGroundSkill() = int16 packetType + uint16 SKID + uint32 AID +
// int16 level + int16 xPos + int16 yPos + uint32 startTime = 2+2+4+2+2+2+4 = 18
// (rathena/src/map/packets_struct.hpp:4696).
// sizeZCNotifyGroundSkill is the source pack's own length for this packet.
func sizeZCNotifyGroundSkill() int { return layout("PACKET_ZC_NOTIFY_GROUNDSKILL") }

// sizeZCNotifySkill() = int16 packetType + uint16 SKID + uint32 AID +
// uint32 targetID + uint32 startTime + int32 attackMT +
// int32 attackedMT + int32 damage + int16 level + int16 count +
// int8 action = 2+2+4+4+4+4+4+4+2+2+1 = 33
// (rathena/src/map/packets_struct.hpp:4658-4671, PACKETVER >= 3).
// sizeZCNotifySkill is the source pack's own length for this packet.
func sizeZCNotifySkill() int { return packetLengthOf(478) }

// sizeZCAckToUseSkill() = int16 packetType + uint16 skillId + int32 btype +
// uint32 itemId + uint8 flag + uint8 cause = 2+2+4+4+1+1 = 14
// (rathena/src/map/packets_struct.hpp:2448-2460, PACKETVER_MAIN_NUM
// >= 20181121 branch — 20250604 satisfies it).
// sizeZCAckToUseSkill is the source pack's own length for this packet.
func sizeZCAckToUseSkill() int { return layout("PACKET_ZC_ACK_TOUSESKILL") }

// sizeCZUseItem2() = int16 packetType + uint16 index + uint32 AID = 2+2+4 = 8
// (rathena/src/map/clif_packetdb.hpp:1151).
// sizeCZUseItem2 is the source pack's own length for this packet.
func sizeCZUseItem2() int { return packetLengthOf(1081) }

// sizeCZItemPickup() = int16 packetType + uint32 map_object_id = 2+4 = 6
// (rathena/src/map/clif_packetdb.hpp:50).
// sizeCZItemPickup is the source pack's own length for this packet.
func sizeCZItemPickup() int { return packetLengthOf(866) }

// sizeCZDropItem() = int16 packetType + uint16 index + uint16 amount = 2+2+2 = 6.
// Shared by every modern DropItem opcode band (clif_packetdb.hpp:1385-1606).
// sizeCZDropItem is the source pack's own length for this packet.
func sizeCZDropItem() int { return packetLengthOf(867) }

// sizeCZReqWearEquipV5() = int16 packetType + uint16 index + uint32 position = 2+2+4 = 8
// (rathena/src/map/packets.hpp:1504-1509).
// sizeCZReqWearEquipV5 is the source pack's own length for this packet.
func sizeCZReqWearEquipV5() int { return packetLengthOf(2456) }

// sizeCZReqTakeoffEquip() = int16 packetType + uint16 index = 2+2 = 4
// (rathena/src/map/clif_packetdb.hpp:59).
// sizeCZReqTakeoffEquip is the source pack's own length for this packet.
func sizeCZReqTakeoffEquip() int { return packetLengthOf(171) }

// sizeZCReqWearEquipAckV5() = int16 packetType + uint16 index + uint32 wearLocation +
// uint16 wItemSpriteNumber + uint8 result = 2+2+4+2+1 = 11
// (rathena/src/map/packets_struct.hpp:1269-1276).
// sizeZCReqWearEquipAckV5 is the source pack's own length for this packet.
func sizeZCReqWearEquipAckV5() int { return layout("PACKET_ZC_REQ_WEAR_EQUIP_ACK") }

// sizeZCReqTakeoffEquipAck() = int16 packetType + uint16 index + uint32 wearLocation +
// uint8 flag = 2+2+4+1 = 9
// (rathena/src/map/packets.hpp:1007-1013).
// sizeZCReqTakeoffEquipAck is the source pack's own length for this packet.
func sizeZCReqTakeoffEquipAck() int { return layout("PACKET_ZC_REQ_TAKEOFF_EQUIP_ACK") }

// sizeZCUseItemAck2() = int16 packetType + int16 index + uint16 itemId +
// uint32 AID + int16 amount + uint8 result = 2+2+2+4+2+1 = 13
// (rathena/src/map/packets_struct.hpp:2577-2589, PACKETVER 20250604 branch).
// sizeZCUseItemAck2 is the source pack's own length for this packet.
func sizeZCUseItemAck2() int { return packetLengthOf(456) }

// Damage / action type constants from rathena/src/map/clif.hpp:691-707.
// Used as the `type` field in ZC_NOTIFY_ACT and the `action` byte in
// CZ_ACTION_REQUEST (0x0089).
const (
	DMGNormal  uint8 = 0 // DMG_NORMAL — single attack
	DMGPickup  uint8 = 1 // DMG_PICKUP_ITEM — pick up item
	DMGSitDown uint8 = 2 // DMG_SIT_DOWN — sit down
	DMGStandUp uint8 = 3 // DMG_STAND_UP — stand up
	DMGRepeat  uint8 = 7 // DMG_REPEAT — continuous attack
)

// Vanish type constants from rathena/src/map/clif.hpp:358-361.
// Used as the `type` field in ZC_NOTIFY_VANISH (0x0080).
const (
	VanishOutsight uint8 = 0 // CLR_OUTSIGHT
	VanishDead     uint8 = 1 // CLR_DEAD
	VanishRespawn  uint8 = 2 // CLR_RESPAWN
	VanishTeleport uint8 = 3 // CLR_TELEPORT
)

// NewMapServerDB returns a packet database pre-populated with all known
// map-server packet definitions for Thai Classic (PACKETVER 20250604).
//
// Outbound (S→C) entries register both the small fixed packets we encode
// here (ZC_REFUSE_ENTER, ZC_ACCEPT_ENTER) and the inbound (C→S) parsers.
// Unlike the login and char databases, the map server has no single large
// accept packet; the connection sequence is a short CZ_ENTER exchange
// followed by the bulk packet stream (movement, chat, skills, etc.) which
// land in M3b+ as their own packet tables.
//
// All on-wire sizes and opcode IDs are sourced from rathena/src/map/
// packets.hpp (ZC_*) and rathena/src/map/clif.cpp (CZ_*).
func NewMapServerDB() *DB {
	db := NewDB()

	// --- C→S: client → map server.
	db.Register(Definition{
		ID:        HeaderCZENTER(),
		Name:      "CZ_ENTER",
		Length:    sizeCZEnter(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQUESTMOVE(),
		Name:      "CZ_REQUEST_MOVE",
		Length:    sizeCZRequestMove(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZNOTIFYACTORINIT(),
		Name:      "CZ_NOTIFY_ACTORINIT",
		Length:    sizeCZNotifyActorInit(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQUESTTIME(),
		Name:      "CZ_REQUEST_TIME",
		Length:    sizeCZRequestTime(),
		Direction: DirectionClientToServer,
	})
	// M11: action + chat. CZ_ACTION_REQUEST is fixed 7 bytes; CZ_GLOBAL_MESSAGE
	// is variable — the text starts at offset 4 and the wire packetLength
	// slot at [2:4] carries the trailing byte count. See rathena/src/map/
	// clif_packetdb.hpp:38-40 for the canonical entries.
	db.Register(Definition{
		ID:        HeaderCZACTIONREQUEST(),
		Name:      "CZ_ACTION_REQUEST",
		Length:    sizeCZActionRequest(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZGLOBALMESSAGE(),
		Name:      "CZ_GLOBAL_MESSAGE",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	// M12: CZ_CHANGE_DIRECTION (fixed 5 bytes) + CZ_REQ_EMOTION (fixed 3
	// bytes) — basic player expression echo path. See the header
	// constants above for rAthena source pointers.
	db.Register(Definition{
		ID:        HeaderCZCHANGEDIR(),
		Name:      "CZ_CHANGE_DIRECTION",
		Length:    sizeCZChangeDir(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQEMOTION(),
		Name:      "CZ_REQ_EMOTION",
		Length:    sizeCZReqEmotion(),
		Direction: DirectionClientToServer,
	})
	// Phase 41: whisper ignore-list verbs — /ex /in /exall /inall /wl.
	db.Register(Definition{
		ID:        HeaderCZPMIGNORE(),
		Name:      "CZ_PMIgnore",
		Length:    sizeCZPMIgnore(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZSETTINGWHISPERSTATE(),
		Name:      "CZ_SETTING_WHISPER_STATE",
		Length:    sizeCZSettingWhisperState(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQWHISPERLIST(),
		Name:      "CZ_REQ_WHISPER_LIST",
		Length:    2,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCSETTINGWHISPERPC(),
		Name:      "ZC_SETTING_WHISPER_PC",
		Length:    sizeZCSettingWhisperPC(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSETTINGWHISPERSTATE(),
		Name:      "ZC_SETTING_WHISPER_STATE",
		Length:    sizeZCSettingWhisperState(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCWHISPERLIST(),
		Name:      "ZC_WHISPER_LIST",
		Length:    -1, // variable: 4 + 24 per name
		Direction: DirectionServerToClient,
	})
	// M13: CZ_GETCHARNAMEREQUEST (fixed 6 bytes) + CZ_RESTART (fixed 3
	// bytes) — name lookup and respawn/char-select request.
	db.Register(Definition{
		ID:        HeaderCZGETCHARNAMEREQUEST(),
		Name:      "CZ_GETCHARNAMEREQUEST",
		Length:    sizeCZGetCharNameRequest(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZRESTART(),
		Name:      "CZ_RESTART",
		Length:    sizeCZRestart(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZSKILLUP(),
		Name:      "CZ_SKILLUP",
		Length:    SizeCZSkillUp(),
		Direction: DirectionClientToServer,
	})

	// --- S→C: map server → client.
	db.Register(Definition{
		ID:        HeaderZCREFUSEENTER(),
		Name:      "ZC_REFUSE_ENTER",
		Length:    sizeZCRefuseEnter(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCACCEPTENTER(),
		Name:      "ZC_ACCEPT_ENTER",
		Length:    sizeZCAcceptEnter(),
		Direction: DirectionServerToClient,
	})
	// ZC_NPCACK_MAPMOVE (0x0091) — the map-server's cross-map relocation
	// directive, emitted on a warp portal (dispatch.go relocateThroughPortal).
	// It was described by sizeZCNPCAckMapMove() all along but never registered
	// here, so the packet DB — the reference every frame reader resolves
	// against — did not know a frame the server actually sends.
	db.Register(Definition{
		ID:        HeaderZCNPCACKMAPMOVE(),
		Name:      "ZC_NPCACK_MAPMOVE",
		Length:    sizeZCNPCAckMapMove(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNPCACKSERVERMOVE(),
		Name:      "ZC_NPCACK_SERVERMOVE",
		Length:    sizeZCNPCAckServerMove(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYPLAYERMOVE(),
		Name:      "ZC_NOTIFY_PLAYERMOVE",
		Length:    sizeZCNotifyPlayerMove(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSPAWNUNIT(),
		Name:      "ZC_SPAWN_UNIT",
		Length:    sizeZCSpawnUnit,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCMAPPROPERTYR2(),
		Name:      "ZC_MAPPROPERTY_R2",
		Length:    sizeZCMapPropertyR2(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYTIME(),
		Name:      "ZC_NOTIFY_TIME",
		Length:    sizeZCNotifyTime(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSTATUS(),
		Name:      "ZC_STATUS",
		Length:    sizeZCStatus(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPARCHANGE(),
		Name:      "ZC_PAR_CHANGE",
		Length:    sizeZCParChange(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCLONGPARCHANGE(),
		Name:      "ZC_LONGPAR_CHANGE",
		Length:    sizeZCLongParChange(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCLONGLONGPARCHANGE(),
		Name:      "ZC_LONGLONGPAR_CHANGE",
		Length:    sizeZCLongLongParChange(),
		Direction: DirectionServerToClient,
	})
	// M10: list packets emitted after the status burst. The three
	// list packets (inventory normal/equip, skill) are variable-length —
	// the wire length is encoded in the int16 packetLength slot and the
	// trailing flexible array may be zero entries — so we register
	// VariableLength (-1) and rely on the codec to read the wire length
	// at decode time. ZC_SHORTCUT_KEY_LIST (0x0b20) is fixed at 271
	// bytes (2-byte opcode + rotate + tab + 38 zero-filled hotkey slots
	// of 7 bytes each) regardless of how many slots the client actually
	// has configured; the slot count is encoded in the PACKETVER's struct
	// shape, not the wire data.
	// TODO(B1): inventory list opcodes + the 5-byte invType header and
	// the 38-slot hotkey shape resolve per-PACKETVER via PacketRegistry
	// (packetdb N1.1). The MAIN@20250604 forms are registered here.
	db.Register(Definition{
		ID:        HeaderZCINVENTORYSTART(),
		Name:      "ZC_INVENTORY_START",
		Length:    sizeZCInventoryStart(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCINVENTORYITEMLISTNORMAL(),
		Name:      "ZC_INVENTORY_ITEMLIST_NORMAL",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCINVENTORYITEMLISTEQUIP(),
		Name:      "ZC_INVENTORY_ITEMLIST_EQUIP",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCINVENTORYEND(),
		Name:      "ZC_INVENTORY_END",
		Length:    sizeZCInventoryEnd(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSKILLINFOLIST(),
		Name:      "ZC_SKILLINFO_LIST",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSHORTCUTKEYLIST(),
		Name:      "ZC_SHORTCUT_KEY_LIST",
		Length:    sizeZCShortcutKeyList(),
		Direction: DirectionServerToClient,
	})
	// M11: ZC_NOTIFY_CHAT is variable length (chat text + trailing NUL);
	// ZC_ACTION_RESPONSE is fixed 11 bytes (cmd + GID + action + targetGID).
	db.Register(Definition{
		ID:        HeaderZCNOTIFYCHAT(),
		Name:      "ZC_NOTIFY_CHAT",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	// M10 Rung C: ZC_BROADCAST is variable length (announce text + trailing
	// NUL), the frame the announce/mapannounce script builtins emit.
	db.Register(Definition{
		ID:        HeaderZCBROADCAST(),
		Name:      "ZC_BROADCAST",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	// The source has no ZC_ACTION_RESPONSE: it registers 0x008b as a
	// two-byte packet with no handler (src/map/clif_packetdb.hpp:39), so
	// the source's client has no action echo to read. The source
	// broadcasts an action as PACKET_ZC_NOTIFY_ACT
	// (src/map/packets.hpp:1413), which is what the gateway sends.
	db.Register(Definition{
		ID:        HeaderZCNotifyAct(),
		Name:      "ZC_NOTIFY_ACT",
		Length:    sizeZCNotifyAct(),
		Direction: DirectionServerToClient,
	})
	// M12: ZC_CHANGE_DIRECTION (fixed 9 bytes) + ZC_EMOTION (fixed 7 bytes)
	// — direction / emotion single-player echo.
	db.Register(Definition{
		ID:        HeaderZCCHANGEDIR(),
		Name:      "ZC_CHANGE_DIRECTION",
		Length:    sizeZCChangeDir(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCEMOTION(),
		Name:      "ZC_EMOTION",
		Length:    sizeZCEmotion(),
		Direction: DirectionServerToClient,
	})
	// P2A: ZC_SPRITE_CHANGE2 (0x01d7) — look-sprite update broadcast to the actor
	// + AOI neighbors on equip/unequip. 15 bytes for PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderZCSPRITECHANGE(),
		Name:      "ZC_SPRITE_CHANGE",
		Length:    sizeZCSpriteChange(),
		Direction: DirectionServerToClient,
	})
	// M13: ZC_ACK_REQNAME (fixed 30 bytes) — name lookup response.
	db.Register(Definition{
		ID:        HeaderZCACKREQNAME(),
		Name:      "ZC_ACK_REQNAME",
		Length:    sizeZCAckReqName,
		Direction: DirectionServerToClient,
	})
	// ZC_ACK_REQNAMEALL2 (0x0a30, fixed 106 bytes) — full PC name reply emitted by
	// clif_name at PACKETVER >= 20150225 (party/guild/position names + title id).
	db.Register(Definition{
		ID:        HeaderZCACKREQNAMEALL2(),
		Name:      "ZC_ACK_REQNAMEALL2",
		Length:    sizeZCAckReqNameAll2(),
		Direction: DirectionServerToClient,
	})
	// ZC_ACK_REQNAMEALL_NPC (0x0adf, fixed 58 bytes) — NPC/mob name reply emitted
	// by clif_name at PACKETVER >= 20180207 (groupId + name + title).
	db.Register(Definition{
		ID:        HeaderZCACKREQNAMEALLNPC(),
		Name:      "ZC_ACK_REQNAMEALL_NPC",
		Length:    sizeZCAckReqNameAllNPC(),
		Direction: DirectionServerToClient,
	})
	// P2c: CZ_WHISPER (0x0096) — private message request. Variable length;
	// codec bounds by the embedded packetLength slot at offset 2.
	db.Register(Definition{
		ID:        HeaderCZWHISPER(),
		Name:      "CZ_WHISPER",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	// P2c: ZC_WHISPER (0x09de) — delivered whisper to the recipient. Variable
	// length (sender + message); 0x09de branch active at PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderZCWHISPER(),
		Name:      "ZC_WHISPER",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	// P2c: ZC_ACK_WHISPER (0x09df) — whisper result ack to the sender. Fixed 7
	// bytes; 0x09df branch active at PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderZCACKWHISPER(),
		Name:      "ZC_ACK_WHISPER",
		Length:    sizeZCAckWhisper(),
		Direction: DirectionServerToClient,
	})
	// M14: ZC_SET_UNIT_IDLE (fixed 107 bytes) — NPC entity spawn.
	// Same struct layout as ZC_SPAWN_UNIT (0x09fe) but uses opcode
	// 0x09ff per rAthena's clif_set_unit_idle path.
	db.Register(Definition{
		ID:        HeaderZCSETUNITIDLE(),
		Name:      "ZC_SET_UNIT_IDLE",
		Length:    sizeZCSetUnitIdle,
		Direction: DirectionServerToClient,
	})
	// M-phase1: ZC_UNIT_WALKING (fixed 114 bytes) — observer movement
	// broadcast. rAthena's clif_set_unit_walking writes the
	// packet_unit_walking struct (rathena/src/map/packets_struct.hpp:758-830,
	// PACKETVER >= 20150513 branch) and sends it via the area-WIDE path,
	// excluding the moving entity itself. The self-only move ack is
	// ZC_NOTIFY_PLAYERMOVE (0x0087); this packet is the broadcast leg.
	db.Register(Definition{
		ID:        HeaderZCUNITWALKING(),
		Name:      "ZC_UNIT_WALKING",
		Length:    sizeZCUnitWalking,
		Direction: DirectionServerToClient,
	})
	// M15: NPC dialog interaction — CZ_CONTACTNPC (fixed 7 bytes),
	// CZ_REQNEXTSCRIPT (fixed 6 bytes), CZ_CLOSE_DIALOG (fixed 6 bytes).
	db.Register(Definition{
		ID:        HeaderCZCONTACTNPC(),
		Name:      "CZ_CONTACTNPC",
		Length:    sizeCZContactNPC(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQNEXTSCRIPT(),
		Name:      "CZ_REQNEXTSCRIPT",
		Length:    sizeCZReqNextScript(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZCLOSEDIALOG(),
		Name:      "CZ_CLOSE_DIALOG",
		Length:    sizeCZCloseDialog(),
		Direction: DirectionClientToServer,
	})
	// CZ_CHOOSE_MENU — client selects an item from a ZC_MENU_LIST dialog.
	db.Register(Definition{
		ID:        HeaderCZCHOOSEMENU(),
		Name:      "CZ_CHOOSE_MENU",
		Length:    sizeCZChooseMenu(),
		Direction: DirectionClientToServer,
	})
	// M14c: NPC input dialogs. CZ_INPUT_EDITDLG (fixed 10 bytes) carries a
	// numeric value; CZ_INPUT_EDITDLGSTR (variable) carries a string.
	// ZC_OPEN_EDITDLG / ZC_OPEN_EDITDLGSTR (fixed 6 bytes) open the windows.
	db.Register(Definition{
		ID:        HeaderCZINPUTEDITDLG(),
		Name:      "CZ_INPUT_EDITDLG",
		Length:    sizeCZInputEditDlg(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZINPUTEDITDLGSTR(),
		Name:      "CZ_INPUT_EDITDLGSTR",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCOPENEDITDLG(),
		Name:      "ZC_OPEN_EDITDLG",
		Length:    sizeZCOpenEditDlg(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCOPENEDITDLGSTR(),
		Name:      "ZC_OPEN_EDITDLGSTR",
		Length:    sizeZCOpenEditDlg(),
		Direction: DirectionServerToClient,
	})
	// M15: ZC_SAY_DIALOG2 (variable length), ZC_WAIT_DIALOG2 (fixed 7 bytes),
	// ZC_CLOSE_DIALOG (fixed 6 bytes).
	db.Register(Definition{
		ID:        HeaderZCSAYDIALOG2(),
		Name:      "ZC_SAY_DIALOG2",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCWAITDIALOG2(),
		Name:      "ZC_WAIT_DIALOG2",
		Length:    sizeZCWaitDialog2(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCCLOSEDIALOG(),
		Name:      "ZC_CLOSE_DIALOG",
		Length:    sizeZCCloseDialog(),
		Direction: DirectionServerToClient,
	})
	// ZC_MENU_LIST — server sends menu items for the NPC dialog selector.
	db.Register(Definition{
		ID:        HeaderZCMENULIST(),
		Name:      "ZC_MENU_LIST",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	// M16: NPC shop interaction — CZ_ACK_SELECT_DEALTYPE (fixed 7 bytes),
	// CZ_PC_PURCHASE_ITEMLIST (variable length), ZC_SELECT_DEALTYPE
	// (fixed 6 bytes), ZC_PC_PURCHASE_ITEMLIST (variable length),
	// ZC_PC_PURCHASE_RESULT (fixed 3 bytes).
	db.Register(Definition{
		ID:        HeaderCZACKSELECTDEALTYPE(),
		Name:      "CZ_ACK_SELECT_DEALTYPE",
		Length:    sizeCZAckSelectDealtype(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZPCPURCHASEITEMLIST(),
		Name:      "CZ_PC_PURCHASE_ITEMLIST",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCSELECTDEALTYPE(),
		Name:      "ZC_SELECT_DEALTYPE",
		Length:    sizeZCSelectDealtype(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPCPURCHASEITEMLIST(),
		Name:      "ZC_PC_PURCHASE_ITEMLIST",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPCPURCHASERESULT(),
		Name:      "ZC_PC_PURCHASE_RESULT",
		Length:    sizeZCPCPurchaseResult(),
		Direction: DirectionServerToClient,
	})
	// P2B: shop sell flow. CZ_PC_SELL_ITEMLIST (variable, the per-
	// entry size is 4), ZC_PC_SELL_ITEMLIST (variable, per-item 10),
	// ZC_PC_SELL_RESULT (fixed 3). See rathena/src/map/clif_packetdb.hpp.
	db.Register(Definition{
		ID:        HeaderCZPCSELLITEMLIST(),
		Name:      "CZ_PC_SELL_ITEMLIST",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCPCSELLITEMLIST(),
		Name:      "ZC_PC_SELL_ITEMLIST",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPCSELLRESULT(),
		Name:      "ZC_PC_SELL_RESULT",
		Length:    sizeZCPCSellResult(),
		Direction: DirectionServerToClient,
	})
	// M18: ZC_NOTIFY_ACT (fixed 34 bytes) — damage / sit / stand notification.
	// ZC_NOTIFY_VANISH (fixed 7 bytes) — entity death / despawn notification.
	db.Register(Definition{
		ID:        HeaderZCNOTIFYACT(),
		Name:      "ZC_NOTIFY_ACT",
		Length:    sizeZCNotifyAct(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYVANISH(),
		Name:      "ZC_NOTIFY_VANISH",
		Length:    sizeZCNotifyVanish(),
		Direction: DirectionServerToClient,
	})
	// P2A: inventory equip / use packet family — three C→S requests
	// and three S→C acks. See the per-constant source citations above
	// for the rAthena packetdb / packets.hpp / packets_struct.hpp
	// lines that pin each opcode and on-wire size to PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderCZITEMPICKUP(),
		Name:      "CZ_ITEM_PICKUP",
		Length:    sizeCZItemPickup(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZUSEITEM2(),
		Name:      "CZ_USE_ITEM2",
		Length:    sizeCZUseItem2(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQWEAREQUIPV5(),
		Name:      "CZ_REQ_WEAR_EQUIP_V5",
		Length:    sizeCZReqWearEquipV5(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQTAKEOFFEQUIP(),
		Name:      "CZ_REQ_TAKEOFF_EQUIP",
		Length:    sizeCZReqTakeoffEquip(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCREQWEAREQUIPACKV5(),
		Name:      "ZC_REQ_WEAR_EQUIP_ACK_V5",
		Length:    sizeZCReqWearEquipAckV5(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCREQTAKEOFFEQUIPACK(),
		Name:      "ZC_REQ_TAKEOFF_EQUIP_ACK",
		Length:    sizeZCReqTakeoffEquipAck(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCUSEITEMACK2(),
		Name:      "ZC_USE_ITEM_ACK2",
		Length:    sizeZCUseItemAck2(),
		Direction: DirectionServerToClient,
	})
	// P2C: stats & leveling — CZ_STATUS_CHANGE (stat allocation request)
	// plus the two server replies. See the per-constant source citations
	// above (clif.cpp:12714 / clif.cpp:4283 / packets.hpp:1120) for the
	// rAthena packetdb lines pinning each opcode to PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderCZSTATUSCHANGE(),
		Name:      "CZ_STATUS_CHANGE",
		Length:    sizeCZStatusChange(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCSTATUSCHANGEACK(),
		Name:      "ZC_STATUS_CHANGE_ACK",
		Length:    sizeZCStatusChangeAck(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYEFFECT(),
		Name:      "ZC_NOTIFY_EFFECT",
		Length:    sizeZCNotifyEffect(),
		Direction: DirectionServerToClient,
	})
	// P3b-2: skill usage family (CZ_USE_SKILL2 + ZC_NOTIFY_SKILL +
	// ZC_ACK_TOUSESKILL). See the per-constant source citations above
	// (clif_shuffle.hpp:4750 + packets_struct.hpp:2448-2461, 4658-4671)
	// for the rAthena packetdb lines pinning each opcode and on-wire
	// size to PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderCZUSESKILL(),
		Name:      "CZ_USE_SKILL2",
		Length:    sizeCZUseSkill2(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYSKILL(),
		Name:      "ZC_NOTIFY_SKILL",
		Length:    sizeZCNotifySkill(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCACKTOUSESKILL(),
		Name:      "ZC_ACK_TOUSESKILL",
		Length:    sizeZCAckToUseSkill(),
		Direction: DirectionServerToClient,
	})
	// M14d: ground-target skills. CZ_USE_SKILL_TOPOS (fixed 11 bytes) requests a
	// ground cast; ZC_NOTIFY_GROUNDSKILL (fixed 18 bytes) broadcasts the poseffect
	// animation to the AREA around the cast tile.
	db.Register(Definition{
		ID:        HeaderCZUSESKILLTOPOS(),
		Name:      "CZ_USE_SKILL_TOPOS",
		Length:    sizeCZUseSkillToPos(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYGROUNDSKILL(),
		Name:      "ZC_NOTIFY_GROUNDSKILL",
		Length:    sizeZCNotifyGroundSkill(),
		Direction: DirectionServerToClient,
	})
	// S1: player-to-player trade handshake (request/ack/cancel). NO item or zeny
	// movement (the atomic-commit slice is deferred). ZC_REQ/ZC_ACK use the
	// PACKETVER > 6 layouts (packets.hpp:373/:387); see trade.go for field maps.
	db.Register(Definition{
		ID:        HeaderCZTRADEREQUEST(),
		Name:      "CZ_TRADE_REQUEST",
		Length:    sizeCZTradeRequest(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZTRADEACK(),
		Name:      "CZ_TRADE_ACK",
		Length:    sizeCZTradeAck(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZTRADECANCEL(),
		Name:      "CZ_TRADE_CANCEL",
		Length:    sizeCZTradeCancel(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCREQEXCHANGEITEM(),
		Name:      "ZC_REQ_EXCHANGE_ITEM",
		Length:    sizeZCReqExchange(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCACKEXCHANGEITEM(),
		Name:      "ZC_ACK_EXCHANGE_ITEM",
		Length:    sizeZCAckExchange(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCCANCELEXCHANGEITEM(),
		Name:      "ZC_CANCEL_EXCHANGE_ITEM",
		Length:    sizeZCCancelExchange(),
		Direction: DirectionServerToClient,
	})
	// S2: trade add-item/ok/conclude. The 0x0b42 ZC_ADD variant is the
	// PACKETVER >= 20200916 layout active at 20250604 (62B; see trade.go).
	db.Register(Definition{
		ID:        HeaderCZADDEXCHANGEITEM(),
		Name:      "CZ_ADD_EXCHANGE_ITEM",
		Length:    sizeCZAddExchangeItem(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZTRADEOK(),
		Name:      "CZ_TRADE_OK",
		Length:    sizeCZTradeOk(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCADDEXCHANGEITEM(),
		Name:      "ZC_ADD_EXCHANGE_ITEM",
		Length:    sizeZCAddExchangeItem(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCACKADDEXCHANGEITEM(),
		Name:      "ZC_ACK_ADD_EXCHANGE_ITEM",
		Length:    sizeZCAckAddExchange(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCCONCLUDEEXCHANGEITEM(),
		Name:      "ZC_CONCLUDE_EXCHANGE_ITEM",
		Length:    sizeZCConcludeExchange(),
		Direction: DirectionServerToClient,
	})
	// P3c: ground item drop notification. rAthena binds opcode 0x0ADD
	// with size 22 for PACKETVER >= 20180418 (clif_packetdb.hpp:1921);
	// the v5 layout adds <showDropEffect> + <dropEffectMode> on top of
	// the v4 20-byte frame. See map_item_drop.go for the full struct
	// documentation and per-field citations.
	db.Register(Definition{
		ID:        HeaderZCItemFallEntry(),
		Name:      "ZC_ITEM_FALL_ENTRY",
		Length:    sizeZCItemFallEntry(),
		Direction: DirectionServerToClient,
	})
	// A4: item drop + pickup. The C→S drop frame is <index>.W <amount>.W
	// (clif_packetdb.hpp:1385-1606). Which opcode the client sends is
	// build-specific, so the gateway accepts the five free aliases below; the
	// effective drop opcode at PACKETVER 20250604 is 0x0363 (clif_shuffle.hpp:4733).
	// 0x0362 is the modern pickup opcode (clif_shuffle.hpp:4732) and 0x0438 is
	// CZ_USE_SKILL2 (clif_shuffle.hpp:4750), so neither is a drop alias here.
	for _, op := range []uint16{
		HeaderCZDROPITEM0363(), HeaderCZDROPITEM0885(), HeaderCZDROPITEM02C4(),
		HeaderCZDROPITEM0891(), HeaderCZDROPITEM089E(),
	} {
		db.Register(Definition{
			ID:        op,
			Name:      "CZ_ITEM_DROP",
			Length:    sizeCZDropItem(),
			Direction: DirectionClientToServer,
		})
	}
	// A4: the modern pickup alias 0x0362 (clif_shuffle.hpp:4732 TakeItem). The
	// 0x009f legacy pickup registration above stays for older PACKETVERs; both
	// route through the same CZ_ITEM_PICKUP parser (see HeaderCZITEMTAKE0362()).
	db.Register(Definition{
		ID:        HeaderCZITEMTAKE0362(),
		Name:      "CZ_ITEM_PICKUP",
		Length:    sizeCZItemPickup(),
		Direction: DirectionClientToServer,
	})
	// A4: server→client floor-item and ack frames. Sizes + opcodes verified
	// in map_item_drop.go.
	db.Register(Definition{
		ID:        HeaderZCItemEntry(),
		Name:      "ZC_ITEM_ENTRY",
		Length:    sizeZCItemEntry(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCItemDisappear(),
		Name:      "ZC_ITEM_DISAPPEAR",
		Length:    sizeZCItemDisappear(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCItemThrowAck(),
		Name:      "ZC_ITEM_THROW_ACK",
		Length:    sizeZCItemThrowAck(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCItemPickupAck(),
		Name:      "ZC_ITEM_PICKUP_ACK",
		Length:    sizeZCItemPickupAck(),
		Direction: DirectionServerToClient,
	})
	// Phase 44: the inventory-removal counterpart. A shop sale deletes the sold
	// row and rAthena tells the client with this frame (clif_delitem,
	// clif.cpp:2915-2928; struct packets.hpp:825-831, 8B at every PACKETVER).
	db.Register(Definition{
		ID:        HeaderZCDeleteItemFromBody(),
		Name:      "ZC_DELETE_ITEM_FROM_BODY",
		Length:    sizeZCDeleteItemFromBody,
		Direction: DirectionServerToClient,
	})
	// M11: the party (group) family. Both C→S create variants are registered
	// because the client picks one by build (clif_packetdb.hpp:98,:243); the S→C
	// roster/member opcodes are the >=20171207 pair 0x0ae5/0x0ae4 active at
	// 20250604 (packets_struct.hpp:274-276). ZC_GROUP_LIST is variable-length, so
	// its registered Length is the empty-roster header size and the real frame
	// length travels in its own packetLen field.
	db.Register(Definition{
		ID:        HeaderCZMAKEGROUP(),
		Name:      "CZ_MAKE_GROUP",
		Length:    sizeCZMakeGroup(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZMAKEGROUP2(),
		Name:      "CZ_MAKE_GROUP2",
		Length:    sizeCZMakeGroup2(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQJOINGROUP(),
		Name:      "CZ_REQ_JOIN_GROUP",
		Length:    sizeCZReqJoinGroup(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZJOINGROUP(),
		Name:      "CZ_JOIN_GROUP",
		Length:    sizeCZJoinGroup(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQLEAVEGROUP(),
		Name:      "CZ_REQ_LEAVE_GROUP",
		Length:    sizeCZReqLeaveGroup(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZCHANGEGROUPEXPOPT(),
		Name:      "CZ_CHANGE_GROUPEXPOPTION",
		Length:    sizeCZChangeGroupExp(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQEXPELGROUPMEMBER(),
		Name:      "CZ_REQ_EXPEL_GROUP_MEMBER",
		Length:    sizeCZReqExpelMember(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCACKMAKEGROUP(),
		Name:      "ZC_ACK_MAKE_GROUP",
		Length:    sizeZCAckMakeGroup(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPARTYJOINREQ(),
		Name:      "ZC_PARTY_JOIN_REQ",
		Length:    sizeZCPartyJoinReq(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPARTYJOINREQACK(),
		Name:      "ZC_PARTY_JOIN_REQ_ACK",
		Length:    sizeZCPartyJoinReqAck(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPARTYCONFIG(),
		Name:      "ZC_PARTY_CONFIG",
		Length:    sizeZCPartyConfig(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCGROUPLIST(),
		Name:      "ZC_GROUP_LIST",
		Length:    sizeZCGroupListHeader,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCADDMEMBERTOGROUP(),
		Name:      "ZC_ADD_MEMBER_TO_GROUP",
		Length:    sizeZCAddMemberToGroup,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCDELETEMEMBERFROMGRP(),
		Name:      "ZC_DELETE_MEMBER_FROM_GROUP",
		Length:    sizeZCDeleteMember(),
		Direction: DirectionServerToClient,
	})

	// --- M11 mail (RODEX, >=20150513; clif_packetdb.hpp:1738-1762) ---
	// C→S mailbox verbs. The three refreshinbox opcodes and the two 26-byte
	// mailbox2 variants share handlers; read and delete share a shape.
	db.Register(Definition{ID: HeaderCZOPENMAILBOX(), Name: "CZ_OPEN_MAILBOX", Length: sizeCZOpenMailbox(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQNEXTMAILLIST(), Name: "CZ_REQ_NEXT_MAIL_LIST", Length: sizeCZOpenMailbox(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQREFRESHMAILL(), Name: "CZ_REQ_REFRESH_MAIL_LIST", Length: sizeCZOpenMailbox(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZOPENMAILBOX2(), Name: "CZ_OPEN_MAILBOX2", Length: sizeCZOpenMailbox2(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREFRESHMAILLIST(), Name: "CZ_REQ_REFRESH_MAIL_LIST2", Length: sizeCZOpenMailbox2(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZCLOSEMAILBOX(), Name: "CZ_CLOSE_MAILBOX", Length: sizeCZCancelWrite(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQREADMAIL(), Name: "CZ_REQ_READ_MAIL", Length: sizeCZReadDeleteMail(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQDELETEMAIL(), Name: "CZ_REQ_DELETE_MAIL", Length: sizeCZReadDeleteMail(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQZENYFROMMAIL(), Name: "CZ_REQ_ZENY_FROM_MAIL", Length: sizeCZGetAttach(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQITEMFROMMAIL(), Name: "CZ_REQ_ITEM_FROM_MAIL", Length: sizeCZGetAttach(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQCANCELWRITE(), Name: "CZ_REQ_CANCEL_WRITE_MAIL", Length: sizeCZCancelWrite(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQADDITEMMAIL(), Name: "CZ_REQ_ADD_ITEM_TO_MAIL", Length: sizeCZMailItem(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQREMOVEITEMMA(), Name: "CZ_REQ_REMOVE_ITEM_MAIL", Length: sizeCZMailItem(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQOPENWRITEMAI(), Name: "CZ_REQ_OPEN_WRITE_MAIL", Length: sizeCZOpenWriteMail(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZCHECKRECEIVENAM(), Name: "CZ_CHECK_RECEIVE_CHARACTER_NAME", Length: sizeCZOpenWriteMail(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZCHECKNAME2(), Name: "CZ_CHECKNAME2", Length: sizeCZCheckName2(), Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQWRITEMAIL(), Name: "CZ_REQ_WRITE_MAIL", Length: VariableLength, Direction: DirectionClientToServer})
	db.Register(Definition{ID: HeaderCZREQWRITEMAIL2(), Name: "CZ_REQ_WRITE_MAIL2", Length: VariableLength, Direction: DirectionClientToServer})
	// S→C fixed-size frames; the variable 0x0ac2 list and 0x0b63 read ack are
	// emitted straight to the wire and need no inbound length.
	db.Register(Definition{ID: HeaderZCNOTIFYUNREADMA(), Name: "ZC_NOTIFY_UNREADMAIL", Length: sizeZCNotifyUnreadMail(), Direction: DirectionServerToClient})
	db.Register(Definition{ID: HeaderZCACKMAILWRITE(), Name: "ZC_ACK_WRITE_MAIL", Length: sizeZCAckWriteMail(), Direction: DirectionServerToClient})
	db.Register(Definition{ID: HeaderZCACKZENYFROMMAI(), Name: "ZC_ACK_ZENY_FROM_MAIL", Length: sizeZCAckZenyFromMail(), Direction: DirectionServerToClient})
	db.Register(Definition{ID: HeaderZCACKITEMFROMMAI(), Name: "ZC_ACK_ITEM_FROM_MAIL", Length: sizeZCAckItemFromMail(), Direction: DirectionServerToClient})
	db.Register(Definition{ID: HeaderZCACKDELETEMAIL(), Name: "ZC_ACK_DELETE_MAIL", Length: sizeZCAckDeleteMail(), Direction: DirectionServerToClient})
	db.Register(Definition{ID: HeaderZCACKREMOVEITEMM(), Name: "ZC_ACK_REMOVE_ITEM_MAIL", Length: sizeZCAckRemoveItem(), Direction: DirectionServerToClient})
	db.Register(Definition{ID: HeaderZCACKOPENWRITE(), Name: "ZC_ACK_OPEN_WRITE_MAIL", Length: sizeZCAckOpenWrite(), Direction: DirectionServerToClient})
	db.Register(Definition{ID: HeaderZCCHECKNAME(), Name: "ZC_CHECKNAME", Length: sizeZCCheckName(), Direction: DirectionServerToClient})
	db.Register(Definition{ID: HeaderZCACKADDITEMRODE(), Name: "ZC_ACK_ADD_ITEM_RODEX", Length: sizeZCAckAddItemRodex(), Direction: DirectionServerToClient})

	return db
}
