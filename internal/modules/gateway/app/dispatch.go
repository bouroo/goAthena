package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"time"

	"github.com/panjf2000/gnet/v2"

	storagedomain "github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
	dialogdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	partydomain "github.com/bouroo/goAthena/internal/modules/social/party/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/equip"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// mapHandler is one entry in the map-server dispatch table and the function that
// processes a complete frame of that opcode.
//
// Most map packets are fixed-length: size is their constant byte count. The few
// variable-length packets (e.g. CZ_INPUT_EDITDLGSTR 0x01d5) leave size at 0 and
// set frameSize, which derives the full frame length from the buffered bytes and
// reports whether the whole frame has arrived — mirroring the fixed-size wait in
// OnTraffic without disturbing it.
type mapHandler struct {
	// size is the fixed frame byte count for constant-length packets. Ignored
	// when frameSize is non-nil.
	size int
	// frameSize, when set, derives the full frame length for a variable-length
	// packet from the buffered bytes (its on-wire uint16 length at offset 2) and
	// returns false until the whole frame has buffered. nil means use size.
	frameSize func(c gnet.Conn) (int, bool)
	// fn receives the authed identity resolved on the eventloop so handlers never
	// read c.Context() off-loop, where gnet's conn.release() races on close.
	fn func(s *MapServer, c gnet.Conn, auth *mapAuth, frame []byte)
}

// frameLen returns the full frame byte count for this opcode and whether that
// many bytes are already buffered. Fixed-length packets compare size directly;
// variable-length packets delegate to frameSize. Callers (OnTraffic) read this
// once and then Next(n), keeping the size decision in one place.
func (h mapHandler) frameLen(c gnet.Conn) (n int, ready bool) {
	if h.frameSize != nil {
		return h.frameSize(c)
	}
	return h.size, c.InboundBuffered() >= h.size
}

// mapHandlers is the opcode→handler table the map server dispatches against.
// Fixed-length packets carry their constant size; variable-length packets carry
// a frameSize func instead. A connection's first packet is always CZ_ENTER (the
// trust gate); the rest are only valid post-auth.
func mapHandlers() map[uint16]mapHandler {
	return map[uint16]mapHandler{
		0x0072:                               {size: czEnterSize, fn: (*MapServer).handleEnterFrame},
		0x007d:                               {size: 2, fn: (*MapServer).handleLoadEndAck},
		0x0085:                               {size: 5, fn: (*MapServer).handleRequestMove},
		0x0089:                               {size: 7, fn: (*MapServer).handleActionRequest},                                  // CZ_ACTION_REQUEST
		0x0090:                               {size: 7, fn: (*MapServer).handleContactNPC},                                     // CZ_CONTACT_NPC (NPC click)
		ropacket.HeaderCZRESTART:             {size: 3, fn: (*MapServer).handleRestart},                                        // CZ_RESTART (respawn / return to char-select)
		ropacket.HeaderCZSTATUSCHANGE:        {size: 5, fn: (*MapServer).handleStatusChange},                                   // CZ_STATUS_CHANGE (stat allocation)
		0x00b8:                               {size: 7, fn: (*MapServer).handleChooseMenu},                                     // CZ_CHOOSE_MENU
		0x00b9:                               {size: 6, fn: (*MapServer).handleReqNextScript},                                  // CZ_REQ_NEXT_SCRIPT
		0x0143:                               {size: 10, fn: (*MapServer).handleInputEditDlg},                                  // CZ_INPUT_EDITDLG
		0x01d5:                               {frameSize: variableFrameSize, fn: (*MapServer).handleInputEditDlgStr},           // CZ_INPUT_EDITDLGSTR (variable length)
		0x0146:                               {size: 6, fn: (*MapServer).handleCloseDialog},                                    // CZ_CLOSE_DIALOG
		ropacket.HeaderCZACKSELECTDEALTYPE:   {size: 7, fn: (*MapServer).handleAckSelectDealtype},                              // CZ_ACK_SELECT_DEALTYPE (NPC shop open)
		ropacket.HeaderCZPCPURCHASEITEMLIST:  {frameSize: variableFrameSize, fn: (*MapServer).handlePurchaseItemList},          // CZ_PC_PURCHASE_ITEMLIST (variable)
		ropacket.HeaderCZPCSELLITEMLIST:      {frameSize: variableFrameSize, fn: (*MapServer).handleSellItemList},              // CZ_PC_SELL_ITEMLIST (variable)
		0x0362:                               {size: 6, fn: (*MapServer).handleItemPickup},                                     // CZ_ITEM_PICKUP @ 20250604
		0x0363:                               {size: 6, fn: (*MapServer).handleItemDrop},                                       // CZ_ITEM_DROP @ 20250604
		0x0439:                               {size: 8, fn: (*MapServer).handleUseItem},                                        // CZ_USE_ITEM2 @ 20250604 (cmd+index+AID)
		0x0438:                               {size: 10, fn: (*MapServer).handleUseSkill2},                                     // CZ_USE_SKILL2 @ 20250604 (clif_shuffle.hpp:4750)
		0x0af4:                               {size: 11, fn: (*MapServer).handleUseSkillToPos},                                 // CZ_USE_SKILL_TOPOS @ 20250604 (clif_packetdb.hpp:1905)
		ropacket.HeaderCZSKILLUP:             {size: 4, fn: (*MapServer).handleSkillUp},                                        // CZ_SKILLUP (skill learn)
		ropacket.HeaderCZTRADEREQUEST:        {size: 6, fn: (*MapServer).handleTradeRequest},                                   // CZ_TRADE_REQUEST 0x00e4 (cmd+targetGID)
		ropacket.HeaderCZTRADEACK:            {size: 3, fn: (*MapServer).handleTradeAck},                                       // CZ_TRADE_ACK 0x00e6 (cmd+type)
		ropacket.HeaderCZADDEXCHANGEITEM:     {size: 8, fn: (*MapServer).handleAddExchangeItem},                                // CZ_ADD_EXCHANGE_ITEM 0x00e8 (cmd+index+amount)
		ropacket.HeaderCZTRADEOK:             {size: 2, fn: (*MapServer).handleTradeOK},                                        // CZ_TRADE_OK 0x00eb (cmd only)
		ropacket.HeaderCZTRADECANCEL:         {size: 2, fn: (*MapServer).handleTradeCancel},                                    // CZ_TRADE_CANCEL 0x00ed (cmd only)
		ropacket.HeaderCZREQWEAREQUIPV5:      {size: 8, fn: (*MapServer).handleReqWearEquip},                                   // CZ_REQ_WEAR_EQUIP_V5 0x0998 (cmd+index+position)
		ropacket.HeaderCZREQTAKEOFFEQUIP:     {size: 4, fn: (*MapServer).handleReqTakeoffEquip},                                // CZ_REQ_TAKEOFF_EQUIP 0x00ab (cmd+index)
		ropacket.HeaderCZWHISPER:             {frameSize: variableFrameSize, fn: (*MapServer).handleWhisper},                   // CZ_WHISPER 0x0096 (variable)
		ropacket.HeaderCZGLOBALMESSAGE:       {frameSize: variableFrameSize, fn: (*MapServer).handleGlobalMessage},             // CZ_GLOBAL_MESSAGE 0x008c (variable)
		ropacket.HeaderCZGETCHARNAMEREQUEST:  {size: 6, fn: (*MapServer).handleGetCharNameRequest},                             // CZ_GETCHARNAMEREQUEST 0x0094
		ropacket.HeaderCZREQUESTTIME:         {size: 6, fn: (*MapServer).handleRequestTime},                                    // CZ_REQUEST_TIME 0x007e (clock ping)
		ropacket.HeaderCZREQEMOTION:          {size: 3, fn: (*MapServer).handleReqEmotion},                                     // CZ_REQ_EMOTION 0x00bf (emotion icon)
		ropacket.HeaderCZCHANGEDIR:           {size: 5, fn: (*MapServer).handleChangeDir},                                      // CZ_CHANGE_DIR 0x009b (facing)
		ropacket.HeaderCZPMIGNORE:            {size: 27, fn: (*MapServer).handlePMIgnore},                                      // CZ_PMIgnore 0x00cf (/ex /in)
		ropacket.HeaderCZSETTINGWHISPERSTATE: {size: 3, fn: (*MapServer).handleSettingWhisperState},                            // CZ_SETTING_WHISPER_STATE 0x00d0 (/exall /inall)
		ropacket.HeaderCZREQWHISPERLIST:      {size: 2, fn: (*MapServer).handleReqWhisperList},                                 // CZ_REQ_WHISPER_LIST 0x00d3 (/wl)
		ropacket.HeaderCZREQOPENSTORE2:       {size: ropacket.SizeCZReqOpenStore2, fn: (*MapServer).handleReqOpenStore2},       // CZ_REQ_OPENSTORE2 0x07e4 (cmd+accountName)
		ropacket.HeaderCZCLOSESTORE:          {size: 2, fn: (*MapServer).handleCloseStore},                                     // CZ_CLOSE_STORE 0x07e5 (cmd only)
		ropacket.HeaderCZMOVEITEMTOSTORE2:    {size: ropacket.SizeCZMoveItemToStore2, fn: (*MapServer).handleMoveItemToStore2}, // CZ_MOVE_ITEM_TO_STORE2 0x07e6 (cmd+index+amount)
		ropacket.HeaderCZMOVEITEMTOBODY2:     {size: ropacket.SizeCZMoveItemToBody2, fn: (*MapServer).handleMoveItemToBody2},   // CZ_MOVE_ITEM_TO_BODY2 0x07e7 (cmd+index+amount)
		// M11: party (group) family. Both create variants are dispatched because
		// the client picks one by build; CZ_CHANGE_GROUPEXPOPTION is leader-only.
		ropacket.HeaderCZMAKEGROUP:           {size: 26, fn: (*MapServer).handleMakeGroup},           // CZ_MAKE_GROUP 0x00f9 (cmd+name)
		ropacket.HeaderCZMAKEGROUP2:          {size: 28, fn: (*MapServer).handleMakeGroup},           // CZ_MAKE_GROUP2 0x01e8 (cmd+name+pickup+share)
		ropacket.HeaderCZREQJOINGROUP:        {size: 6, fn: (*MapServer).handleReqJoinGroup},         // CZ_REQ_JOIN_GROUP 0x00fc (cmd+AID)
		ropacket.HeaderCZJOINGROUP:           {size: 10, fn: (*MapServer).handleJoinGroup},           // CZ_JOIN_GROUP 0x00ff (cmd+partyID+flag)
		ropacket.HeaderCZREQLEAVEGROUP:       {size: 2, fn: (*MapServer).handleLeaveGroup},           // CZ_REQ_LEAVE_GROUP 0x0100 (cmd only)
		ropacket.HeaderCZCHANGEGROUPEXPOPT:   {size: 6, fn: (*MapServer).handleChangeGroupExpOption}, // CZ_CHANGE_GROUPEXPOPTION 0x0102 (cmd+expflag)
		ropacket.HeaderCZREQEXPELGROUPMEMBER: {size: 30, fn: (*MapServer).handleExpelGroupMember},    // CZ_REQ_EXPEL_GROUP_MEMBER 0x0103 (cmd+AID+name)
		// M11: friend family. Add is by display name; the reply names the inviter
		// pair; delete names the friend pair (all three clif_packetdb.hpp:257-263).
		ropacket.HeaderCZFRIENDSADD:    {size: 26, fn: (*MapServer).handleFriendsAdd},    // CZ_ADD_FRIENDS 0x0202 (cmd+name)
		ropacket.HeaderCZFRIENDSDELETE: {size: 10, fn: (*MapServer).handleFriendsRemove}, // CZ_DELETE_FRIENDS 0x0203 (cmd+AID+CID)
		ropacket.HeaderCZFRIENDSREPLY:  {size: 14, fn: (*MapServer).handleFriendsReply},  // CZ_ACK_REQ_ADD_FRIENDS 0x0208 (cmd+AID+CID+reply)
		// M11: guild family. Create/leave/ban/disorganize/invite/reply are
		// fixed-length; guild chat is length-prefixed. MenuInterface is the
		// guild-window permission poll (clif_packetdb.hpp:150-159, 171).
		ropacket.HeaderCZCREATEGUILD:        {size: ropacket.SizeCZCreateGuild, fn: (*MapServer).handleCreateGuild},           // CZ_REQ_MAKE_GUILD 0x0165 (cmd+charID+name)
		ropacket.HeaderCZREQJOINGUILD:       {size: ropacket.SizeCZReqJoinGuild, fn: (*MapServer).handleGuildInvite},          // CZ_REQ_JOIN_GUILD 0x0168 (cmd+AID+inviterAID+inviterCID)
		ropacket.HeaderCZJOINGUILD:          {size: ropacket.SizeCZJoinGuild, fn: (*MapServer).handleGuildReplyInvite},        // CZ_JOIN_GUILD 0x016b (cmd+guildID+answer)
		ropacket.HeaderCZREQLEAVEGUILD:      {size: ropacket.SizeCZGuildLeave, fn: (*MapServer).handleGuildLeave},             // CZ_REQ_LEAVE_GUILD 0x0159 (cmd+guildID+AID+CID+reason)
		ropacket.HeaderCZREQBANGUILD:        {size: ropacket.SizeCZGuildBan, fn: (*MapServer).handleGuildBan},                 // CZ_REQ_BAN_GUILD 0x015b (same shape)
		ropacket.HeaderCZREQDISORGANIZEGILD: {size: ropacket.SizeCZGuildBreak, fn: (*MapServer).handleGuildBreak},             // CZ_REQ_DISORGANIZE_GUILD 0x015d (cmd+key)
		ropacket.HeaderCZGUILDCHAT:          {frameSize: variableFrameSize, fn: (*MapServer).handleGuildChat},                 // CZ_GUILD_CHAT 0x017e (cmd+len+msg)
		ropacket.HeaderCZGUILDCHECKMASTER:   {size: ropacket.SizeCZGuildCheckMaster, fn: (*MapServer).handleGuildCheckMaster}, // CZ_REQ_GUILD_MENUINTERFACE 0x014d (cmd)
		// M11: mail (RODEX) family. The five refreshinbox opcodes share a
		// handler (clif_parse_Mail_refreshinbox); read/delete share a shape;
		// the two send opcodes are variable-length (strings follow the
		// fixed header); the name-check verbs share a shape.
		ropacket.HeaderCZOPENMAILBOX:     {size: ropacket.SizeCZOpenMailbox, fn: (*MapServer).handleOpenMailbox},         // CZ_OPEN_MAILBOX 0x09e8 (cmd+mail id.Q)
		ropacket.HeaderCZCLOSEMAILBOX:    {size: 2, fn: (*MapServer).handleCloseMailbox},                                 // CZ_CLOSE_MAILBOX 0x09e9 (no-op, clif_parse_dull)
		ropacket.HeaderCZREQREADMAIL:     {size: ropacket.SizeCZReadDeleteMail, fn: (*MapServer).handleReadMail},         // CZ_REQ_READ_MAIL 0x09ea (cmd+tab.B+mail id.Q)
		ropacket.HeaderCZREQNEXTMAILLIST: {size: ropacket.SizeCZOpenMailbox, fn: (*MapServer).handleOpenMailbox},         // CZ_REQ_NEXT_MAIL_LIST 0x09ee (refreshinbox)
		ropacket.HeaderCZREQREFRESHMAILL: {size: ropacket.SizeCZOpenMailbox, fn: (*MapServer).handleOpenMailbox},         // CZ_REQ_REFRESH_MAIL_LIST 0x09ef (refreshinbox)
		ropacket.HeaderCZREQZENYFROMMAIL: {size: ropacket.SizeCZGetAttach, fn: (*MapServer).handleCollectZeny},           // CZ_REQ_ZENY_FROM_MAIL 0x09f1 (cmd+mail id.Q+tab.B)
		ropacket.HeaderCZREQITEMFROMMAIL: {size: ropacket.SizeCZGetAttach, fn: (*MapServer).handleCollectItems},          // CZ_REQ_ITEM_FROM_MAIL 0x09f3 (cmd+mail id.Q+tab.B)
		ropacket.HeaderCZREQDELETEMAIL:   {size: ropacket.SizeCZReadDeleteMail, fn: (*MapServer).handleDeleteMail},       // CZ_REQ_DELETE_MAIL 0x09f5 (cmd+tab.B+mail id.Q)
		ropacket.HeaderCZREQCANCELWRITE:  {size: 2, fn: (*MapServer).handleCancelWriteMail},                              // CZ_REQ_CANCEL_WRITE_MAIL 0x0a03 (cmd)
		ropacket.HeaderCZREQADDITEMMAIL:  {size: ropacket.SizeCZMailItem, fn: (*MapServer).handleAddItemToMail},          // CZ_REQ_ADD_ITEM_TO_MAIL 0x0a04 (cmd+index.W+count.W)
		ropacket.HeaderCZREQREMOVEITEMMA: {size: ropacket.SizeCZMailItem, fn: (*MapServer).handleRemoveItemFromMail},     // CZ_REQ_REMOVE_ITEM_MAIL 0x0a06 (cmd+index.W+count.W)
		ropacket.HeaderCZREQOPENWRITEMAI: {size: ropacket.SizeCZOpenWriteMail, fn: (*MapServer).handleOpenWriteMail},     // CZ_REQ_OPEN_WRITE_MAIL 0x0a08 (cmd+name.24B)
		ropacket.HeaderCZCHECKRECEIVENAM: {size: ropacket.SizeCZOpenWriteMail, fn: (*MapServer).handleCheckReceiverName}, // CZ_CHECK_RECEIVE_CHARACTER_NAME 0x0a13 (cmd+name.24B)
		ropacket.HeaderCZREQWRITEMAIL:    {frameSize: variableFrameSize, fn: (*MapServer).handleWriteMail},               // CZ_REQ_WRITE_MAIL 0x09ec (variable)
		ropacket.HeaderCZREQWRITEMAIL2:   {frameSize: variableFrameSize, fn: (*MapServer).handleWriteMail},               // CZ_REQ_WRITE_MAIL2 0x0a6e (variable)
		ropacket.HeaderCZOPENMAILBOX2:    {size: ropacket.SizeCZOpenMailbox2, fn: (*MapServer).handleOpenMailbox},        // CZ_OPEN_MAILBOX2 0x0ac0 (cmd+mail id.Q+unknown.16B)
		ropacket.HeaderCZREFRESHMAILLIST: {size: ropacket.SizeCZOpenMailbox2, fn: (*MapServer).handleOpenMailbox},        // CZ_REQ_REFRESH_MAIL_LIST2 0x0ac1 (refreshinbox)
		ropacket.HeaderCZCHECKNAME2:      {size: ropacket.SizeCZCheckName2, fn: (*MapServer).handleCheckReceiverName},    // CZ_CHECKNAME2 0x0b97 (cmd+name.24B+own_char.B)
	}
}

