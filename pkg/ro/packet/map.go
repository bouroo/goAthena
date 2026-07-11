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
const (
	// C→S — client → map server.
	HeaderCZENTER           uint16 = 0x0072 // rathena/src/map/clif.cpp:10642 (CZ_ENTER)
	HeaderCZREQUESTMOVE     uint16 = 0x0085 // rathena/src/map/clif.cpp:11374 (CZ_REQUEST_MOVE)
	HeaderCZNOTIFYACTORINIT uint16 = 0x007d // rathena/src/map/clif.cpp:10744 (CZ_NOTIFY_ACTORINIT / LoadEndAck)
	HeaderCZREQUESTTIME     uint16 = 0x007e // rathena/src/map/clif.cpp:11198 (CZ_REQUEST_TIME / TickSend)
	// CZ_ACTION_REQUEST (0x0089) — sit/stand/attack request. rathena/src/map/
	// clif_packetdb.hpp:38 (`parseable_packet(0x0089,7,clif_parse_ActionRequest,2,6)`).
	// Fixed 7 bytes: [2:cmd][4:targetGID][1:action].
	HeaderCZACTIONREQUEST uint16 = 0x0089
	// CZ_GLOBAL_MESSAGE (0x008c) — public chat. rathena/src/map/
	// clif_packetdb.hpp:40 (`parseable_packet(0x008c,-1,clif_parse_GlobalMessage,2,4)`).
	// Variable length: [2:cmd][2:packetLength][n:text+null].
	HeaderCZGLOBALMESSAGE uint16 = 0x008c
	// CZ_CHANGE_DIRECTION (0x009b) — direction change request. rathena/src/map/
	// clif_packetdb.hpp:48 (`parseable_packet(0x009b,5,clif_parse_ChangeDir,2,4)`).
	// Fixed 5 bytes: [2:cmd][2:headDir uint16][1:dir uint8]. The pos array
	// [2, 4] documents headDir at offset 2 and dir at offset 4 — the
	// handler reads headDir via RFIFOB on this PACKETVER (clif.cpp:11613).
	HeaderCZCHANGEDIR uint16 = 0x009b
	// CZ_REQ_EMOTION (0x00bf) — emotion request. rathena/src/map/
	// clif_packetdb.hpp:66 (`parseable_packet(HEADER_CZ_REQ_EMOTION,
	// sizeof(PACKET_CZ_REQ_EMOTION), clif_parse_Emotion, 0)`).
	// Fixed 3 bytes: [2:cmd][1:emotion_type]. The struct definition lives
	// at rathena/src/map/packets.hpp:1406-1409.
	HeaderCZREQEMOTION uint16 = 0x00bf

	// S→C — map server → client.
	HeaderZCACCEPTENTER      uint16 = 0x02eb // rathena/src/map/packets.hpp:571 (ZC_ACCEPT_ENTER, PACKETVER >= 20160330 branch)
	HeaderZCREFUSEENTER      uint16 = 0x0074 // rathena/src/map/packets.hpp:590 (ZC_REFUSE_ENTER)
	HeaderZCNOTIFYPLAYERMOVE uint16 = 0x0087 // rathena/src/map/packets.hpp (ZC_NOTIFY_PLAYERMOVE)
	HeaderZCSPAWNUNIT        uint16 = 0x09fe // rathena/src/map/packets.hpp ZC_SPAWN_UNIT (PACKETVER >= 20150513 branch)
	HeaderZCSETUNITIDLE      uint16 = 0x09ff // rathena/src/map/packets.hpp ZC_SET_UNIT_IDLE (same layout as ZC_SPAWN_UNIT, different opcode)
	HeaderZCUNITWALKING      uint16 = 0x09fd // rathena/src/map/packets_struct.hpp unit_walkingType (PACKETVER >= 20150513)
	HeaderZCMAPPROPERTYR2    uint16 = 0x099b // rathena/src/map/clif.cpp:6869 (ZC_MAPPROPERTY_R2, PACKETVER >= 20121010)
	HeaderZCNOTIFYTIME       uint16 = 0x007f // rathena/src/map/clif.cpp:11186 (ZC_NOTIFY_TIME)
	// ZC_ACTION_RESPONSE (0x008b) — sit/stand/attack broadcast echo. rAthena
	// does not currently emit 0x008b (clif_packetdb.hpp:39 registers it as a
	// 2-byte stub) — modern clients use ZC_NOTIFY_ACT (0x008a) for area
	// broadcast. For the single-player echo path we use the compact 0x008b
	// shape: [2:cmd][4:GID][1:action][4:targetGID] = 11 bytes.
	HeaderZCACTIONRESPONSE uint16 = 0x008b
	// ZC_NOTIFY_CHAT (0x008d) — chat echo. rathena/src/map/packets_struct.hpp:2337
	// (`PACKET_ZC_NOTIFY_CHAT { int16 PacketType; int16 PacketLength; uint32 GID; char Message[] }`).
	// Variable length: [2:cmd][2:packetLength][4:GID][n:text+null].
	HeaderZCNOTIFYCHAT uint16 = 0x008d
	// ZC_CHANGE_DIRECTION (0x009c) — direction echo. rathena/src/map/
	// packets.hpp:688-694 (`PACKET_ZC_CHANGE_DIRECTION { int16 packetType;
	// uint32 srcId; uint16 headDir; uint8 dir }`). Fixed 9 bytes:
	// [2:cmd][4:srcId][2:headDir uint16][1:dir uint8].
	HeaderZCCHANGEDIR uint16 = 0x009c
	// ZC_EMOTION (0x00c0) — emotion echo. rathena/src/map/packets.hpp:1973-1978
	// (`PACKET_ZC_EMOTION { int16 packetType; int32 GID; uint8 type }`).
	// Fixed 7 bytes: [2:cmd][4:GID int32][1:type uint8].
	HeaderZCEMOTION uint16 = 0x00c0
	// CZ_GETCHARNAMEREQUEST (0x0094) — client requests a character name by GID.
	// rathena/src/map/clif_packetdb.hpp:45 (`parseable_packet(0x0094,6,clif_parse_GetCharNameRequest,2)`).
	// Fixed 6 bytes: [2:cmd][4:GID int32].
	HeaderCZGETCHARNAMEREQUEST uint16 = 0x0094
	// ZC_ACK_REQNAME (0x0095) — server responds with a character name.
	// rathena/src/map/clif.cpp:9923 (`0095 <id>.L <char name>.24B`).
	// Fixed 30 bytes: [2:cmd][4:GID int32][24:name char[24]].
	HeaderZCACKREQNAME uint16 = 0x0095
	// CZ_RESTART (0x00b2) — client requests respawn or return to char select.
	// rathena/src/map/clif_packetdb.hpp:61 (`parseable_packet(0x00b2,3,clif_parse_Restart,2)`).
	// Fixed 3 bytes: [2:cmd][1:type uint8] (0=respawn, 1=return to char select).
	HeaderCZRESTART uint16 = 0x00b2
	// CZ_CONTACTNPC (0x0090) — client clicks an NPC. rathena/src/map/
	// clif_packetdb.hpp:42 (`parseable_packet(0x0090,7,clif_parse_NpcClicked,2,6)`).
	// Fixed 7 bytes: [2:cmd][4:AID uint32][1:type uint8] (1=click).
	HeaderCZCONTACTNPC uint16 = 0x0090
	// CZ_REQNEXTSCRIPT (0x00b9) — client clicks "Next" in a dialog.
	// rathena/src/map/clif_packetdb.hpp:60 (`parseable_packet(0x00b9,6,clif_parse_ScriptContinue,2)`).
	// Fixed 6 bytes: [2:cmd][4:NpcID uint32].
	HeaderCZREQNEXTSCRIPT uint16 = 0x00b9
	// P2A: CZ_USE_ITEM2 (0x0439) — client requests to use a consumable
	// item. rathena/src/map/clif_packetdb.hpp:1151
	// (`parseable_packet(0x0439,8,clif_parse_UseItem,2,4)`).
	// Fixed 8 bytes: [2:cmd=0x0439][2:inventory index uint16]
	// [4:AID uint32]. The 2-byte "item id" rAthena reads at
	// packet_db[..].pos[0] is the inventory index, not the item DB
	// nameid — clif_parse_UseItem uses it to look up the row in
	// `inventory` and then resolves nameid from there.
	HeaderCZUSEITEM2 uint16 = 0x0439
	// P2A: CZ_REQ_WEAR_EQUIP_V5 (0x0998) — client requests to equip
	// an item. rathena/src/map/packets.hpp:1504-1509
	// (PACKET_CZ_REQ_WEAR_EQUIP, PACKETVER >= 20120925 branch).
	// Fixed 10 bytes: [2:cmd=0x0998][2:inventory index uint16]
	// [4:equip position uint32 — EQP_* bitmask]. The 32-bit position
	// field is what makes this the "V5" shape; pre-20120925 uses
	// uint16 position with opcode 0x00a9.
	HeaderCZREQWEAREQUIPV5 uint16 = 0x0998
	// P2A: CZ_REQ_TAKEOFF_EQUIP (0x00ab) — client requests to
	// unequip an item. rathena/src/map/clif_packetdb.hpp:59
	// (`parseable_packet(0x00ab,4,clif_parse_UnequipItem,2)`).
	// Fixed 4 bytes: [2:cmd=0x00ab][2:inventory index uint16]. The
	// unequip handler ignores the equip-position field the client
	// sends; the server derives the position from the row's
	// `inventory.equip` column.
	HeaderCZREQTAKEOFFEQUIP uint16 = 0x00ab
	// CZ_CLOSE_DIALOG (0x0146) — client clicks "Close" in a dialog.
	// rathena/src/map/clif_packetdb.hpp:72 (`parseable_packet(0x0146,6,clif_parse_CloseDialog,2)`).
	// Fixed 6 bytes: [2:cmd][4:GID uint32].
	HeaderCZCLOSEDIALOG uint16 = 0x0146
	// ZC_SAY_DIALOG2 (0x0972) — server sends dialog text (PACKETVER >= 20220504).
	// rathena/src/map/packets_struct.hpp: ZC_SAY_DIALOG2.
	// Variable length: [2:cmd][2:packetLength][4:NpcID][1:type][n:message+null].
	HeaderZCSAYDIALOG2 uint16 = 0x0972
	// ZC_WAIT_DIALOG2 (0x0973) — server shows "Next" button (PACKETVER >= 20220504).
	// rathena/src/map/packets_struct.hpp: ZC_WAIT_DIALOG2.
	// Fixed 7 bytes: [2:cmd][4:NpcID][1:type].
	HeaderZCWAITDIALOG2 uint16 = 0x0973
	// ZC_CLOSE_DIALOG (0x00b6) — server shows "Close" button.
	// rathena/src/map/clif_packetdb.hpp:58 (`packet(0x00b6,6,clif_parse_CloseDialog,0)`).
	// Fixed 6 bytes: [2:cmd][4:NpcID].
	HeaderZCCLOSEDIALOG uint16 = 0x00b6
	// M16: NPC shop interaction. CZ_ACK_SELECT_DEALTYPE / ZC_SELECT_DEALTYPE
	// carry the deal-type selection (Buy / Sell / Cancel) that follows
	// CZ_CONTACTNPC for shop-type NPCs. CZ_PC_PURCHASE_ITEMLIST /
	// ZC_PC_PURCHASE_ITEMLIST carry the buy item list and the player's
	// purchase request. ZC_PC_PURCHASE_RESULT acknowledges the purchase
	// outcome (success / not enough zeny / not enough slots / overweight).
	// Sources cited per constant; the modern pre-Renewal / Renewal shop
	// opcodes are in rathena/src/map/clif_packetdb.hpp.
	HeaderCZACKSELECTDEALTYPE  uint16 = 0x00c5 // CZ_ACK_SELECT_DEALTYPE
	HeaderCZPCPURCHASEITEMLIST uint16 = 0x00c8 // CZ_PC_PURCHASE_ITEMLIST
	HeaderZCSELECTDEALTYPE     uint16 = 0x00c4 // ZC_SELECT_DEALTYPE
	HeaderZCPCPURCHASEITEMLIST uint16 = 0x0b77 // ZC_PC_PURCHASE_ITEMLIST (PACKETVER >= 20210203)
	HeaderZCPCPURCHASERESULT   uint16 = 0x00ca // ZC_PC_PURCHASE_RESULT
	// P2B: shop sell flow. CZ_PC_SELL_ITEMLIST (0x00c9, C→S) carries
	// the player's sell list (per-entry inventory index + amount);
	// ZC_PC_SELL_ITEMLIST (0x00c7, S→C) is the server's priced list
	// of every item the player currently owns at sell-list time
	// (per-entry index + sell price + overcharge price), and
	// ZC_PC_SELL_RESULT (0x00cb, S→C) is the per-transaction ack.
	// rathena/src/map/clif_packetdb.hpp + clif.cpp:buy_sell_selection /
	// clif_parse_PurchaseItem / clif_purchaseitemlist for shape.
	HeaderCZPCSELLITEMLIST          uint16 = 0x00c9 // CZ_PC_SELL_ITEMLIST
	HeaderZCPCSELLITEMLIST          uint16 = 0x00c7 // ZC_PC_SELL_ITEMLIST
	HeaderZCPCSELLRESULT            uint16 = 0x00cb // ZC_PC_SELL_RESULT
	HeaderZCSTATUS                  uint16 = 0x00bd // rathena/src/map/packets.hpp:909 (ZC_STATUS)
	HeaderZCPARCHANGE               uint16 = 0x00b0 // rathena/src/map/packets_struct.hpp:354 (ZC_PAR_CHANGE)
	HeaderZCLONGPARCHANGE           uint16 = 0x00b1 // rathena/src/map/packets_struct.hpp:361 (ZC_LONGPAR_CHANGE)
	HeaderZCINVENTORYITEMLISTNORMAL uint16 = 0x00a3 // rathena/src/map/clif_packetdb.hpp (ZC_INVENTORY_ITEMLIST_NORMAL)
	HeaderZCINVENTORYITEMLISTEQUIP  uint16 = 0x00a4 // rathena/src/map/clif_packetdb.hpp (ZC_INVENTORY_ITEMLIST_EQUIP)
	// P2A: ZC_REQ_WEAR_EQUIP_ACK_V5 (0x0999) — server ack for
	// CZ_REQ_WEAR_EQUIP_V5. rathena/src/map/packets_struct.hpp:1269-1276
	// (PACKETVER_MAIN_NUM >= 20121205 branch). Fixed 11 bytes:
	// [2:cmd=0x0999][2:inventory index uint16][4:wearLocation uint32]
	// [2:wItemSpriteNumber uint16][1:result uint8] (result 0=fail,
	// 1=ok, 2=low-level fail per clif.cpp:4306-4309).
	HeaderZCREQWEAREQUIPACKV5 uint16 = 0x0999
	// P2A: ZC_REQ_TAKEOFF_EQUIP_ACK (0x99a) — server ack for
	// CZ_REQ_TAKEOFF_EQUIP. rathena/src/map/packets.hpp:1007-1013
	// (PACKETVER >= 20130000 branch, which covers 20250604). Fixed 8
	// bytes: [2:cmd=0x99a][2:inventory index uint16]
	// [4:wearLocation uint32][1:flag uint8] (inverted for
	// PACKETVER >= 20110824 per clif.cpp:4338 — success becomes
	// 0 on the wire).
	HeaderZCREQTAKEOFFEQUIPACK uint16 = 0x099a
	// P2A: ZC_USE_ITEM_ACK2 (0x01c8) — server ack for
	// CZ_USE_ITEM2. rathena/src/map/packets_struct.hpp:312
	// (useItemAckType = 0x1c8, PACKETVER >= 3). Fixed 13 bytes:
	// [2:cmd=0x01c8][2:index int16][2:itemId uint16][4:AID uint32]
	// [2:amount int16][1:result uint8] (PACKETVER 20250604 uses
	// uint16 itemId; uint32 is reserved for PACKETVER >=
	// 20181121). For PACKETVER 20250604, index is +2 from the
	// server-side row index (clif.cpp:4482).
	HeaderZCUSEITEMACK2   uint16 = 0x01c8
	HeaderZCSKILLINFOLIST uint16 = 0x010f // rathena/src/map/packets_struct.hpp:4279 (ZC_SKILLINFO_LIST)
	// P3b-2: skill usage family. The 20250604 PACKETVER falls into the
	// PACKETVER_RE_NUM >= 20190904 / PACKETVER_MAIN_NUM >= 20190904
	// branch of rathena/src/map/clif_shuffle.hpp:4750, which binds
	// opcode 0x0438 to clif_parse_UseSkillToId with field offsets 2,4,6
	// (the older "to pos" 0x0438 variant is for earlier PACKETVERs and
	// shares the same opcode+length but parses 4 fields). Layouts are
	// pinned to rathena/src/map/packets_struct.hpp.
	HeaderCZUSESKILL      uint16 = 0x0438 // CZ_USE_SKILL2 — clif_parse_UseSkillToId (clif_shuffle.hpp:4750)
	HeaderZCNOTIFYSKILL   uint16 = 0x01de // ZC_NOTIFY_SKILL — packets_struct.hpp:4671 (PACKETVER >= 3)
	HeaderZCACKTOUSESKILL uint16 = 0x0110 // ZC_ACK_TOUSESKILL — packets_struct.hpp:2461
	// P2C: stats & leveling — stat allocation + level-up effect.
	// CZ_STATUS_CHANGE (0x00bb) is the client request to raise a base
	// stat (rathena/src/map/clif.cpp:12714 clif_parse_StatusChange).
	HeaderCZSTATUSCHANGE    uint16 = 0x00bb // rathena/src/map/clif.cpp:12714 (CZ_STATUS_CHANGE)
	HeaderZCSTATUSCHANGEACK uint16 = 0x00bc // rathena/src/map/clif.cpp:4283 (ZC_STATUS_CHANGE_ACK)
	HeaderZCNOTIFYEFFECT    uint16 = 0x019b // rathena/src/map/packets.hpp:1120 (ZC_NOTIFY_EFFECT)
	HeaderZCSHORTCUTKEYLIST uint16 = 0x02b9 // rathena/src/map/packets_struct.hpp:1619 (ZC_SHORTCUT_KEY_LIST, PACKETVER < 20090603)
	// ZC_NOTIFY_ACT (0x08c8) — damage / action notification. rathena/src/map/
	// packets.hpp:1426 (PACKETVER >= 20131223). Fixed 34 bytes.
	HeaderZCNOTIFYACT uint16 = 0x08c8
	// ZC_NOTIFY_VANISH (0x0080) — entity vanish notification. rathena/src/map/
	// packets.hpp:609. Fixed 7 bytes.
	HeaderZCNOTIFYVANISH uint16 = 0x0080
)

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
	// sizeZCAcceptEnter = int16 packetType + uint32 startTime +
	// uint8 posDir[3] + uint8 xSize + uint8 ySize + uint16 font =
	// 2+4+3+1+1+2 = 13 (rathena/src/map/packets.hpp:562-571).
	sizeZCAcceptEnter = 13
	// sizeZCRefuseEnter = int16 packetType + uint8 errorCode = 2+1 = 3
	// (rathena/src/map/packets.hpp:585-589, static_assert at :589).
	sizeZCRefuseEnter = 3
	// sizeCZEnter = int16 packetType + uint32 AID + uint32 CID +
	// uint32 authCode + uint32 clientTime + uint8 sex = 2+4+4+4+4+1 = 19
	// (rathena/src/map/clif.cpp:10642 + the WantToConnection handler
	// reading RFIFO* at the documented offsets).
	sizeCZEnter = 19
	// sizeCZRequestMove = int16 packetType + uint8 dest[3] = 2+3 = 5
	// (rathena/src/map/clif.cpp:11374; the WalkToXY handler calls RFIFOPOS
	// at packet_db[..].pos[0], which is at offset 2 right after the cmd).
	sizeCZRequestMove = 5
	// sizeZCNotifyPlayerMove = int16 packetType + uint32 moveStartTime +
	// uint8 srcPos[3] + uint8 destPos[3] = 2+4+3+3 = 12
	// (rathena/src/map/packets.hpp ZC_NOTIFY_PLAYERMOVE).
	sizeZCNotifyPlayerMove = 12
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
	// sizeCZNotifyActorInit = int16 packetType = 2 (cmd-only packet, no payload).
	// rathena/src/map/clif.cpp:10744-10746.
	sizeCZNotifyActorInit = 2
	// sizeCZRequestTime = int16 packetType + uint32 clientTick = 2+4 = 6
	// (rathena/src/map/clif.cpp:11198-11206).
	sizeCZRequestTime = 6
	// sizeZCMapPropertyR2 = int16 packetType + int16 propertyType + uint32 flags = 2+2+4 = 8
	// (rathena/src/map/clif.cpp:6869-6902, PACKETVER >= 20121010 branch).
	sizeZCMapPropertyR2 = 8
	// sizeZCNotifyTime = int16 packetType + uint32 time = 2+4 = 6
	// (rathena/src/map/clif.cpp:11186-11193).
	sizeZCNotifyTime = 6
	// sizeZCStatus = int16 packetType + uint16 point + 6*(uint8 stat +
	// uint8 need) + 12*int16 derived = 2+2+12+24 = 44
	// (rathena/src/map/packets.hpp:909-938).
	sizeZCStatus = 44
	// sizeZCParChange = int16 packetType + uint16 varID + int32 count = 2+2+4 = 8
	// (rathena/src/map/packets_struct.hpp:354-358).
	sizeZCParChange = 8
	// sizeZCLongParChange = int16 packetType + uint16 varID + int32 amount = 2+2+4 = 8
	// (rathena/src/map/packets_struct.hpp:361-365).
	sizeZCLongParChange = 8
	// P2C: stat allocation + level-up effect sizes.
	// sizeCZStatusChange = int16 packetType + uint16 statusID + uint8 amount = 2+2+1 = 5
	// (rathena/src/map/clif.cpp:12714 clif_parse_StatusChange).
	sizeCZStatusChange = 5
	// sizeZCStatusChangeAck = int16 packetType + uint16 statusID + uint8 result
	// + uint8 value = 2+2+1+1 = 6 (rathena/src/map/clif.cpp:4283).
	sizeZCStatusChangeAck = 6
	// sizeZCNotifyEffect = int16 packetType + uint32 AID + uint32 effectID = 2+4+4 = 10
	// (rathena/src/map/packets.hpp:1120 ZC_NOTIFY_EFFECT).
	sizeZCNotifyEffect = 10
	// sizeEmptyInventoryList = int16 packetType + int16 packetLength = 2+2 = 4.
	// Used for ZC_INVENTORY_ITEMLIST_NORMAL / ZC_INVENTORY_ITEMLIST_EQUIP /
	// ZC_SKILLINFO_LIST when the list is empty (count=0). The trailing
	// NORMALITEM_INFO / EQUIPITEM_INFO / SKILLDATA flexible array is
	// omitted entirely; rAthena's clif_send path only writes the header
	// in that case. See rathena/src/map/clif.cpp clif_inventorylist
	// (~:3060) and clif_skillinfoblock (~:5694).
	sizeEmptyInventoryList = 4
	// sizeZCShortcutKeyList = int16 packetType + 27 * hotkey_data =
	// 2 + 27*(int8 isSkill + uint32 id + int16 count) = 2 + 27*7 = 191
	// (rathena/src/map/packets_struct.hpp:1613-1619 — the PACKETVER
	// < 20090603 branch that gives opcode 0x02b9 with MAX_HOTKEYS_PACKET=27).
	// All 27 slots are zero-filled for a fresh character. hotkey_data is
	// declared at rathena/src/map/packets_struct.hpp:1576-1580.
	sizeZCShortcutKeyList = 191
	// sizeCZActionRequest = int16 packetType + uint32 targetGID +
	// uint8 action = 2+4+1 = 7 (rathena/src/map/clif_packetdb.hpp:38 —
	// `parseable_packet(0x0089,7,clif_parse_ActionRequest,2,6)`). The pos
	// array [2, 6] documents targetGID at offset 2 and action at offset 6.
	sizeCZActionRequest = 7
	// sizeZCActionResponse = int16 packetType + uint32 GID + uint8 action +
	// uint32 targetGID = 2+4+1+4 = 11. The wire shape is the compact
	// pre-Renewal echo for 0x008b — rAthena's modern clif uses
	// ZC_NOTIFY_ACT (0x008a) for the same broadcast but the gateway emits
	// 0x008b for the single-player echo path.
	sizeZCActionResponse = 11
	// sizeCZChangeDir = int16 packetType + uint16 headDir + uint8 dir =
	// 2+2+1 = 5 (rathena/src/map/clif_packetdb.hpp:48 —
	// `parseable_packet(0x009b,5,clif_parse_ChangeDir,2,4)`). The pos
	// array [2, 4] documents headDir at offset 2 and dir at offset 4.
	// rAthena's clif_parse_ChangeDir reads headDir via RFIFOB (single
	// byte) at clif.cpp:11613 — the upper byte of the uint16 is
	// effectively reserved on this PACKETVER and ignored by the handler.
	sizeCZChangeDir = 5
	// sizeZCChangeDir = int16 packetType + uint32 srcId + uint16 headDir +
	// uint8 dir = 2+4+2+1 = 9 (rathena/src/map/packets.hpp:688-694).
	sizeZCChangeDir = 9
	// sizeCZReqEmotion = int16 packetType + uint8 emotion_type = 2+1 = 3
	// (rathena/src/map/packets.hpp:1406-1410).
	sizeCZReqEmotion = 3
	// sizeZCEmotion = int16 packetType + int32 GID + uint8 type = 2+4+1 = 7
	// (rathena/src/map/packets.hpp:1973-1978).
	sizeZCEmotion = 7
	// sizeCZGetCharNameRequest = int16 packetType + int32 GID = 2+4 = 6
	// (rathena/src/map/clif_packetdb.hpp:45).
	sizeCZGetCharNameRequest = 6
	// sizeZCAckReqName = int16 packetType + int32 GID + char name[24] = 2+4+24 = 30
	// (rathena/src/map/packets_struct.hpp:3556-3560).
	sizeZCAckReqName = 30
	// sizeZCAckReqNameName is the on-wire name field width in ZC_ACK_REQNAME
	// (rathena/src/common/mmo.hpp:154 — NAME_LENGTH = 23+1 = 24).
	sizeZCAckReqNameName = 24
	// sizeCZRestart = int16 packetType + uint8 type = 2+1 = 3
	// (rathena/src/map/clif_packetdb.hpp:61).
	sizeCZRestart = 3
	// sizeCZContactNPC = int16 packetType + uint32 AID + uint8 type = 2+4+1 = 7
	// (rathena/src/map/clif_packetdb.hpp:42).
	sizeCZContactNPC = 7
	// sizeCZReqNextScript = int16 packetType + uint32 NpcID = 2+4 = 6
	// (rathena/src/map/clif_packetdb.hpp:60).
	sizeCZReqNextScript = 6
	// sizeCZCloseDialog = int16 packetType + uint32 GID = 2+4 = 6
	// (rathena/src/map/clif_packetdb.hpp:72).
	sizeCZCloseDialog = 6
	// sizeZCWaitDialog2 = int16 packetType + uint32 NpcID + uint8 type = 2+4+1 = 7
	// (rathena/src/map/packets_struct.hpp: ZC_WAIT_DIALOG2).
	sizeZCWaitDialog2 = 7
	// sizeZCCloseDialog = int16 packetType + uint32 NpcID = 2+4 = 6
	// (rathena/src/map/clif_packetdb.hpp:58).
	sizeZCCloseDialog = 6
	// sizeZCSelectDealtype = int16 packetType + uint32 NpcID = 2+4 = 6
	// (rathena/src/map/packets.hpp: ZC_SELECT_DEALTYPE).
	sizeZCSelectDealtype = 6
	// sizeCZAckSelectDealtype = int16 packetType + uint32 NpcID +
	// uint8 type = 2+4+1 = 7 (rathena/src/map/clif_packetdb.hpp —
	// CZ_ACK_SELECT_DEALTYPE).
	sizeCZAckSelectDealtype = 7
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
	// sizeZCPCPurchaseResult = int16 packetType + uint8 result = 2+1 = 3
	// (rathena/src/map/packets.hpp: ZC_PC_PURCHASE_RESULT).
	sizeZCPCPurchaseResult = 3
	// sizeZCPCSellResult = int16 packetType + uint8 result = 2+1 = 3
	// (rathena/src/map/packets.hpp: ZC_PC_SELL_RESULT).
	sizeZCPCSellResult = 3
	// sizeCZPCSellItemListEntry is the per-entry size in
	// CZ_PC_SELL_ITEMLIST: uint16 index + uint16 amount = 2+2 = 4
	// (rathena/src/map/clif_packetdb.hpp — CZ_PC_SELL_ITEMLIST).
	sizeCZPCSellItemListEntry = 4
	// sizeZCPCSellItemListItem is the per-item size in
	// ZC_PC_SELL_ITEMLIST: uint16 index + uint32 price +
	// uint32 overcharge = 2+4+4 = 10
	// (rathena/src/map/packets_struct.hpp: ZC_PC_SELL_ITEMLIST
	// PACKET_ZC_PC_SELL_ITEMLIST entry).
	sizeZCPCSellItemListItem = 10
	// sizeZCNotifyAct = int16 packetType + int32 srcID + int32 targetID +
	// int32 serverTick + int32 srcSpeed + int32 dmgSpeed + int32 damage +
	// int8 isSPDamage + uint16 div + uint8 type + int32 damage2
	// = 2+4+4+4+4+4+4+1+2+1+4 = 34 (rathena/src/map/packets.hpp:1413-1425,
	// PACKETVER >= 20131223 branch).
	sizeZCNotifyAct = 34
	// sizeZCNotifyVanish = int16 packetType + uint32 gid + uint8 type = 2+4+1 = 7
	// (rathena/src/map/packets.hpp:604-608).
	sizeZCNotifyVanish = 7
	// sizeCZUseSkill2 = int16 packetType + int16 skillLv + uint16 skillID +
	// uint32 targetID = 2+2+2+4 = 10 (clif_shuffle.hpp:4750 binds
	// opcode 0x0438 to length 10 for PACKETVER_RE_NUM >= 20190904).
	sizeCZUseSkill2 = 10
	// sizeZCNotifySkill = int16 packetType + uint16 SKID + uint32 AID +
	// uint32 targetID + uint32 startTime + int32 attackMT +
	// int32 attackedMT + int32 damage + int16 level + int16 count +
	// int8 action = 2+2+4+4+4+4+4+4+2+2+1 = 33
	// (rathena/src/map/packets_struct.hpp:4658-4671, PACKETVER >= 3).
	sizeZCNotifySkill = 33
	// sizeZCAckToUseSkill = int16 packetType + uint16 skillId + int32 btype +
	// uint32 itemId + uint8 flag + uint8 cause = 2+2+4+4+1+1 = 14
	// (rathena/src/map/packets_struct.hpp:2448-2460, PACKETVER_MAIN_NUM
	// >= 20181121 branch — 20250604 satisfies it).
	sizeZCAckToUseSkill = 14
	// sizeCZUseItem2 = int16 packetType + uint16 index + uint32 AID = 2+2+4 = 8
	// (rathena/src/map/clif_packetdb.hpp:1151).
	sizeCZUseItem2 = 8
	// sizeCZReqWearEquipV5 = int16 packetType + uint16 index + uint32 position = 2+2+4 = 8
	// (rathena/src/map/packets.hpp:1504-1509).
	sizeCZReqWearEquipV5 = 8
	// sizeCZReqTakeoffEquip = int16 packetType + uint16 index = 2+2 = 4
	// (rathena/src/map/clif_packetdb.hpp:59).
	sizeCZReqTakeoffEquip = 4
	// sizeZCReqWearEquipAckV5 = int16 packetType + uint16 index + uint32 wearLocation +
	// uint16 wItemSpriteNumber + uint8 result = 2+2+4+2+1 = 11
	// (rathena/src/map/packets_struct.hpp:1269-1276).
	sizeZCReqWearEquipAckV5 = 11
	// sizeZCReqTakeoffEquipAck = int16 packetType + uint16 index + uint32 wearLocation +
	// uint8 flag = 2+2+4+1 = 9
	// (rathena/src/map/packets.hpp:1007-1013).
	sizeZCReqTakeoffEquipAck = 9
	// sizeZCUseItemAck2 = int16 packetType + int16 index + uint16 itemId +
	// uint32 AID + int16 amount + uint8 result = 2+2+2+4+2+1 = 13
	// (rathena/src/map/packets_struct.hpp:2577-2589, PACKETVER 20250604 branch).
	sizeZCUseItemAck2 = 13
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
		ID:        HeaderCZENTER,
		Name:      "CZ_ENTER",
		Length:    sizeCZEnter,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQUESTMOVE,
		Name:      "CZ_REQUEST_MOVE",
		Length:    sizeCZRequestMove,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZNOTIFYACTORINIT,
		Name:      "CZ_NOTIFY_ACTORINIT",
		Length:    sizeCZNotifyActorInit,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQUESTTIME,
		Name:      "CZ_REQUEST_TIME",
		Length:    sizeCZRequestTime,
		Direction: DirectionClientToServer,
	})
	// M11: action + chat. CZ_ACTION_REQUEST is fixed 7 bytes; CZ_GLOBAL_MESSAGE
	// is variable — the text starts at offset 4 and the wire packetLength
	// slot at [2:4] carries the trailing byte count. See rathena/src/map/
	// clif_packetdb.hpp:38-40 for the canonical entries.
	db.Register(Definition{
		ID:        HeaderCZACTIONREQUEST,
		Name:      "CZ_ACTION_REQUEST",
		Length:    sizeCZActionRequest,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZGLOBALMESSAGE,
		Name:      "CZ_GLOBAL_MESSAGE",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	// M12: CZ_CHANGE_DIRECTION (fixed 5 bytes) + CZ_REQ_EMOTION (fixed 3
	// bytes) — basic player expression echo path. See the header
	// constants above for rAthena source pointers.
	db.Register(Definition{
		ID:        HeaderCZCHANGEDIR,
		Name:      "CZ_CHANGE_DIRECTION",
		Length:    sizeCZChangeDir,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQEMOTION,
		Name:      "CZ_REQ_EMOTION",
		Length:    sizeCZReqEmotion,
		Direction: DirectionClientToServer,
	})
	// M13: CZ_GETCHARNAMEREQUEST (fixed 6 bytes) + CZ_RESTART (fixed 3
	// bytes) — name lookup and respawn/char-select request.
	db.Register(Definition{
		ID:        HeaderCZGETCHARNAMEREQUEST,
		Name:      "CZ_GETCHARNAMEREQUEST",
		Length:    sizeCZGetCharNameRequest,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZRESTART,
		Name:      "CZ_RESTART",
		Length:    sizeCZRestart,
		Direction: DirectionClientToServer,
	})

	// --- S→C: map server → client.
	db.Register(Definition{
		ID:        HeaderZCREFUSEENTER,
		Name:      "ZC_REFUSE_ENTER",
		Length:    sizeZCRefuseEnter,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCACCEPTENTER,
		Name:      "ZC_ACCEPT_ENTER",
		Length:    sizeZCAcceptEnter,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYPLAYERMOVE,
		Name:      "ZC_NOTIFY_PLAYERMOVE",
		Length:    sizeZCNotifyPlayerMove,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSPAWNUNIT,
		Name:      "ZC_SPAWN_UNIT",
		Length:    sizeZCSpawnUnit,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCMAPPROPERTYR2,
		Name:      "ZC_MAPPROPERTY_R2",
		Length:    sizeZCMapPropertyR2,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYTIME,
		Name:      "ZC_NOTIFY_TIME",
		Length:    sizeZCNotifyTime,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSTATUS,
		Name:      "ZC_STATUS",
		Length:    sizeZCStatus,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPARCHANGE,
		Name:      "ZC_PAR_CHANGE",
		Length:    sizeZCParChange,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCLONGPARCHANGE,
		Name:      "ZC_LONGPAR_CHANGE",
		Length:    sizeZCLongParChange,
		Direction: DirectionServerToClient,
	})
	// M10: empty list packets emitted after the status burst. The three
	// list packets (inventory normal/equip, skill) are variable-length —
	// the wire length is encoded in the int16 packetLength slot and the
	// trailing flexible array may be zero entries — so we register
	// VariableLength (-1) and rely on the codec to read the wire length
	// at decode time. ZC_SHORTCUT_KEY_LIST (0x02b9) is fixed at 191
	// bytes (2-byte opcode + 27 zero-filled hotkey slots of 7 bytes
	// each) regardless of how many slots the client actually has
	// configured; the slot count is encoded in the PACKETVER's struct
	// shape, not the wire data.
	db.Register(Definition{
		ID:        HeaderZCINVENTORYITEMLISTNORMAL,
		Name:      "ZC_INVENTORY_ITEMLIST_NORMAL",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCINVENTORYITEMLISTEQUIP,
		Name:      "ZC_INVENTORY_ITEMLIST_EQUIP",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSKILLINFOLIST,
		Name:      "ZC_SKILLINFO_LIST",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSHORTCUTKEYLIST,
		Name:      "ZC_SHORTCUT_KEY_LIST",
		Length:    sizeZCShortcutKeyList,
		Direction: DirectionServerToClient,
	})
	// M11: ZC_NOTIFY_CHAT is variable length (chat text + trailing NUL);
	// ZC_ACTION_RESPONSE is fixed 11 bytes (cmd + GID + action + targetGID).
	db.Register(Definition{
		ID:        HeaderZCNOTIFYCHAT,
		Name:      "ZC_NOTIFY_CHAT",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCACTIONRESPONSE,
		Name:      "ZC_ACTION_RESPONSE",
		Length:    sizeZCActionResponse,
		Direction: DirectionServerToClient,
	})
	// M12: ZC_CHANGE_DIRECTION (fixed 9 bytes) + ZC_EMOTION (fixed 7 bytes)
	// — direction / emotion single-player echo.
	db.Register(Definition{
		ID:        HeaderZCCHANGEDIR,
		Name:      "ZC_CHANGE_DIRECTION",
		Length:    sizeZCChangeDir,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCEMOTION,
		Name:      "ZC_EMOTION",
		Length:    sizeZCEmotion,
		Direction: DirectionServerToClient,
	})
	// M13: ZC_ACK_REQNAME (fixed 30 bytes) — name lookup response.
	db.Register(Definition{
		ID:        HeaderZCACKREQNAME,
		Name:      "ZC_ACK_REQNAME",
		Length:    sizeZCAckReqName,
		Direction: DirectionServerToClient,
	})
	// M14: ZC_SET_UNIT_IDLE (fixed 107 bytes) — NPC entity spawn.
	// Same struct layout as ZC_SPAWN_UNIT (0x09fe) but uses opcode
	// 0x09ff per rAthena's clif_set_unit_idle path.
	db.Register(Definition{
		ID:        HeaderZCSETUNITIDLE,
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
		ID:        HeaderZCUNITWALKING,
		Name:      "ZC_UNIT_WALKING",
		Length:    sizeZCUnitWalking,
		Direction: DirectionServerToClient,
	})
	// M15: NPC dialog interaction — CZ_CONTACTNPC (fixed 7 bytes),
	// CZ_REQNEXTSCRIPT (fixed 6 bytes), CZ_CLOSE_DIALOG (fixed 6 bytes).
	db.Register(Definition{
		ID:        HeaderCZCONTACTNPC,
		Name:      "CZ_CONTACTNPC",
		Length:    sizeCZContactNPC,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQNEXTSCRIPT,
		Name:      "CZ_REQNEXTSCRIPT",
		Length:    sizeCZReqNextScript,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZCLOSEDIALOG,
		Name:      "CZ_CLOSE_DIALOG",
		Length:    sizeCZCloseDialog,
		Direction: DirectionClientToServer,
	})
	// M15: ZC_SAY_DIALOG2 (variable length), ZC_WAIT_DIALOG2 (fixed 7 bytes),
	// ZC_CLOSE_DIALOG (fixed 6 bytes).
	db.Register(Definition{
		ID:        HeaderZCSAYDIALOG2,
		Name:      "ZC_SAY_DIALOG2",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCWAITDIALOG2,
		Name:      "ZC_WAIT_DIALOG2",
		Length:    sizeZCWaitDialog2,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCCLOSEDIALOG,
		Name:      "ZC_CLOSE_DIALOG",
		Length:    sizeZCCloseDialog,
		Direction: DirectionServerToClient,
	})
	// M16: NPC shop interaction — CZ_ACK_SELECT_DEALTYPE (fixed 7 bytes),
	// CZ_PC_PURCHASE_ITEMLIST (variable length), ZC_SELECT_DEALTYPE
	// (fixed 6 bytes), ZC_PC_PURCHASE_ITEMLIST (variable length),
	// ZC_PC_PURCHASE_RESULT (fixed 3 bytes).
	db.Register(Definition{
		ID:        HeaderCZACKSELECTDEALTYPE,
		Name:      "CZ_ACK_SELECT_DEALTYPE",
		Length:    sizeCZAckSelectDealtype,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZPCPURCHASEITEMLIST,
		Name:      "CZ_PC_PURCHASE_ITEMLIST",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCSELECTDEALTYPE,
		Name:      "ZC_SELECT_DEALTYPE",
		Length:    sizeZCSelectDealtype,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPCPURCHASEITEMLIST,
		Name:      "ZC_PC_PURCHASE_ITEMLIST",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPCPURCHASERESULT,
		Name:      "ZC_PC_PURCHASE_RESULT",
		Length:    sizeZCPCPurchaseResult,
		Direction: DirectionServerToClient,
	})
	// P2B: shop sell flow. CZ_PC_SELL_ITEMLIST (variable, the per-
	// entry size is 4), ZC_PC_SELL_ITEMLIST (variable, per-item 10),
	// ZC_PC_SELL_RESULT (fixed 3). See rathena/src/map/clif_packetdb.hpp.
	db.Register(Definition{
		ID:        HeaderCZPCSELLITEMLIST,
		Name:      "CZ_PC_SELL_ITEMLIST",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCPCSELLITEMLIST,
		Name:      "ZC_PC_SELL_ITEMLIST",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCPCSELLRESULT,
		Name:      "ZC_PC_SELL_RESULT",
		Length:    sizeZCPCSellResult,
		Direction: DirectionServerToClient,
	})
	// M18: ZC_NOTIFY_ACT (fixed 34 bytes) — damage / sit / stand notification.
	// ZC_NOTIFY_VANISH (fixed 7 bytes) — entity death / despawn notification.
	db.Register(Definition{
		ID:        HeaderZCNOTIFYACT,
		Name:      "ZC_NOTIFY_ACT",
		Length:    sizeZCNotifyAct,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYVANISH,
		Name:      "ZC_NOTIFY_VANISH",
		Length:    sizeZCNotifyVanish,
		Direction: DirectionServerToClient,
	})
	// P2A: inventory equip / use packet family — three C→S requests
	// and three S→C acks. See the per-constant source citations above
	// for the rAthena packetdb / packets.hpp / packets_struct.hpp
	// lines that pin each opcode and on-wire size to PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderCZUSEITEM2,
		Name:      "CZ_USE_ITEM2",
		Length:    sizeCZUseItem2,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQWEAREQUIPV5,
		Name:      "CZ_REQ_WEAR_EQUIP_V5",
		Length:    sizeCZReqWearEquipV5,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQTAKEOFFEQUIP,
		Name:      "CZ_REQ_TAKEOFF_EQUIP",
		Length:    sizeCZReqTakeoffEquip,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCREQWEAREQUIPACKV5,
		Name:      "ZC_REQ_WEAR_EQUIP_ACK_V5",
		Length:    sizeZCReqWearEquipAckV5,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCREQTAKEOFFEQUIPACK,
		Name:      "ZC_REQ_TAKEOFF_EQUIP_ACK",
		Length:    sizeZCReqTakeoffEquipAck,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCUSEITEMACK2,
		Name:      "ZC_USE_ITEM_ACK2",
		Length:    sizeZCUseItemAck2,
		Direction: DirectionServerToClient,
	})
	// P2C: stats & leveling — CZ_STATUS_CHANGE (stat allocation request)
	// plus the two server replies. See the per-constant source citations
	// above (clif.cpp:12714 / clif.cpp:4283 / packets.hpp:1120) for the
	// rAthena packetdb lines pinning each opcode to PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderCZSTATUSCHANGE,
		Name:      "CZ_STATUS_CHANGE",
		Length:    sizeCZStatusChange,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCSTATUSCHANGEACK,
		Name:      "ZC_STATUS_CHANGE_ACK",
		Length:    sizeZCStatusChangeAck,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYEFFECT,
		Name:      "ZC_NOTIFY_EFFECT",
		Length:    sizeZCNotifyEffect,
		Direction: DirectionServerToClient,
	})
	// P3b-2: skill usage family (CZ_USE_SKILL2 + ZC_NOTIFY_SKILL +
	// ZC_ACK_TOUSESKILL). See the per-constant source citations above
	// (clif_shuffle.hpp:4750 + packets_struct.hpp:2448-2461, 4658-4671)
	// for the rAthena packetdb lines pinning each opcode and on-wire
	// size to PACKETVER 20250604.
	db.Register(Definition{
		ID:        HeaderCZUSESKILL,
		Name:      "CZ_USE_SKILL2",
		Length:    sizeCZUseSkill2,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYSKILL,
		Name:      "ZC_NOTIFY_SKILL",
		Length:    sizeZCNotifySkill,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCACKTOUSESKILL,
		Name:      "ZC_ACK_TOUSESKILL",
		Length:    sizeZCAckToUseSkill,
		Direction: DirectionServerToClient,
	})
	// P3c: ground item drop notification. rAthena binds opcode 0x0ADD
	// with size 22 for PACKETVER >= 20180418 (clif_packetdb.hpp:1921);
	// the v5 layout adds <showDropEffect> + <dropEffectMode> on top of
	// the v4 20-byte frame. See map_item_drop.go for the full struct
	// documentation and per-field citations.
	db.Register(Definition{
		ID:        HeaderZCItemFallEntry,
		Name:      "ZC_ITEM_FALL_ENTRY",
		Length:    sizeZCItemFallEntry,
		Direction: DirectionServerToClient,
	})

	return db
}