// handleContactNPC starts an NPC dialog script on click (CZ_CONTACT_NPC 0x0090).
func (s *MapServer) handleContactNPC(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZContactNPC(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_CONTACT_NPC", "err", err)
		return
	}
	// Shop NPCs open the deal-type selector instead of a dialog (rAthena's
	// clif_parse_NpcClicked → npc_click shop branch): reply ZC_SELECT_DEALTYPE
	// and wait for CZ_ACK_SELECT_DEALTYPE.
	if s.shops != nil && s.shopStore != nil {
		if _, isShop := s.shopStore.ShopForNPC(context.Background(), req.AID); isShop {
			var buf bytes.Buffer
			_ = ropacket.SelectDealtypeResponse{NpcID: req.AID}.Encode(&buf) //nolint:errcheck // buffer write cannot fail
			_ = c.AsyncWrite(buf.Bytes(), nil)
			return
		}
	}
	s.content.StartDialog(auth.accountID, auth.charID, req.AID, gnetWriter{c: c})
}

// handleReqNextScript advances an active dialog (CZ_REQ_NEXT_SCRIPT 0x00b9).
func (s *MapServer) handleReqNextScript(_ gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Advance: true})
}

// handleChooseMenu delivers a menu selection (CZ_CHOOSE_MENU 0x00b8).
func (s *MapServer) handleChooseMenu(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZChooseMenu(frame)
	if err != nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Choice: uint8(req.Selected)}) //nolint:gosec // G115: -1→255 cancel.
}

// handleInputEditDlg delivers a numeric input (CZ_INPUT_EDITDLG 0x0143).
func (s *MapServer) handleInputEditDlg(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZInputEditDlg(frame)
	if err != nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Input: strconv.FormatInt(int64(req.Value), 10)})
}

// handleInputEditDlgStr delivers a text input (CZ_INPUT_EDITDLGSTR 0x01d5). The
// frame is already detached and length-resolved by the dispatcher; this mirrors
// the numeric handler, substituting the raw string value for a decimal string.
func (s *MapServer) handleInputEditDlgStr(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZInputEditDlgStr(frame)
	if err != nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Input: req.Value})
}

// variableFrameSize derives the full byte length of a length-prefixed variable
// packet by reading its uint16 total-length field at offset 2 — the rAthena
// convention for non-constant-length frames (CZ_INPUT_EDITDLGSTR 0x01d5 today).
// It returns false until the whole frame has buffered. A malformed length
// smaller than its own header resyncs over the 2-byte opcode (matching
// unhandledSkip), so the dispatcher never spins on a zero-length read.
func variableFrameSize(c gnet.Conn) (int, bool) {
	prefix, err := c.Peek(4)
	if err != nil {
		return 0, false // length field not fully buffered yet
	}
	n := int(binary.LittleEndian.Uint16(prefix[2:4]))
	if n < 4 {
		return 2, true // malformed length prefix: resync over the header
	}
	if c.InboundBuffered() < n {
		return 0, false // wait for the rest of the frame
	}
	return n, true
}

// handleCloseDialog cancels an active dialog (CZ_CLOSE_DIALOG 0x0146).
func (s *MapServer) handleCloseDialog(_ gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Cancel: true})
}

// CZ_ACK_SELECT_DEALTYPE type byte (rAthena clif_parse_NpcSelectDealType) and the
// ZC_PC_PURCHASE/SELL_RESULT result byte. The wire carries raw bytes, so they are
// defined here at the dispatch layer rather than in the packet encoder.
const (
	dealTypeBuy    uint8 = 0
	dealTypeSell   uint8 = 1
	dealTypeCancel uint8 = 2

	shopResultSuccess uint8 = 0
	shopResultFailed  uint8 = 1

	// CZ_RESTART (0x00b2) selector byte (pkg/ro/packet/map_parse.go):
	// 0x00 respawn at the save point, 0x01 return to the character-select screen.
	czRestartRespawn        uint8 = 0x00
	czRestartReturnToSelect uint8 = 0x01
	// ZC_RESTART_ACK (0x00b3) type byte (pkg/ro/packet/map_encode.go): 0 refuse,
	// 1 leave-for-char-select. Only the char-select branch sends the ack.
	zcRestartAckLeaveForSelect uint8 = 1
)

// handleAckSelectDealtype handles CZ_ACK_SELECT_DEALTYPE (0x00c5, 7B): the client
// picked Buy/Sell/Cancel on a shop NPC. It resolves the NPC GID to a shop name,
// threads that name for the following purchase/sell frames (which carry item
// entries, not the NPC id), and emits the priced buy list (Buy) or sell list
// (Sell). Cancel and unknown NPCs are no-ops that keep the connection alive.
func (s *MapServer) handleAckSelectDealtype(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_ACK_SELECT_DEALTYPE from unauthed conn")
		return
	}
	if s.shops == nil || s.shopStore == nil {
		s.log.Debug("map: shop not wired, ignoring CZ_ACK_SELECT_DEALTYPE")
		return
	}
	req, err := ropacket.ParseCZAckSelectDealType(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ACK_SELECT_DEALTYPE", "err", err)
		return
	}
	shopName, ok := s.shopStore.ShopForNPC(context.Background(), req.NpcID)
	if !ok {
		s.log.Debug("map: CZ_ACK_SELECT_DEALTYPE for non-shop NPC", "npc", req.NpcID)
		return
	}
	s.setOpenedShop(auth.charID, shopName)

	switch req.Type {
	case dealTypeBuy:
		s.writePurchaseItemList(c, shopName)
	case dealTypeSell:
		s.writeSellItemList(context.Background(), c, shopName, auth.accountID, auth.charID)
	default:
		// dealTypeCancel (2) and any unknown value: close the deal window, no list.
	}
}

// handlePurchaseItemList handles CZ_PC_PURCHASE_ITEMLIST (0x00c8, variable): the
// player's buy request against the last-opened shop. Each entry is one
// (itemId, amount); ShopService charges zeny and grants the item. On any entry
// error it emits ZC_PC_PURCHASE_RESULT(failed) and keeps the connection alive.
// Partial success is not rolled back (documented simplification: earlier entries
// in the same request that succeeded stay bought).
//
// Each granted entry first emits ZC_ITEM_PICKUP_ACK for the row it landed in,
// then the request's single result byte — rAthena's order: npc_buylist grants
// with pc_additem (npc.cpp:2921-2928), pc_additem calls clif_additem at both
// grant sites (pc.cpp:6079 stacking, :6110 fresh), and clif_additem emits the
// add-item frame with the destination slot (clif.cpp:2836-2901); the buy result
// byte follows (clif_npc_buy_result, clif.cpp:12343). Without that frame the
// client never learns its bag changed and keeps a stale grid.
func (s *MapServer) handlePurchaseItemList(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_PC_PURCHASE_ITEMLIST from unauthed conn")
		return
	}
	if s.shops == nil {
		s.log.Debug("map: shop not wired, ignoring CZ_PC_PURCHASE_ITEMLIST")
		return
	}
	shopName, ok := s.openedShop(auth.charID)
	if !ok {
		s.log.Debug("map: CZ_PC_PURCHASE_ITEMLIST with no opened shop", "gid", auth.charID)
		s.writePurchaseResult(c, shopResultFailed)
		return
	}
	req, err := ropacket.ParseCZPCPurchaseItemList(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_PC_PURCHASE_ITEMLIST", "err", err)
		s.writePurchaseResult(c, shopResultFailed)
		return
	}
	ctx := context.Background()
	result := shopResultSuccess
	for _, e := range req.Entries {
		granted, err := s.shops.Buy(ctx, auth.charID, shopName, e.ItemID, int(e.Amount))
		if err != nil {
			s.log.Warn("map: shop buy", "shop", shopName, "item", e.ItemID, "amount", e.Amount, "err", err)
			result = shopResultFailed
			break
		}
		slot, ok := s.clientIndexForItem(ctx, auth.accountID, auth.charID, granted.ID)
		if !ok {
			// The row was just written, so this is an internal inconsistency
			// (a failed reload), not a client error. Suppressing the frame is
			// deliberate: a wrong slot would move the client's item, a missing
			// frame only leaves that slot stale until the next list verb.
			s.log.Error("map: granted row has no resolvable bag slot", "nameID", e.ItemID, "row", granted.ID)
			continue
		}
		s.writeItemPickupAck(c, slot, e.ItemID, e.Amount)
	}
	s.writePurchaseResult(c, result)
}

// handleSellItemList handles CZ_PC_SELL_ITEMLIST (0x00c9, variable): the player's
// sell request. Each entry is (index, amount) where index is the client inventory
// slot. Two simplifications are documented honestly rather than faked:
//
//   - index resolution: the client slot is assumed to match LoadByChar's list
//     order (rAthena assigns client indices during the init burst and they are
//     not guaranteed equal to DB row order). The same assumption backs the sell
//     list emitted on open, so index ↔ item stays consistent within a session.
//   - pricing: the shop pays its catalog SellPrice (real rAthena uses the
//     item_db sell price + overcharge, not just the shop catalog).
//
// On any error it emits ZC_PC_SELL_RESULT(failed) and keeps the connection alive.
//
// Each sold entry first emits ZC_DELETE_ITEM_FROM_BODY(deleteType 6, "Item sold")
// for the slot it removed, then the request's single result byte — rAthena's
// order: npc_selllist calls pc_delitem (npc.cpp:3090-3114), which calls
// clif_delitem (pc.cpp:6170 → clif.cpp:2915-2928), and the sell result byte
// follows (clif_npc_sell_result, clif.cpp:12352).
func (s *MapServer) handleSellItemList(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_PC_SELL_ITEMLIST from unauthed conn")
		return
	}
	if s.shops == nil {
		s.log.Debug("map: shop not wired, ignoring CZ_PC_SELL_ITEMLIST")
		return
	}
	shopName, ok := s.openedShop(auth.charID)
	if !ok {
		s.log.Debug("map: CZ_PC_SELL_ITEMLIST with no opened shop", "gid", auth.charID)
		s.writeSellResult(c, shopResultFailed)
		return
	}
	req, err := ropacket.ParseCZPCSellItemList(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_PC_SELL_ITEMLIST", "err", err)
		s.writeSellResult(c, shopResultFailed)
		return
	}
	ctx := context.Background()
	items, err := s.inv.LoadByChar(ctx, auth.accountID, auth.charID)
	if err != nil {
		s.log.Error("map: load inventory for sell", "err", err)
		s.writeSellResult(c, shopResultFailed)
		return
	}
	result := shopResultSuccess
	for _, e := range req.Entries {
		row := int(ropacket.ServerIndex(e.Index))
		if row >= len(items) {
			result = shopResultFailed
			break
		}
		it := items[row]
		if it.IsEquipped() {
			result = shopResultFailed
			break
		}
		price, ok := s.shops.SellPriceFor(shopName, it.NameID)
		if !ok {
			result = shopResultFailed
			break
		}
		if err := s.shops.Sell(ctx, auth.charID, it.ID, it.NameID, int(e.Amount), price); err != nil {
			s.log.Warn("map: shop sell", "shop", shopName, "item", it.NameID, "amount", e.Amount, "err", err)
			result = shopResultFailed
			break
		}
		// Re-sync the bag grid before the result byte: rAthena removes with
		// pc_delitem → clif_delitem (ZC_DELETE_ITEM_FROM_BODY, deleteType 6 =
		// "Item sold") and only then sends the sell result (npc.cpp:3090-3114 →
		// clif_npc_sell_result, clif.cpp:12352). The index echoed is the client
		// slot the request carried: npc_selllist passes pc_delitem the SERVER
		// row (item_list[i].index - 2) and clif_delitem writes it back through
		// client_index (clif.cpp:2921), so the value round-trips unchanged.
		s.writeItemDelete(c, ropacket.DeleteItemFromBodyResponse{
			DeleteType: ropacket.DeleteTypeItemSold,
			Index:      e.Index,
			Count:      int16(e.Amount), //nolint:gosec // G115: wire count is int16; player-bounded stack count.
		})
	}
	s.writeSellResult(c, result)
}

// writePurchaseItemList emits ZC_PC_PURCHASE_ITEMLIST: the shop's priced buy
// catalog. ItemType/ViewSprite/Location come from item_db, not the shop catalog,
// so they are zero until the item_db loader resolves them (M5b); Price ==
// DiscountPrice (no discount model yet).
func (s *MapServer) writePurchaseItemList(c gnet.Conn, shopName string) {
	items, ok := s.shops.CatalogItems(shopName)
	if !ok {
		return // shop vanished between open and list; nothing to send
	}
	buy := make([]ropacket.ShopBuyItem, 0, len(items))
	for _, it := range items {
		price := uint32(it.Price) //nolint:gosec // G115: catalog prices are non-negative zeny; int32 is the domain's zeny type.
		buy = append(buy, ropacket.ShopBuyItem{
			ItemID:        it.NameID,
			Price:         price,
			DiscountPrice: price,
		})
	}
	resp := ropacket.PurchaseItemListResponse{Items: buy}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_PC_PURCHASE_ITEMLIST", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeSellItemList emits ZC_PC_SELL_ITEMLIST: the player's inventory priced for
// resale. Index is the CLIENT index of the LoadByChar row (ropacket.ClientIndex,
// server row + 2) — the same index space the client echoes back in
// CZ_PC_SELL_ITEMLIST, which handleSellItemList converts with ServerIndex. Price
// == Overcharge (no overcharge model yet). Only items the shop trades appear
// (pricing simplification, see handleSellItemList).
func (s *MapServer) writeSellItemList(ctx context.Context, c gnet.Conn, shopName string, accountID, charID uint32) {
	items, err := s.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		s.log.Error("map: load inventory for sell list", "err", err)
		return
	}
	sell := make([]ropacket.ShopSellItem, 0, len(items))
	for i, it := range items {
		if it.IsEquipped() {
			continue
		}
		sellPrice, ok := s.shops.SellPriceFor(shopName, it.NameID)
		if !ok {
			continue
		}
		price := uint32(sellPrice) //nolint:gosec // G115: catalog sell prices are non-negative zeny; int32 is the domain's zeny type.
		sell = append(sell, ropacket.ShopSellItem{
			Index:      ropacket.ClientIndex(uint16(i)), //nolint:gosec // G115: row count bounded by MAX_INVENTORY
			Price:      price,
			Overcharge: price,
		})
	}
	resp := ropacket.SellItemListResponse{Items: sell}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_PC_SELL_ITEMLIST", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// clientIndexForItem resolves the client-facing bag slot of the inventory row
// with the given id. The slot is that row's position in the ordered LoadByChar
// list — the same list writeInventoryLists assigns client indices from
// (ropacket.ClientIndex, server row + 2), so a row written by a grant and the
// index the client addresses it by cannot diverge.
//
// A miss is not a client error: the caller has just written the row, so failing
// to find it means the reload failed or the row vanished. Callers suppress their
// re-sync frame rather than invent a slot — a wrong slot moves the client's item
// to the wrong cell, while a missing frame only leaves that slot stale until the
// next list verb re-sends it.
func (s *MapServer) clientIndexForItem(ctx context.Context, accountID, charID uint32, id invdomain.ItemID) (uint16, bool) {
	if s.inv == nil {
		return 0, false
	}
	items, err := s.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		s.log.Error("map: load inventory to resolve a granted slot", "err", err)
		return 0, false
	}
	for row, it := range items {
		if it.ID == id {
			return ropacket.ClientIndex(uint16(row)), true //nolint:gosec // G115: row count bounded by MAX_INVENTORY
		}
	}
	return 0, false
}

// writeItemPickupAck emits ZC_ITEM_PICKUP_ACK for one granted inventory row: the
// bag slot the client must place the item in and the amount granted. Every grant
// path (a shop buy, a floor pickup) sends exactly this frame with result 0,
// mirroring clif_additem (clif.cpp:2836-2901) — so both routes to a bag slot
// share one construction and cannot drift from each other.
func (s *MapServer) writeItemPickupAck(c gnet.Conn, slot uint16, nameID uint32, count uint16) {
	resp := ropacket.ItemPickupAckResponse{
		Index:        slot,
		Count:        count,
		NameID:       nameID,
		IsIdentified: 1,
		Result:       0, // success
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_ITEM_PICKUP_ACK", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeItemDelete emits ZC_DELETE_ITEM_FROM_BODY, the frame that removes or
// decrements a bag slot on the client (clif_delitem, clif.cpp:2915-2928).
func (s *MapServer) writeItemDelete(c gnet.Conn, resp ropacket.DeleteItemFromBodyResponse) {
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_DELETE_ITEM_FROM_BODY", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writePurchaseResult emits ZC_PC_PURCHASE_RESULT (0=success, 1=failed).
func (s *MapServer) writePurchaseResult(c gnet.Conn, result uint8) {
	resp := ropacket.PurchaseResultResponse{Result: result}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_PC_PURCHASE_RESULT", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeSellResult emits ZC_PC_SELL_RESULT (0=success, 1=failed).
func (s *MapServer) writeSellResult(c gnet.Conn, result uint8) {
	resp := ropacket.SellResultResponse{Result: result}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_PC_SELL_RESULT", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleEnterFrame wraps handleEnter to satisfy the dispatch signature (the
// frame is already detached from gnet's ring buffer by the caller).
func (s *MapServer) handleEnterFrame(c gnet.Conn, auth *mapAuth, frame []byte) {
	s.handleEnter(c, auth, frame)
}

// handleLoadEndAck handles CZ_NOTIFY_ACTORINIT (0x007d, 2B cmd-only). This is
// the signal the client finished loading the map; rAthena replies with the
// inventory/skill/hotkey init burst. The burst is: ZC_INVENTORY_START →
// ZC_INVENTORY_ITEMLIST_NORMAL → ZC_INVENTORY_ITEMLIST_EQUIP → ZC_INVENTORY_END.
// The item lists are populated from the character's real inventory rows
// (writeInventoryLists); before Phase 42 they were the empty forms, which left
// the client's bag grid permanently empty for a character who owned items.
func (s *MapServer) handleLoadEndAck(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		s.log.Warn("map: LoadEndAck from unauthed conn")
		return
	}
	// Coalesce the whole burst into one AsyncWrite to avoid per-frame syscalls.
	var burst []byte
	burst = append(burst, ropacket.EncodeInventoryStart()...)
	burst = s.writeInventoryLists(burst, auth)
	burst = append(burst, ropacket.EncodeInventoryEnd()...)
	if e, err := s.world.Get(worlddomain.EntityID(auth.charID)); err == nil {
		burst = s.writeSkillInfoList(burst, e)
	}
	// Party restore, mirroring rAthena's LoadEndAck tail: it sends
	// ZC_PARTY_CONFIG (clif_partyinvitationstate) unconditionally, then — only
	// when the char is in a party — the roster and option frames via
	// party_send_movemap (src/map/clif.cpp:11014, :10819-10823). Without the
	// roster a client that entered the map already grouped shows an empty
	// party window until the next membership change.
	burst = append(burst, encodePartyConfig(0)...)
	if s.party != nil {
		if p, perr := s.party.GetByMember(context.Background(), auth.charID); perr == nil {
			if members, merr := s.party.Members(context.Background(), p.ID); merr == nil {
				burst = s.appendGroupList(burst, p, members)
			}
		}
	}
	// Friend restore, mirroring clif_friendslist_send's LoadEndAck tail
	// (clif.cpp:15355-15391): the whole list first, then one online
	// ZC_FRIENDS_STATE per friend the map registry reports connected.
	if s.friend != nil {
		if friends, ferr := s.friend.List(context.Background(), auth.charID); ferr == nil {
			burst = s.appendFriendsList(burst, friends)
		}
		s.notifyFriendsOnline(auth.charID, auth.accountID, playerName(s.world, auth.charID), true)
	}
	// Guild restore, mirroring the LoadEndAck guild tail (clif_parse_LoadEndAck
	// → clif_guild_send_basicinfo/memberlist): belong-info, guild-info, and
	// roster, so a char entering while garrisoned sees their guild window
	// populated. A char without a guild gets nothing (rAthena skips on null
	// guild).
	if s.guild != nil {
		if g, gerr := s.guild.GetByMember(context.Background(), auth.charID); gerr == nil {
			burst = s.appendGuildBurst(burst, g)
		}
	}
	// Mail icon restore, mirroring rAthena's LoadEndAck tail (clif.cpp
	// :11176 → clif_Mail_new): the unread-mail icon is sent on entry so the
	// client's mail button shows the badge. The full inbox list is NOT sent
	// here (rAthena requests it lazily on window open via
	// CZ_REQ_REFRESH_MAIL_LIST).
	if s.mail != nil {
		if n, merr := s.mail.UnreadCount(context.Background(), auth.charID); merr == nil && n > 0 {
			var icon bytes.Buffer
			if ierr := ropacket.EncodeZCNotifyUnreadMail(&icon, true); ierr == nil {
				burst = append(burst, icon.Bytes()...)
			}
		}
	}
	_ = c.AsyncWrite(burst, nil)
	s.log.Debug("map: client load complete (inventory + skill init sent)", "aid", auth.accountID, "gid", auth.charID)
}

// handleRestart processes CZ_RESTART (0x00b2, 3B): the client's respawn-or-return-
// to-character-select request.
//
// Type 0x00 (respawn): cancel any pending death-respawn timer (so the button and
// the ArmRespawn timer never both fire) and respawn at the save point.
// RespawnPlayer's OnRespawn/OnStatChange hooks relocate the client
// (ZC_ACCEPT_ENTER to the requester) and restore its HP/SP bar (ZC_PAR_CHANGE);
// no ZC_RESTART_ACK is sent — that packet is char-select-only. The connection
// stays alive: the player relocates within the map-server.
//
// Type 0x01 (return to character select): persist + despawn via LeaveMap, send
// ZC_RESTART_ACK type=1 so the client leaves for the char-server, then close the
// conn. OnClose fires on the close and is a clean no-op: LeaveMap is idempotent on
// an already-removed entity. Best-effort on the leave path — a LeaveMap failure is
// logged, the ack is still sent, and the conn still closes.
func (s *MapServer) handleRestart(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZRestart(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_RESTART", "err", err)
		return
	}
	switch req.Type {
	case czRestartRespawn:
		s.world.CancelRespawn(auth.charID)
		if err := s.world.RespawnPlayer(auth.charID); err != nil {
			s.log.Warn("map: respawn on CZ_RESTART", "gid", auth.charID, "err", err)
			return
		}
	case czRestartReturnToSelect:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.world.LeaveMap(ctx, auth.charID); err != nil {
			s.log.Error("map: leave world on CZ_RESTART", "gid", auth.charID, "err", err)
		}
		s.writeRestartAck(c, zcRestartAckLeaveForSelect)
		if err := c.Close(); err != nil {
			s.log.Warn("map: close conn on CZ_RESTART", "gid", auth.charID, "err", err)
		}
	default:
		s.log.Warn("map: unknown CZ_RESTART type", "type", req.Type, "gid", auth.charID)
	}
}

// handleSkillUp handles CZ_SKILLUP (0x0112): the client spends one skill point to
// raise one learned-skill level, gated by the job's skill tree. On success it sends
// ZC_SKILLINFO_UPDATE (11B) plus a ParChange(SPSkillPoint). On failure it drops
// silently — no reply packet, connection stays open — per rAthena convention.
func (s *MapServer) handleSkillUp(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZSkillUp(frame)
	if err != nil {
		s.log.Debug("map: parse CZ_SKILLUP", "err", err)
		return
	}
	newLevel, spCost, rng, upgradable, err := s.skills.LearnSkill(context.Background(), auth.charID, req.SkillID)
	if err != nil {
		s.log.Debug("map: LearnSkill", "gid", auth.charID, "skillID", req.SkillID, "err", err)
		return
	}
	// Build buffered write: ZC_SKILLINFO_UPDATE + ParChange(SPSkillPoint).
	spU16 := int16(spCost) //nolint:gosec // spCost bounded by skill_db (per-level SP table)
	var buf bytes.Buffer
	_ = ropacket.SkillInfoUpdateResponse{
		SkillID:        req.SkillID,
		Level:          newLevel,
		SP:             spU16,
		Range2:         rng,
		UpgradableFlag: upgradable,
	}.Encode(&buf)
	e, err := s.world.Get(worlddomain.EntityID(auth.charID))
	if err == nil {
		points := int32(e.SkillPoint) //nolint:gosec // points is small; ParChange count is int32
		_ = ropacket.ParChangeResponse{VarID: ropacket.SPSkillPoint, Count: points}.Encode(&buf)
	}
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// writeRestartAck emits ZC_RESTART_ACK (0x00b3): Type=1 lets the client leave for
// the character-select screen, Type=0 would refuse (kept configurable for the
// future refuse path). Only the char-select branch of CZ_RESTART sends it; the
// respawn branch relies on RespawnPlayer's notifications. On an encode error it
// logs — the caller closes the conn regardless.
func (s *MapServer) writeRestartAck(c gnet.Conn, ackType uint8) {
	resp := ropacket.RestartAckResponse{Type: ackType}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_RESTART_ACK", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleRequestMove handles CZ_REQUEST_MOVE (0x0085, 5B): parse the 3-byte
// packed destination, move the entity in the world, and reply
// ZC_NOTIFY_PLAYERMOVE. Full AOI broadcast to neighbors lands with the
// connection-registry in M4b.
func (s *MapServer) handleRequestMove(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_REQUEST_MOVE from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZRequestMove(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQUEST_MOVE", "err", err)
		return
	}
	gid := worlddomain.EntityID(auth.charID)
	// Source position for the move response (before the move).
	src, _ := s.world.Get(gid)
	dest := worlddomain.Position{X: req.DestX, Y: req.DestY}
	if err := s.world.MoveEntity(gid, dest); err != nil {
		s.log.Warn("map: move entity", "gid", auth.charID, "err", err)
		return
	}
	resp := ropacket.MapNotifyPlayerMoveResponse{
		MoveStartTime: 0, // server tick at move start; 0 acceptable for local clock
		SrcX:          src.Pos.X,
		SrcY:          src.Pos.Y,
		DestX:         req.DestX,
		DestY:         req.DestY,
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode player-move", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)

	// Broadcast the walk to OTHER nearby clients so they see the mover travel.
	// ZC_UNIT_WALKING (0x09fd) carries the mover's GID and is distinct from the
	// self-only ZC_NOTIFY_PLAYERMOVE (0x0087) emitted above — the anchor is the
	// destination cell, so any player who can see where the mover ends up is told.
	if wbuf, ok := encodeUnitWalk(s, unitWalkFromEntity(src, req.DestX, req.DestY)); ok {
		s.broadcast(wbuf, src.Map, worlddomain.Position{X: req.DestX, Y: req.DestY}, auth.charID)
	}

	// Warp portal: landing on a corpus warp trigger tile teleports the player
	// (rAthena's invisible tile portals). The move above already seated the
	// player at the trigger cell; the portal relocation re-seats it at the
	// destination, persists the position, and hands the client a
	// ZC_NPCACK_MAPMOVE so it re-enters at the destination.
	if def, ok := s.spawn.PortalAt(src.Map, int(req.DestX), int(req.DestY)); ok {
		s.relocateThroughPortal(c, auth.charID, def)
	}
}

// relocateThroughPortal moves a player through a warp portal: destination
// persists via WarpPlayer, the source-map neighbors see the player vanish, and
// the client receives ZC_NPCACK_MAPMOVE to relocate. The destination-side
// appear/back-fill happens when the client re-enters through handleEnter
// (same contract as the script `warp` builtin).
func (s *MapServer) relocateThroughPortal(c gnet.Conn, charID uint32, def script.WarpDef) {
	e, err := s.world.Get(worlddomain.EntityID(charID)) //nolint:gosec // G115: charID is a char_id (uint32).
	if err != nil {
		return // left between move and portal check
	}
	fromMap, fromPos := e.Map, e.Pos
	if err := s.world.RelocatePlayer(charID, def.DestMap, int16(def.DestX), int16(def.DestY)); err != nil { //nolint:gosec // G115: portal coords are map-tile bounds.
		s.log.Warn("map: portal warp", "charID", charID, "dest", def.DestMap, "err", err)
		return
	}
	// Source-map farewell: neighbors stop seeing the player at the trigger cell.
	vanish := ropacket.NotifyVanishResponse{GID: charID, Type: ropacket.VanishDead}
	vbuf := make([]byte, vanish.Size())
	if err := vanish.Encode(sliceWriter(vbuf)); err != nil {
		s.log.Error("map: encode portal vanish", "err", err)
	} else {
		s.broadcast(vbuf, fromMap, fromPos, charID)
	}
	var buf bytes.Buffer
	_ = ropacket.MapMoveResponse{MapName: def.DestMap, X: uint16(def.DestX), Y: uint16(def.DestY)}.Encode(&buf) //nolint:errcheck,gosec // G115: portal coords fit; encode cannot fail on a bytes.Buffer.
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// objectTypePC is the ZC_SPAWN_UNIT / ZC_UNIT_WALKING object-type byte for a
// player character (rAthena's clif_bl_type: 0=PC).
const objectTypePC uint8 = 0

// objectTypeMob is the ZC_UNIT_WALKING object-type byte for a monster
// (rAthena's clif_bl_type: 5=MOB). Used by the mob-chase broadcast.
const objectTypeMob uint8 = 5

// objectTypeNPC is the ZC_SPAWN_UNIT object-type byte for a static NPC
// (rAthena's clif_bl_type: 6=NPC_EVT). Used by the map-enter AOI back-fill so
// a joining client sees seeded dialog/shop NPCs.
const objectTypeNPC uint8 = 6

// unitWalkFromEntity builds the ZC_UNIT_WALKING (0x09fd) observer broadcast for
// a PC moving from src to dest. The mover's own move-ack (ZC_NOTIFY_PLAYERMOVE
// 0x0087) is a separate packet; this is what OTHER nearby clients receive so
// they see the sprite travel. Look fields come from the entity captured before
// the move, whose position is still the move's source cell.
func unitWalkFromEntity(src worlddomain.Entity, destX, destY int16) ropacket.UnitWalkingResponse {
	return ropacket.UnitWalkingResponse{
		ObjectType: objectTypePC,
		AID:        src.Account,
		GID:        uint32(src.ID), //nolint:gosec // G115: EntityID wraps a uint32 char_id.
		Speed:      src.Speed,
		Job:        src.Job,
		Head:       src.Head,
		Weapon:     src.Weapon,
		Shield:     src.Shield,
		Sex:        src.Sex,
		SrcX:       src.Pos.X,
		SrcY:       src.Pos.Y,
		DestX:      destX,
		DestY:      destY,
		XSize:      5, // rAthena hardcodes 5 for PCs.
		YSize:      5,
		CLevel:     src.Level,
		MaxHP:      src.MaxHP,
		HP:         src.HP,
		Body:       src.Job,
		Name:       src.Name,
	}
}

// spawnUnitFromEntity builds the ZC_SPAWN_UNIT (0x09fe) broadcast for a PC that
// just entered a map, so OTHER nearby clients see the player appear.
func spawnUnitFromEntity(e worlddomain.Entity) ropacket.SpawnUnitResponse {
	resp := spawnUnitAny(e, objectTypePC)
	resp.AID = e.Account
	return resp
}

// spawnUnitNPC builds the ZC_SPAWN_UNIT frame for a static NPC: ObjectType=6
// and the sprite rides the Job field (rAthena renders an NPC's view from the
// job/class slot). AID=GID (NPCs have no account).
func spawnUnitNPC(e worlddomain.Entity) ropacket.SpawnUnitResponse {
	resp := spawnUnitAny(e, objectTypeNPC)
	resp.AID = uint32(e.ID)   //nolint:gosec // G115: EntityID wraps a uint32 GID.
	resp.Job = int16(e.Class) //nolint:gosec // G115: the NPC's sprite id rides Class for EntityTypeNPC.
	resp.XSize, resp.YSize = 5, 5
	return resp
}

// spawnUnitAny fills the entity-independent ZC_SPAWN_UNIT fields.
func spawnUnitAny(e worlddomain.Entity, objectType uint8) ropacket.SpawnUnitResponse {
	return ropacket.SpawnUnitResponse{
		ObjectType: objectType,
		AID:        e.Account,
		GID:        uint32(e.ID), //nolint:gosec // G115: EntityID wraps a uint32 char_id.
		Speed:      e.Speed,
		Job:        e.Job,
		Head:       e.Head,
		Weapon:     e.Weapon,
		Shield:     e.Shield,
		Sex:        e.Sex,
		PosX:       e.Pos.X,
		PosY:       e.Pos.Y,
		Dir:        e.Dir,
		XSize:      5, // rAthena hardcodes 5 for PCs.
		YSize:      5,
		CLevel:     e.Level,
		MaxHP:      e.MaxHP,
		HP:         e.HP,
		Body:       e.Job,
		Name:       e.Name,
	}
}

// encodeUnitWalk encodes a UnitWalkingResponse into a fresh buffer, logging and
// returning ok=false on failure so the caller can skip the broadcast without a
// wire error.
func encodeUnitWalk(s *MapServer, r ropacket.UnitWalkingResponse) ([]byte, bool) {
	buf := make([]byte, r.Size())
	if err := r.Encode(sliceWriter(buf)); err != nil {
		s.log.Error("map: encode unit-walking", "err", err)
		return nil, false
	}
	return buf, true
}

// encodeSpawnUnit encodes a SpawnUnitResponse into a fresh buffer, logging and
// returning ok=false on failure.
func encodeSpawnUnit(s *MapServer, r ropacket.SpawnUnitResponse) ([]byte, bool) {
	buf := make([]byte, r.Size())
	if err := r.Encode(sliceWriter(buf)); err != nil {
		s.log.Error("map: encode spawn-unit", "err", err)
		return nil, false
	}
	return buf, true
}

// handleItemPickup handles CZ_ITEM_PICKUP (0x0362, 6B): parse GroundID, look up
// the floor item, remove it from the ground, add it to the player's inventory,
// and reply ZC_ITEM_PICKUP_ACK.
func (s *MapServer) handleItemPickup(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_ITEM_PICKUP from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZItemPickup(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ITEM_PICKUP", "err", err)
		return
	}
	fi, err := s.spawn.PickupFloorItem(req.GroundID)
	if err != nil {
		s.log.Debug("map: pickup (not found)", "gid", req.GroundID)
		return // item already taken or gone — client re-syncs
	}
	added, err := s.inv.Add(context.Background(), auth.charID, fi.NameID, int(fi.Amount))
	if err != nil {
		s.log.Error("map: pickup add inventory", "err", err)
		return
	}
	// The ack must name the slot the item actually landed in, not the zero
	// value: rAthena writes packet.index = client_index(n) for the row
	// pc_additem chose (clif_additem, clif.cpp:2897), and a hardcoded 0 would
	// tell every pickup it became the first grid slot.
	slot, ok := s.clientIndexForItem(context.Background(), auth.accountID, auth.charID, added.ID)
	if !ok {
		s.log.Error("map: picked-up row has no resolvable bag slot", "nameID", fi.NameID, "row", added.ID)
		return
	}
	s.writeItemPickupAck(c, slot, fi.NameID, uint16(fi.Amount)) //nolint:gosec // G115: item amount bounded to small stack values.
	// Other nearby players must stop seeing the item on the ground
	// (ZC_ITEM_DISAPPEAR at the item's cell; the picker's own copy left with
	// the ack above). Without it neighbors keep a ghost loot sprite and any
	// click on it dead-ends at "not found".
	dis := ropacket.ItemDisappearResponse{AID: fi.GroundID}
	dbuf := make([]byte, dis.Size())
	if err := dis.Encode(sliceWriter(dbuf)); err != nil {
		s.log.Error("map: encode item-disappear", "err", err)
		return
	}
	s.broadcast(dbuf, fi.Map, worlddomain.Position{X: fi.PosX, Y: fi.PosY}, auth.charID)
}

// handleItemDrop handles CZ_ITEM_DROP (0x0363, 6B): resolve the inventory slot
// the client drops from, remove Amount units from the bag, spawn a floor item at
// the player's feet, and reply ZC_ITEM_THROW_ACK (SELF) + ZC_ITEM_FALL_ENTRY
// (the floor-item landing packet the rathenaThailand fork emits on a drop).
//
// The wire InventoryIndex is a CLIENT index (server row + 2, clif.cpp:122-128);
// it converts with ropacket.ServerIndex to the row rAthena's clif_parse_DropItem
// computes as `RFIFOW(...)-2` (clif.cpp:12063). The inventory port keys removal
// by ItemID, not index, so the row resolves against the ordered list LoadByChar
// returns — which is the same order handleLoadEndAck's init burst assigns
// (Phase 42), so the client's slot and this row now agree.
func (s *MapServer) handleItemDrop(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_ITEM_DROP from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZDropItem(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ITEM_DROP", "err", err)
		return
	}
	if req.Amount == 0 {
		s.log.Warn("map: CZ_ITEM_DROP zero amount", "index", req.InventoryIndex)
		return
	}
	items, err := s.inv.LoadByChar(context.Background(), auth.accountID, auth.charID)
	if err != nil {
		s.log.Error("map: drop load inventory", "err", err)
		return
	}
	row := int(ropacket.ServerIndex(req.InventoryIndex))
	if row >= len(items) {
		s.log.Warn("map: CZ_ITEM_DROP index out of range", "index", req.InventoryIndex, "slots", len(items))
		return
	}
	item := items[row]
	if uint32(req.Amount) > item.Amount { //nolint:gosec // G115: uint16→uint32 is lossless.
		s.log.Warn("map: CZ_ITEM_DROP amount over stack", "index", req.InventoryIndex, "want", req.Amount, "have", item.Amount)
		return
	}
	if err := s.inv.Remove(context.Background(), item.ID, int(req.Amount)); err != nil {
		s.log.Warn("map: drop remove inventory", "err", err, "id", item.ID)
		return
	}
	gid := worlddomain.EntityID(auth.charID)
	entity, _ := s.world.Get(gid)
	fi := s.spawn.DropItem(item.NameID, uint32(req.Amount), entity.Map, entity.Pos, gid) //nolint:gosec // G115: uint16→uint32 is lossless.

	// Coalesce the SELF throw-ack (tells the dropping client the bag row left)
	// and the floor-item landing (0x0ADD, the fork's only drop-path packet) into
	// one AsyncWrite to avoid two syscalls.
	var burst []byte
	throwAck := ropacket.ItemThrowAckResponse{Index: req.InventoryIndex, Count: req.Amount}
	abuf := make([]byte, throwAck.Size())
	if err := throwAck.Encode(sliceWriter(abuf)); err != nil {
		s.log.Error("map: encode item-throw-ack", "err", err)
		return
	}
	burst = append(burst, abuf...)
	fallEntry := ropacket.ItemFallEntryResponse{
		ID:         fi.GroundID,
		NameID:     fi.NameID,
		Identified: 1,
		X:          uint16(fi.PosX),   //nolint:gosec // G115: map coords are non-negative int16.
		Y:          uint16(fi.PosY),   //nolint:gosec // G115: map coords are non-negative int16.
		Amount:     uint16(fi.Amount), //nolint:gosec // G115: amount bounded to small stack values.
	}
	fbuf := make([]byte, fallEntry.Size())
	if err := fallEntry.Encode(sliceWriter(fbuf)); err != nil {
		s.log.Error("map: encode item-fall-entry", "err", err)
		return
	}
	burst = append(burst, fbuf...)
	_ = c.AsyncWrite(burst, nil)
	// Other nearby players see the dropped item land (ZC_ITEM_FALL_ENTRY); the
	// throw-ack above is dropper-only, so just the fall-entry is fanned out. The
	// anchor is the dropper's cell, where the item spawns at the player's feet.
	s.broadcast(fbuf, entity.Map, entity.Pos, auth.charID)
}

// handleReqWearEquip handles CZ_REQ_WEAR_EQUIP_V5 (0x0998, 8B): the client
// requests wearing the item at inventory Index into Position (an EQP_* bitmask).
// Index is a CLIENT index (server row + 2, clif.cpp:122-128), converted with
// ropacket.ServerIndex before the row is resolved. On success it persists the
// equip via EquipService (which resolves slot conflicts) and replies
// ZC_REQ_WEAR_EQUIP_ACK_V5 with result=1. On a validation failure (sentinel
// error) it logs and keeps the connection alive — only the success path emits an
// ack, because the exact rAthena failure-encoding for the V5 ack (which field
// carries the success/fail byte varies by client era) is uncertain; emitting a
// wrong failure byte could wedge the client's equip slot.
func (s *MapServer) handleReqWearEquip(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_REQ_WEAR_EQUIP from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZReqWearEquip(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_WEAR_EQUIP", "err", err)
		return
	}
	serverRow := int(ropacket.ServerIndex(req.Index))
	if err := s.equip.Equip(context.Background(), auth.accountID, auth.charID, serverRow, req.Position); err != nil {
		s.log.Warn("map: wear equip", "gid", auth.charID, "index", req.Index, "err", err)
		return
	}
	resp := ropacket.ReqWearEquipAckResponse{
		Index:            req.Index, // client index, echoed verbatim (clif.cpp:4315)
		WearLocation:     req.Position,
		ItemSpriteNumber: s.equipSprite(serverRow, req.Position, auth),
		Result:           1, // 1 = success
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode wear-equip ack", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleUseItem handles CZ_USE_ITEM2 (0x0439, 8B): the client double-clicks a
// usable item (e.g. a potion) at inventory Index. It consumes one unit and
// applies the item's effects via ItemUseService, then replies ZC_USE_ITEM_ACK2.
// For a healing item AddVitals fires the world OnStatChange hook (set in
// NewMapServer), so ZC_PAR_CHANGE is emitted during the Use call itself; this
// handler emits only the ack to avoid a duplicate stat-change frame.
//
// Index convention: the wire Index is a CLIENT index (server row + 2,
// clif.cpp:122-128), converted with ropacket.ServerIndex to reach the row
// rAthena's clif_parse_UseItem computes with `n = RFIFOW(...)-2` (clif.cpp:12121).
// The ack carries that same client index back unchanged — rAthena's
// clif_useitemack writes `index + 2` where index is already the server row
// (clif.cpp:4484), which is the value the client originally sent. On a validation
// failure (sentinel error) it emits the ack with Result=0 and keeps the
// connection alive — the failure encoding is known.
func (s *MapServer) handleUseItem(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_USE_ITEM2 from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZUseItem(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_USE_ITEM2", "err", err)
		return
	}
	serverRow := int(ropacket.ServerIndex(req.Index))
	ack, err := s.itemUse.Use(context.Background(), auth.accountID, auth.charID, serverRow)
	if err != nil {
		s.log.Warn("map: use item", "gid", auth.charID, "index", req.Index, "err", err)
		s.writeUseItemAck(c, auth.accountID, req.Index, 0, 0, 0)
		return
	}
	s.writeUseItemAck(c, auth.accountID, req.Index, ack.ItemID, ack.Remaining, 1)
}

// writeUseItemAck emits ZC_USE_ITEM_ACK2 (0x01c8). clientIndex is the already
// +2-translated client-visible index; result is 1 (success) or 0 (failure). On
// an encode error it logs and keeps the connection alive.
func (s *MapServer) writeUseItemAck(c gnet.Conn, aid uint32, clientIndex, itemID, remaining uint16, result uint8) {
	resp := ropacket.UseItemAck2Response{
		Index:  clientIndex,
		ItemID: itemID,
		AID:    aid,
		Amount: remaining,
		Result: result,
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode use-item ack", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleReqTakeoffEquip handles CZ_REQ_TAKEOFF_EQUIP (0x00ab, 4B): the client
// requests removing the item at inventory Index from its slot. It captures the
// worn slot, clears the equip bitmask via EquipService, and replies
// ZC_REQ_TAKEOFF_EQUIP_ACK with flag=0 (success on the wire — the byte is
// inverted for PACKETVER >= 20110824 so 0 = success). On failure it logs and
// keeps the connection alive.
func (s *MapServer) handleReqTakeoffEquip(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_REQ_TAKEOFF_EQUIP from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZReqTakeoffEquip(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_TAKEOFF_EQUIP", "err", err)
		return
	}
	serverRow := int(ropacket.ServerIndex(req.Index))
	worn, ok := s.wornSlot(auth, serverRow)
	if !ok {
		s.log.Warn("map: takeoff index out of range", "gid", auth.charID, "index", req.Index)
		return
	}
	if err := s.equip.Unequip(context.Background(), auth.accountID, auth.charID, serverRow); err != nil {
		s.log.Warn("map: takeoff equip", "gid", auth.charID, "index", req.Index, "err", err)
		return
	}
	resp := ropacket.ReqTakeoffEquipAckResponse{
		Index:        req.Index, // client index, echoed verbatim (clif.cpp:4346)
		WearLocation: worn,
		Flag:         0, // 0 = success on the wire (inverted) for PACKETVER >= 20110824
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode takeoff-equip ack", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// wornSlot resolves the EQP_* bitmask currently worn by the item at server row
// serverRow (0-based), so the takeoff ack can report the slot it freed. It
// returns ok=false when the row is out of range (the player cannot unequip a
// slot that has no item). The read is best-effort: between this load and
// Unequip's internal clear the slot could change for a racy double-unequip, but
// the only consequence is a stale WearLocation in one ack — cosmetic, not
// state-corrupting.
func (s *MapServer) wornSlot(auth *mapAuth, serverRow int) (uint32, bool) {
	items, err := s.inv.LoadByChar(context.Background(), auth.accountID, auth.charID)
	if err != nil {
		s.log.Error("map: load inventory for takeoff", "err", err)
		return 0, false
	}
	if serverRow < 0 || serverRow >= len(items) {
		return 0, false
	}
	return items[serverRow].Equip, true
}

// equipSprite resolves the view sprite the equip ack should carry for the item
// at server row serverRow. rAthena emits the item's look (item_db View) only when
// the item occupies a VISIBLE equip position, and 0 otherwise (clif.cpp:4316-4322
// gates on `equip & EQP_VISIBLE`). Reproducing that gate matters: writing a
// non-zero view for, say, a weapon would make the client render a head sprite.
// position is the EQP_* bitmask the client asked for; on any lookup miss the
// sprite is 0, which is the same value the old hardcoded 0 produced.
func (s *MapServer) equipSprite(serverRow int, position uint32, auth *mapAuth) uint16 {
	if position&equip.EquipVisible == 0 {
		return 0
	}
	items, err := s.inv.LoadByChar(context.Background(), auth.accountID, auth.charID)
	if err != nil || serverRow < 0 || serverRow >= len(items) {
		return 0
	}
	entry := s.itemEntry(items[serverRow].NameID)
	if entry == nil || entry.View <= 0 || entry.View > 0xffff {
		return 0
	}
	return uint16(entry.View) //nolint:gosec // G115: range-checked above.
}

// handleActionRequest handles CZ_ACTION_REQUEST (0x0089, 7B): sit/stand/attack.
// Sit/stand echo back and set the PC's seated state (Sitting halves natural-regen
// intervals); attack (action 0x07) resolves melee damage via CombatService,
// echoes the action, and — when the hit kills a mob — drives the death loop:
// drops + despawn (SpawnService.OnMobDeath) then a ZC_NOTIFY_VANISH + one
// ZC_ITEM_ENTRY per rolled drop.
func (s *MapServer) handleActionRequest(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_ACTION_REQUEST from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZActionRequest(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ACTION_REQUEST", "err", err)
		return
	}
	if req.Action != 0x07 { // sit/stand/pickup: echo only
		s.sendActionResponse(c, auth.charID, req.Action, req.TargetGID)
		// Track seated state so RegenTick can halve the regen interval for sitters.
		// A sit/stand for an entity not on this map is harmless (sentinel ignored).
		switch req.Action {
		case ropacket.DMGSitDown:
			if err := s.world.SetSitting(auth.charID, true); err != nil {
				s.log.Debug("map: sit for unknown entity", "gid", auth.charID, "err", err)
			}
		case ropacket.DMGStandUp:
			if err := s.world.SetSitting(auth.charID, false); err != nil {
				s.log.Debug("map: stand for unknown entity", "gid", auth.charID, "err", err)
			}
		}
		return
	}
	// attack (0x07)
	dmg, died, err := s.combat.Attack(worlddomain.EntityID(auth.charID), worlddomain.EntityID(req.TargetGID))
	if err != nil {
		s.log.Warn("map: attack", "err", err)
		return
	}
	s.log.Debug("map: attack", "attacker", auth.charID, "target", req.TargetGID, "dmg", dmg)
	s.sendActionResponse(c, auth.charID, req.Action, req.TargetGID)
	if died {
		s.handleMobDeath(c, auth.charID, req.TargetGID)
	}
}

// sendActionResponse encodes and writes ZC_ACTION_RESPONSE (the action echo).
func (s *MapServer) sendActionResponse(c gnet.Conn, charID uint32, action uint8, targetGID uint32) {
	resp := ropacket.ActionResponse{GID: charID, Action: action, TargetGID: targetGID}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode action-response", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleMobDeath despawns a dead mob, rolls its drops, and notifies the killing
// client: ZC_NOTIFY_VANISH (mob leaves the map) then one ZC_ITEM_ENTRY per
// rolled drop. The same vanish+drop burst is broadcast to every OTHER player
// near the death cell so the shared world shows the mob dying and its loot
// landing. The death/drop state lives in SpawnService.OnMobDeath; this only
// does the wire side. Only mobs despawn+drop on death (a PC reaching 0 HP is a
// revive flow). The frames are coalesced into one AsyncWrite per recipient.
func (s *MapServer) handleMobDeath(c gnet.Conn, killerCharID uint32, mobGID uint32) {
	defender, err := s.world.Get(worlddomain.EntityID(mobGID))
	if err != nil {
		return // already removed (concurrent death) — nothing to broadcast
	}
	if defender.Type != worlddomain.EntityTypeMob {
		return
	}
	drops := s.spawn.OnMobDeath(defender.Class, defender.Map, defender.Pos, worlddomain.EntityID(mobGID))

	var burst []byte
	vanish := ropacket.NotifyVanishResponse{GID: mobGID, Type: ropacket.VanishDead}
	vbuf := make([]byte, vanish.Size())
	if err := vanish.Encode(sliceWriter(vbuf)); err != nil {
		s.log.Error("map: encode vanish", "err", err)
		return
	}
	burst = append(burst, vbuf...)
	for _, fi := range drops {
		entry := ropacket.ItemEntryResponse{
			AID:        fi.GroundID,
			NameID:     fi.NameID,
			Identified: 1,
			X:          uint16(fi.PosX),   //nolint:gosec // G115: map coords are non-negative int16.
			Y:          uint16(fi.PosY),   //nolint:gosec // G115: map coords are non-negative int16.
			Amount:     uint16(fi.Amount), //nolint:gosec // G115: amount bounded to small stack values.
		}
		ebuf := make([]byte, entry.Size())
		if err := entry.Encode(sliceWriter(ebuf)); err != nil {
			s.log.Error("map: encode item-entry", "err", err)
			continue
		}
		burst = append(burst, ebuf...)
	}
	_ = c.AsyncWrite(burst, nil)
	// Broadcast the mob vanish + loot to OTHER nearby players (not the killer,
	// who already received burst above). burst is immutable after this point, so
	// fanning the same buffer to multiple connections is safe.
	s.broadcast(burst, defender.Map, defender.Pos, killerCharID)
	// EXP reward: grant the mob's mob_db BaseExp/JobExp. Best-effort — a mob with
	// no mob_db entry (MobExp returns 0,0) earns nothing, and a killer that left
	// between the hit and the grant surfaces ErrEntityNotFound, logged not fatal
	// (the vanish/drop broadcast already completed). GrantExp fires
	// OnExpChange, which emits the killer's two ZC_LONGLONGPAR_CHANGE frames.
	base, job := s.spawn.MobExp(defender.Class)
	if base == 0 && job == 0 {
		return
	}
	s.grantMobExp(killerCharID, defender.Map, base, job)
}

// grantMobExp pays a mob-kill reward to the killer and, when the killer is in a
// party whose members share EXP, to the rest of that party.
//
// Eligibility is computed by the party module from the roster (online AND on the
// killer's map), mirroring rAthena's party_exp_share member filter. Death is
// runtime state the party module does not own, so dead members are filtered here
// from the world registry before the split runs.
//
// A solo killer, a killer with no party, or any party error falls back to paying
// the full reward to the killer — the pre-M11 behavior, and the same reward the
// killer would have earned alone, so a party failure can never cost EXP.
func (s *MapServer) grantMobExp(killerCharID uint32, killerMap string, base, job uint64) {
	ctx := context.Background()
	if s.party != nil {
		if awards, err := s.party.SplitExp(ctx, s.killerPartyID(ctx, killerCharID), killerMap, base, job); err == nil && awards != nil {
			awards = s.dropDeadSharers(awards)
			for charID, gain := range awards {
				if _, _, err := s.world.GrantExp(ctx, charID, gain[0], gain[1]); err != nil {
					s.log.Warn("map: grant party exp", "char", charID, "err", err)
				}
			}
			return
		}
	}
	if _, _, err := s.world.GrantExp(ctx, killerCharID, base, job); err != nil {
		s.log.Warn("map: grant exp on mob death", "killer", killerCharID, "err", err)
	}
}

// killerPartyID resolves the killer's party, or 0 when they have none (SplitExp
// then reports ErrPartyNotFound and the caller falls back to the solo reward).
func (s *MapServer) killerPartyID(ctx context.Context, charID uint32) partydomain.PartyID {
	p, err := s.party.GetByMember(ctx, charID)
	if err != nil {
		return 0
	}
	return p.ID
}

// dropDeadSharers removes any award whose recipient is currently dead. rAthena
// excludes dead members from the share (src/map/party.cpp:1257 pc_isdead); a dead
// member's award is simply not paid. Death is HP <= 0 (the world tracks no
// separate flag), and a recipient missing from the registry counts as dead.
func (s *MapServer) dropDeadSharers(awards map[uint32][2]uint64) map[uint32][2]uint64 {
	for charID := range awards {
		e, err := s.world.Get(worlddomain.EntityID(charID))
		if err != nil || e.HP <= 0 {
			delete(awards, charID)
		}
	}
	return awards
}

// handleUseSkill2 handles CZ_USE_SKILL2 (0x0438, 10B): cast a single-target
// skill onto an entity. It validates the cast through SkillService (skill known,
// level in range, target reachable, SP affordable), then on success emits
// ZC_NOTIFY_SKILL (0x01de) carrying the resolved damage. The damage is a
// melee-equivalent hit routed through the existing CombatService path — full
// skill-damage modeling (element/size/crit/per-skill multipliers) is kernel
// future work and deliberately not faked here. On failure it emits
// ZC_ACK_TOUSESKILL only for the verified SP-insufficient cause; other
// validation failures are logged and the connection is kept alive (their
// USESKILL_FAIL_* wire codes are not yet defined in the packet layer, a known
// gap — no wire value is invented). When the hit kills a mob, the same
// drop/despawn loop as CZ_ACTION_REQUEST runs.
func (s *MapServer) handleUseSkill2(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_USE_SKILL2 from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZUseSkill(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_USE_SKILL2", "err", err)
		return
	}
	dmg, died, err := s.skills.UseSkillOnTarget(
		worlddomain.EntityID(auth.charID),
		int32(req.SkillID),
		req.SkillLv,
		worlddomain.EntityID(req.TargetID),
	)
	if err != nil {
		s.log.Warn("map: skill cast", "skill", req.SkillID, "level", req.SkillLv, "err", err)
		s.sendSkillFail(c, req.SkillID, err)
		return
	}
	resp := ropacket.NotifySkillResponse{
		SKID:     req.SkillID,
		AID:      auth.charID,
		TargetID: req.TargetID,
		Damage:   dmg,
		Level:    req.SkillLv,
		Count:    1,
		Action:   0, // DMG_NORMAL (clif.cpp damage_type selector)
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode notify-skill", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
	s.log.Debug("map: skill cast", "skill", req.SkillID, "level", req.SkillLv, "target", req.TargetID, "dmg", dmg)
	if died {
		s.handleMobDeath(c, auth.charID, req.TargetID)
	}
}

// handleUseSkillToPos handles CZ_USE_SKILL_TOPOS (0x0AF4, 11B): a client casts a
// ground-target skill onto a tile. It always emits ZC_NOTIFY_GROUNDSKILL (0x0117)
// to place the skill's visual effect on the cast tile, so the client renders the
// cast even when no mob is in range. As an honest single-target approximation of
// the ground cast it then resolves the nearest mob to the cast tile and routes
// one skill hit through SkillService — the same combat path as CZ_USE_SKILL2 —
// so the cast deals damage when a mob is adjacent. Full ground-AoE damage (every
// mob within the skill's tile radius, element/size-modified skill-damage) is
// kernel future work and deliberately not faked here: only the single nearest
// mob is affected. Validation failures are logged, surface ZC_ACK_TOUSESKILL only
// for the verified SP-insufficient cause, and never close the connection.
func (s *MapServer) handleUseSkillToPos(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_USE_SKILL_TOPOS from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZUseSkillToPos(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_USE_SKILL_TOPOS", "err", err)
		return
	}
	// Place the cast visual on the tile. AID is the caster's GID, matching the
	// rAthena layout documented on GroundSkillPoseEffect.
	pose := ropacket.GroundSkillPoseEffect{
		SKID:      req.SkillID,
		AID:       auth.charID,
		Level:     req.SkillLv,
		XPos:      int16(req.X), //nolint:gosec // G115: map coords are non-negative int16.
		YPos:      int16(req.Y), //nolint:gosec // G115: map coords are non-negative int16.
		StartTime: 0,            // server tick at cast resolution; 0 acceptable for the local clock.
	}
	out := make([]byte, pose.Size())
	if err := pose.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode notify-groundskill", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
	s.log.Debug("map: ground-skill cast", "skill", req.SkillID, "level", req.SkillLv, "x", req.X, "y", req.Y)

	// Resolve the nearest mob to the cast tile and route one hit through the
	// existing SkillService path. A true AoE (every mob in the skill radius) is
	// kernel future work; this single-target approximation keeps the cast honest.
	caster, err := s.world.Get(worlddomain.EntityID(auth.charID))
	if err != nil {
		s.log.Warn("map: ground-skill caster lookup", "err", err)
		return
	}
	mobID := nearestMobID(s.world, caster.Map, int(req.X), int(req.Y))
	if mobID == 0 {
		return
	}
	dmg, died, err := s.skills.UseSkillOnTarget(
		worlddomain.EntityID(auth.charID),
		int32(req.SkillID),
		req.SkillLv,
		mobID,
	)
	if err != nil {
		s.log.Warn("map: ground-skill cast", "skill", req.SkillID, "level", req.SkillLv, "err", err)
		s.sendSkillFail(c, req.SkillID, err)
		return
	}
	// Surface the resolved hit to the client (ZC_NOTIFY_SKILL), mirroring the
	// CZ_USE_SKILL2 path, so the caster sees the damage applied to the nearest mob
	// — not just the ground visual. Full AoE (per-mob notifications) is future work.
	hit := ropacket.NotifySkillResponse{
		SKID:     req.SkillID,
		AID:      auth.charID,
		TargetID: uint32(mobID), //nolint:gosec // G115: EntityID is uint32 by definition.
		Damage:   dmg,
		Level:    req.SkillLv,
		Count:    1,
		Action:   0, // DMG_NORMAL
	}
	out2 := make([]byte, hit.Size())
	if err := hit.Encode(sliceWriter(out2)); err != nil {
		s.log.Error("map: encode ground-skill notify-skill", "err", err)
		return
	}
	_ = c.AsyncWrite(out2, nil)
	s.log.Debug("map: ground-skill hit", "skill", req.SkillID, "target", mobID, "dmg", dmg)
	if died {
		s.handleMobDeath(c, auth.charID, uint32(mobID)) //nolint:gosec // G115: EntityID is uint32 by definition.
	}
}

// nearestMobID returns the EntityID of the mob nearest (Chebyshev cell distance)
// to tile (x, y) on mapName, or 0 when no mob is visible from that tile. It is
// the honest single-target approximation of a ground-AoE skill: full per-radius
// multi-target resolution is kernel future work.
func nearestMobID(world *worldapp.WorldService, mapName string, x, y int) worlddomain.EntityID {
	var nearest worlddomain.EntityID
	bestDist := -1
	for _, id := range world.QueryVisible(mapName, x, y) {
		e, err := world.Get(id)
		if err != nil || e.Type != worlddomain.EntityTypeMob {
			continue
		}
		dx := abs(x - int(e.Pos.X))
		dy := abs(y - int(e.Pos.Y))
		d := dx
		if dy > dx {
			d = dy
		}
		if bestDist < 0 || d < bestDist {
			bestDist = d
			nearest = id
		}
	}
	return nearest
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// sendSkillFail emits ZC_ACK_TOUSESKILL (0x0110) to reject a cast. Only the
// SP-insufficient cause (12) is verified in the packet layer; other failure
// reasons have no defined wire code yet, so they are dropped (logged at the
// call site) rather than emitting an unverified cause.
func (s *MapServer) sendSkillFail(c gnet.Conn, skillID uint16, castErr error) {
	if !errors.Is(castErr, worldapp.ErrInsufficientSP) {
		return
	}
	resp := ropacket.AckUseSkillResponse{SkillID: skillID, Cause: ropacket.UseSkillFailSPInsufficient}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ack-touseskill", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// --- player-to-player trade handlers ---
//
// Trade runs a request→ack→(stage)→ok state machine across TWO connections: the
// sender's (c) and the partner's (resolved through the conn-registry shim by
// charID). Every handler resolves the partner BEFORE calling a service method
// that may tear the session down (Ack cancel / OK conclude / Cancel), since
// Partner() needs the live session to map charID→partner.

// handleTradeRequest opens a trade request (CZ_TRADE_REQUEST 0x00e4). The
// requester targets a partner GID; on success the server opens the TARGET's trade
// dialog (ZC_REQ_EXCHANGE_ITEM carries the requester's name/AID/level — the real
// wire format sends this to the TARGET only, never the requester). On failure the
// requester gets a ZC_ACK_EXCHANGE_ITEM reject reason.
func (s *MapServer) handleTradeRequest(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_TRADE_REQUEST from unauthed conn")
		return
	}
	if s.trade == nil {
		s.log.Debug("map: trade not wired, ignoring CZ_TRADE_REQUEST")
		return
	}
	req, err := ropacket.ParseCZTradeRequest(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_TRADE_REQUEST", "err", err)
		return
	}
	targetGID := req.TargetGID
	if err := s.trade.Request(context.Background(), auth.charID, targetGID); err != nil {
		s.writeTradeAck(c, tradeAckResult(err), 0, 0)
		s.log.Debug("map: trade request rejected", "req", auth.charID, "tgt", targetGID, "err", err)
		return
	}
	// Request succeeded: resolve the requester's name/AID/level to populate the
	// target's dialog, then deliver it to the target's connection.
	reqEnt, gerr := s.world.Get(worlddomain.EntityID(auth.charID))
	if gerr != nil {
		s.trade.Cancel(context.Background(), auth.charID)
		s.log.Error("map: resolve requester entity for trade", "gid", auth.charID, "err", gerr)
		return
	}
	targetConn, ok := s.connFor(targetGID)
	if !ok {
		// Target is an online PC (Request verified it) but not reachable through
		// the conn-registry shim — tear the session down and reject the requester.
		s.trade.Cancel(context.Background(), auth.charID)
		s.writeTradeAck(c, ropacket.TradeAckCharNotExist, 0, 0)
		return
	}
	s.writeTradeRequest(targetConn, reqEnt.Name, auth.accountID, uint16(reqEnt.Level)) //nolint:gosec // G115: base level fits uint16
}

// handleTradeAck applies the target's accept/cancel of a pending request
// (CZ_TRADE_ACK 0x00e6). Type 3 = accept, anything else = cancel. On accept both
// sides get ZC_ACK_EXCHANGE_ITEM(Accept) carrying the OTHER party's AID/level; on
// cancel both get ZC_CANCEL_EXCHANGE_ITEM. The ack-sender is the target (the one
// whose dialog was opened); its partner is the requester.
func (s *MapServer) handleTradeAck(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_TRADE_ACK from unauthed conn")
		return
	}
	if s.trade == nil {
		s.log.Debug("map: trade not wired, ignoring CZ_TRADE_ACK")
		return
	}
	req, err := ropacket.ParseCZTradeAck(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_TRADE_ACK", "err", err)
		return
	}
	accept := req.Type == ropacket.CZTradeAckAccept
	// Resolve the partner BEFORE Ack: an accept leaves both sessions active, but a
	// cancel tears both down.
	partnerID, ok := s.trade.Partner(context.Background(), auth.charID)
	if !ok {
		s.log.Debug("map: CZ_TRADE_ACK with no active trade", "gid", auth.charID)
		return
	}
	if err := s.trade.Ack(context.Background(), auth.charID, accept); err != nil {
		s.log.Debug("map: trade ack failed", "gid", auth.charID, "err", err)
		return
	}
	if !accept {
		s.writeTradeCancel(c)
		if pc, ok := s.connFor(partnerID); ok {
			s.writeTradeCancel(pc)
		}
		return
	}
	// Accept: cross-echo each side the OTHER party's AID/level.
	selfEnt, _ := s.world.Get(worlddomain.EntityID(auth.charID))
	partnerEnt, _ := s.world.Get(worlddomain.EntityID(partnerID))
	s.writeTradeAck(c, ropacket.TradeAckAccept, partnerEnt.Account, uint16(partnerEnt.Level)) //nolint:gosec // G115: base level fits uint16
	if pc, ok := s.connFor(partnerID); ok {
		s.writeTradeAck(pc, ropacket.TradeAckAccept, selfEnt.Account, uint16(selfEnt.Level)) //nolint:gosec // G115: base level fits uint16
	}
}

// handleAddExchangeItem stages an item (index>0) or zeny (index==0) on the
// sender's side (CZ_ADD_EXCHANGE_ITEM 0x00e8). On success the SENDER gets
// ZC_ACK_ADD_EXCHANGE_ITEM(Success) and the PARTNER gets ZC_ADD_EXCHANGE_ITEM (the
// staged view); on failure only the sender is told.
func (s *MapServer) handleAddExchangeItem(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_ADD_EXCHANGE_ITEM from unauthed conn")
		return
	}
	if s.trade == nil {
		s.log.Debug("map: trade not wired, ignoring CZ_ADD_EXCHANGE_ITEM")
		return
	}
	req, err := ropacket.ParseCZAddExchangeItem(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ADD_EXCHANGE_ITEM", "err", err)
		return
	}
	partnerID, ok := s.trade.Partner(context.Background(), auth.charID)
	if !ok {
		s.writeAckAddItem(c, req.Index, ropacket.TradeItemAddCanceled)
		s.log.Debug("map: CZ_ADD_EXCHANGE_ITEM with no active trade", "gid", auth.charID)
		return
	}
	// The zeny sentinel is a WIRE value, not a server row: rAthena tests the raw
	// index for 0 before converting (clif.cpp:12565 `if( p->index == 0 )`), so it
	// must be dispatched here, before ServerIndex turns 0 into a wrapped row. For
	// an item index the wire value is a client index (server row + 2,
	// clif.cpp:122-128).
	ctx := context.Background()
	var res worldapp.AddItemResult
	if req.Index == 0 {
		var err error
		res, err = s.trade.AddZeny(ctx, auth.charID, int(req.Amount)) //nolint:gosec // G115: wire amount is a small positive
		if err != nil {
			s.writeAckAddItem(c, req.Index, tradeItemAddResult(err))
			s.log.Debug("map: trade add-zeny rejected", "gid", auth.charID, "err", err)
			return
		}
	} else {
		var err error
		res, err = s.trade.AddItem(ctx, auth.charID, int(ropacket.ServerIndex(req.Index)), int(req.Index), int(req.Amount)) //nolint:gosec // G115: wire index/amount are small positives
		if err != nil {
			s.writeAckAddItem(c, req.Index, tradeItemAddResult(err))
			s.log.Debug("map: trade add-item rejected", "gid", auth.charID, "err", err)
			return
		}
	}
	s.writeAckAddItem(c, req.Index, ropacket.TradeItemAddSuccess)
	if pc, ok := s.connFor(partnerID); ok {
		s.writeZCAddItem(pc, res)
	}
}

// handleTradeOK locks the sender's side (CZ_TRADE_OK 0x00eb). While the partner has
// not yet locked, both sides get ZC_CONCLUDE_EXCHANGE_ITEM (Who=0 to the locker,
// Who=1 to the partner). When both lock, the service runs the atomic conclude
// swap; the lock notifications are still emitted. A conclude failure (the known
// verify-then-swap TOCTOU window) rolls back, cancels both sessions, and tells both
// sides the trade was cancelled.
func (s *MapServer) handleTradeOK(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_TRADE_OK from unauthed conn")
		return
	}
	if s.trade == nil {
		s.log.Debug("map: trade not wired, ignoring CZ_TRADE_OK")
		return
	}
	partnerID, ok := s.trade.Partner(context.Background(), auth.charID)
	if !ok {
		s.log.Debug("map: CZ_TRADE_OK with no active trade", "gid", auth.charID)
		return
	}
	concluded, err := s.trade.OK(context.Background(), auth.charID)
	if err != nil {
		s.writeTradeCancel(c)
		if pc, ok := s.connFor(partnerID); ok {
			s.writeTradeCancel(pc)
		}
		s.log.Warn("map: trade conclude failed", "gid", auth.charID, "err", err)
		return
	}
	s.writeConclude(c, 0) // you pressed Ok
	if pc, ok := s.connFor(partnerID); ok {
		s.writeConclude(pc, 1) // your partner pressed Ok
	}
	if concluded {
		s.log.Info("map: trade concluded", "gid", auth.charID, "partner", partnerID)
	}
}

// handleTradeCancel tears down the sender's trade (CZ_TRADE_CANCEL 0x00ed) and
// tells both sides via ZC_CANCEL_EXCHANGE_ITEM. A no-op when the sender is not
// trading.
func (s *MapServer) handleTradeCancel(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_TRADE_CANCEL from unauthed conn")
		return
	}
	if s.trade == nil {
		return
	}
	partnerID, ok := s.trade.Partner(context.Background(), auth.charID)
	s.trade.Cancel(context.Background(), auth.charID)
	s.writeTradeCancel(c)
	if ok {
		if pc, ok := s.connFor(partnerID); ok {
			s.writeTradeCancel(pc)
		}
	}
}

// --- trade write helpers ---

func (s *MapServer) writeTradeRequest(c gnet.Conn, requesterName string, requesterAID uint32, requesterLv uint16) {
	resp := ropacket.TradeRequestResponse{RequesterName: requesterName, TargetID: requesterAID, TargetLv: requesterLv}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_REQ_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

func (s *MapServer) writeTradeAck(c gnet.Conn, result uint8, targetAID uint32, targetLv uint16) {
	resp := ropacket.TradeAckResponse{Result: result, TargetID: targetAID, TargetLv: targetLv}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_ACK_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

func (s *MapServer) writeTradeCancel(c gnet.Conn) {
	resp := ropacket.CancelExchangeResponse{}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_CANCEL_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

func (s *MapServer) writeAckAddItem(c gnet.Conn, index uint16, result uint8) {
	resp := ropacket.AckAddExchangeItem{Index: index, Result: result}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_ACK_ADD_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeZCAddItem emits the partner's staged view (ZC_ADD_EXCHANGE_ITEM). Item
// rendering fields that need item_db (ItemType, Location, Look, item options) are
// left zero — a best-effort view; the trade state machine and atomic swap do not
// depend on it.
func (s *MapServer) writeZCAddItem(c gnet.Conn, res worldapp.AddItemResult) {
	resp := ropacket.ZCAddExchangeItem{}
	if res.IsZeny {
		resp.Amount = res.Zeny
	} else {
		it := res.Item
		resp.ItemID = it.NameID
		resp.Amount = int32(it.Amount) //nolint:gosec // G115: stack count fits int32
		resp.Damaged = it.Attribute
		resp.Refine = it.Refine
		resp.Cards = [4]uint32{it.Card0, it.Card1, it.Card2, it.Card3}
		if it.Identify > 0 {
			resp.Identified = 1
		}
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_ADD_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

func (s *MapServer) writeConclude(c gnet.Conn, who uint8) {
	resp := ropacket.ConcludeExchangeItem{Who: who}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_CONCLUDE_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// tradeAckResult maps a TradeService sentinel to the ZC_ACK_EXCHANGE_ITEM result
// byte for a request/ack failure (sentinel errors only; no error-string branch).
// The success path emits Accept/Cancel directly, so this only chooses a reject
// reason for the (logged) failure cases.
func tradeAckResult(err error) uint8 {
	switch {
	case errors.Is(err, worldapp.ErrTradeTargetOffline):
		return ropacket.TradeAckCharNotExist
	case errors.Is(err, worldapp.ErrTradeDifferentMap):
		return ropacket.TradeAckTooFar
	case errors.Is(err, worldapp.ErrTradeAlreadyTrading):
		return ropacket.TradeAckBusy
	default:
		return ropacket.TradeAckFailed
	}
}

// tradeItemAddResult maps a TradeService sentinel to the ZC_ACK_ADD_EXCHANGE_ITEM
// result byte (sentinel errors only). Best-effort: the codes do not map 1:1 to the
// failure causes, and only the success path is asserted by tests.
func tradeItemAddResult(err error) uint8 {
	switch {
	case errors.Is(err, worldapp.ErrTradeNotActive), errors.Is(err, worldapp.ErrTradeLocked):
		return ropacket.TradeItemAddCanceled
	case errors.Is(err, worldapp.ErrTradeItemInsufficient):
		return ropacket.TradeItemAddStackExceed
	case errors.Is(err, worldapp.ErrTradeItemOutOfRange), errors.Is(err, worldapp.ErrTradeItemEquipped):
		return ropacket.TradeItemAddInvFull
	default:
		return ropacket.TradeItemAddStackExceed
	}
}

// --- storage handlers (M9 warehouse) ---

// handleReqOpenStore2 opens the warehouse UI (CZ_REQ_OPENSTORE2 0x07e4). The
// server sends the warehouse's current contents as ZC_STORE_NORMALITEMLIST +
// ZC_STORE_EQUIPMENTITEMLIST so the client can populate the warehouse grid.
//
// Without the storage service wired, the handler is a no-op (matches the trade
// nil-tolerant pattern). With it wired, the handler reads the account's
// warehouse rows and emits the two list frames.
func (s *MapServer) handleReqOpenStore2(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_REQ_OPENSTORE2 from unauthed conn")
		return
	}
	if s.storage == nil {
		s.log.Debug("map: storage not wired, ignoring CZ_REQ_OPENSTORE2")
		return
	}
	rows, err := s.storage.LoadWarehouse(context.Background(), auth.accountID)
	if err != nil {
		s.log.Error("map: load warehouse for init burst", "aid", auth.accountID, "err", err)
		return
	}
	s.writeStorageLists(c, rows)
}

// handleCloseStore closes the warehouse (CZ_CLOSE_STORE 0x07e5). Today the
// warehouse state is server-side (no per-conn flag) — close is informational
// and the handler is a no-op. Mirrors rAthena's clif_parse_CloseStore which
// only clears the per-conn storage flag (clif.cpp:7990-7997).
func (s *MapServer) handleCloseStore(_ gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_CLOSE_STORE from unauthed conn")
		return
	}
	// No state to clear today; the warehouse is a per-account store.
}

// handleMoveItemToStore2 moves an item from the player's bag to the warehouse
// (CZ_MOVE_ITEM_TO_STORE2 0x07e6). The wire index is the bag slot (server row
// + 2). On success, emits ZC_STOREITEMLISTRESULT with result=0 (success);
// on failure, result=1.
func (s *MapServer) handleMoveItemToStore2(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_MOVE_ITEM_TO_STORE2 from unauthed conn")
		return
	}
	if s.storage == nil {
		s.log.Debug("map: storage not wired, ignoring CZ_MOVE_ITEM_TO_STORE2")
		return
	}
	req, err := ropacket.ParseCZMoveItemToStore2(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_MOVE_ITEM_TO_STORE2", "err", err)
		return
	}
	_, mErr := s.storage.MoveToStorage(context.Background(), auth.accountID, auth.charID, uint32(req.Index), int(req.Amount)) //nolint:gosec // G115: amount fits int.
	s.writeStorageItemListResult(c, mErr)
}

// handleMoveItemToBody2 moves an item from the warehouse to the player's bag
// (CZ_MOVE_ITEM_TO_BODY2 0x07e7). The wire index is the warehouse slot
// (server row + 2). On success, emits ZC_STOREITEMLISTRESULT with result=0
// (success); on failure, result=1.
func (s *MapServer) handleMoveItemToBody2(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_MOVE_ITEM_TO_BODY2 from unauthed conn")
		return
	}
	if s.storage == nil {
		s.log.Debug("map: storage not wired, ignoring CZ_MOVE_ITEM_TO_BODY2")
		return
	}
	req, err := ropacket.ParseCZMoveItemToBody2(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_MOVE_ITEM_TO_BODY2", "err", err)
		return
	}
	_, mErr := s.storage.MoveFromStorage(context.Background(), auth.accountID, auth.charID, uint32(req.Index), int(req.Amount)) //nolint:gosec // G115: amount fits int.
	s.writeStorageItemListResult(c, mErr)
}

// writeStorageLists emits ZC_STORE_NORMALITEMLIST + ZC_STORE_EQUIPMENTITEMLIST
// to populate the warehouse grid on CZ_REQ_OPENSTORE2. The shape mirrors
// the inventory init burst but without an invType byte (rAthena's
// clif_storageList emits the bare NORMALITEM_INFO / EQUIPITEM_INFO lists).
func (s *MapServer) writeStorageLists(c gnet.Conn, rows []storagedomain.StorageItem) {
	normal := make([]ropacket.InventoryNormalItem, 0, len(rows))
	equipped := make([]ropacket.InventoryEquipItem, 0, len(rows))
	for i, it := range rows {
		//nolint:gosec // G115: warehouse row count bounded by MAX_STORAGE (600).
		clientIdx := uint16(i) + 2
		entry := s.itemEntry(it.NameID)
		wireType := uint8(0) // default IT_ETC when item_db is absent
		if entry != nil {
			wireType = uint8(itemdb.WireType(entry.Type) & 0xff) //nolint:gosec // G115: IT_* values are small.
		}
		common := ropacket.InventoryNormalItem{
			Index: clientIdx,
			ITID:  uint16(it.NameID), //nolint:gosec // G115: item_db ids are registered well below 2^16 in this corpus.
			Type:  wireType,
			Count: uint16(it.Amount), //nolint:gosec // G115: storage stacks are bounded far below 2^16.
			Card: [4]uint16{ //nolint:gosec // G115: card ids are item_db ids, same bound as ITID.
				uint16(it.Card0), uint16(it.Card1), uint16(it.Card2), uint16(it.Card3), //nolint:gosec // G115: ditto.
			},
		}
		if it.Identify != 0 {
			common.Flag = 1 // bit 0 = IsIdentified
		}
		if !it.IsEquipped() {
			normal = append(normal, common)
			continue
		}
		eq := ropacket.InventoryEquipItem{
			Index:         clientIdx,
			ITID:          common.ITID,
			Type:          wireType,
			Location:      it.Equip,
			RefiningLevel: it.Refine,
			Card:          common.Card,
			Flag:          common.Flag,
		}
		if entry != nil && entry.View > 0 && entry.View <= 0xffff && it.Equip&equip.EquipVisible != 0 {
			eq.ItemSpriteNumber = uint16(entry.View) //nolint:gosec // G115: range-checked above.
		}
		equipped = append(equipped, eq)
	}
	var buf bytes.Buffer
	if err := (ropacket.StorageListNormalResponse{Items: normal}).Encode(&buf); err != nil {
		s.log.Error("map: encode ZC_STORE_NORMALITEMLIST", "err", err)
		return
	}
	if err := (ropacket.StorageListEquipResponse{Items: equipped}).Encode(&buf); err != nil {
		s.log.Error("map: encode ZC_STORE_EQUIPMENTITEMLIST", "err", err)
		return
	}
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// writeStorageItemListResult emits ZC_STOREITEMLISTRESULT (0x07eb, 6 bytes).
// result=0 on success (nil err); result=1 on any failure.
func (s *MapServer) writeStorageItemListResult(c gnet.Conn, mErr error) {
	resp := ropacket.StorageItemListResult{Result: 0}
	if mErr != nil {
		resp.Result = 1
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_STOREITEMLISTRESULT", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// authFromConn extracts the cached mapAuth from a gnet connection, or nil if the
// connection has not passed the CZ_ENTER trust gate.
func authFromConn(c gnet.Conn) *mapAuth {
	v := c.Context()
	auth, ok := v.(mapAuth)
	if !ok {
		return nil
	}
	return &auth
}

// gnetWriter adapts a gnet.Conn to content/domain.PacketWriter. It is the
// gateway's own bridge from the gnet wire to the content domain's writer port,
// so the gateway app layer depends only on content/domain, not content/infra.
type gnetWriter struct{ c gnet.Conn }

// WritePacket sends raw bytes to the connection (concurrency-safe via AsyncWrite).
func (w gnetWriter) WritePacket(data []byte) {
	_ = w.c.AsyncWrite(data, nil)
}
