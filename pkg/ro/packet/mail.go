package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Mail (RODEX) family for PACKETVER 20250604 (MAIN ≥ 20150513 selects the
// RODEX generation; the pre-2008 0x023f-0x0274 generation never wires at this
// packetver). Sources cited per declaration; rAthena paths relative to
// third_party/rathena/.
//
// Struct ground truth: src/map/packets_struct.hpp (PACKET_*MAIL*/maillistinfo),
// builders src/map/clif.cpp:15869-16900, opcode table
// src/map/clif_packetdb.hpp:1738-1762 (+ 0x0AC0/0x0AC1 at :1867, 0x0B97/CZ
// CHECKNAME2 at :2002, 0x0A6E at :1855 block gate 20160330).
const (
	// C→S opcodes.
	HeaderCZOPENMAILBOX     uint16 = 0x09e8 // CZ_OPEN_MAILBOX — clif_parse_Mail_refreshinbox
	HeaderCZCLOSEMAILBOX    uint16 = 0x09e9 // CZ_CLOSE_MAILBOX — clif_parse_dull (no-op)
	HeaderCZREQREADMAIL     uint16 = 0x09ea // CZ_REQ_READ_MAIL — clif_parse_Mail_read
	HeaderCZREQNEXTMAILLIST uint16 = 0x09ee // CZ_REQ_NEXT_MAIL_LIST — refreshinbox
	HeaderCZREQREFRESHMAILL uint16 = 0x09ef // CZ_REQ_REFRESH_MAIL_LIST — refreshinbox
	HeaderCZREQZENYFROMMAIL uint16 = 0x09f1 // CZ_REQ_ZENY_FROM_MAIL — clif_parse_Mail_getattach
	HeaderCZREQITEMFROMMAIL uint16 = 0x09f3 // CZ_REQ_ITEM_FROM_MAIL — clif_parse_Mail_getattach
	HeaderCZREQDELETEMAIL   uint16 = 0x09f5 // CZ_REQ_DELETE_MAIL — clif_parse_Mail_delete
	HeaderCZREQCANCELWRITE  uint16 = 0x0a03 // CZ_REQ_CANCEL_WRITE_MAIL
	HeaderCZREQADDITEMMAIL  uint16 = 0x0a04 // CZ_REQ_ADD_ITEM_TO_MAIL — clif_parse_Mail_setattach
	HeaderCZREQREMOVEITEMMA uint16 = 0x0a06 // CZ_REQ_REMOVE_ITEM_MAIL — clif_parse_Mail_winopen
	HeaderCZREQOPENWRITEMAI uint16 = 0x0a08 // CZ_REQ_OPEN_WRITE_MAIL — clif_parse_Mail_beginwrite
	HeaderCZCHECKRECEIVENAM uint16 = 0x0a13 // CZ_CHECK_RECEIVE_CHARACTER_NAME — clif_parse_Mail_Receiver_Check
	HeaderCZREQWRITEMAIL    uint16 = 0x09ec // CZ_REQ_WRITE_MAIL — clif_parse_Mail_send
	HeaderCZREQWRITEMAIL2   uint16 = 0x0a6e // CZ_REQ_WRITE_MAIL2 (≥20160330, carries receiver char id)
	HeaderCZOPENMAILBOX2    uint16 = 0x0ac0 // CZ_OPEN_MAILBOX2 (≥20170419)
	HeaderCZREFRESHMAILLIST uint16 = 0x0ac1 // CZ_REQ_REFRESH_MAIL_LIST2 (≥20170419)
	HeaderCZCHECKNAME2      uint16 = 0x0b97 // CZ_CHECKNAME2 (≥20201104: + own_char byte)

	// S→C opcodes.
	HeaderZCNOTIFYUNREADMA uint16 = 0x09e7 // ZC_NOTIFY_UNREADMAIL — clif_Mail_new
	HeaderZCACKMAILWRITE   uint16 = 0x09ed // ZC_ACK_WRITE_MAIL — clif_Mail_send
	HeaderZCACKZENYFROMMAI uint16 = 0x09f2 // ZC_ACK_ZENY_FROM_MAIL — clif_mail_getattachment
	HeaderZCACKITEMFROMMAI uint16 = 0x09f4 // ZC_ACK_ITEM_FROM_MAIL — clif_mail_getattachment
	HeaderZCACKDELETEMAIL  uint16 = 0x09f6 // ZC_ACK_DELETE_MAIL — clif_mail_delete
	HeaderZCACKREMOVEITEMM uint16 = 0x0a07 // ZC_ACK_REMOVE_ITEM_MAIL — clif_mail_removeitem
	HeaderZCACKOPENWRITE   uint16 = 0x0a12 // ZC_ACK_OPEN_WRITE_MAIL — clif_send_Mail_beginwrite_ack
	HeaderZCCHECKNAME      uint16 = 0x0a51 // ZC_CHECKNAME (≥20160302: + name) — clif_Mail_Receiver_Ack
	HeaderZCACKADDITEMRODE uint16 = 0x0b3f // ZC_ACK_ADD_ITEM_RODEX (≥20200916 branch) — clif_Mail_setattachment
	HeaderZCACKREADRODEX   uint16 = 0x0b63 // ZC_ACK_READ_RODEX (≥20200916 branch) — clif_Mail_read
	HeaderZCACKMAILLIST    uint16 = 0x0ac2 // ZC_ACK_MAIL_LIST (≥20170419 layout) — clif_Mail_refreshinbox
)

// On-wire sizes.
const (
	// 09e8/09ee/09ef: cmd + mail id.Q = 11 (clif_packetdb.hpp:1740,1745,1746).
	sizeCZOpenMailbox = 11
	// 0ac0/0ac1: cmd + mail id.Q + unknown.16B = 26 (clif_packetdb.hpp:1867).
	sizeCZOpenMailbox2 = 26
	// 09ea/09f5: cmd + mail tab.B + mail id.Q = 11 (clif_packetdb.hpp:1742,1752).
	sizeCZReadDeleteMail = 11
	// 09f1/09f3: cmd + mail id.Q + mail tab.B = 11 (clif_packetdb.hpp:1748,1750).
	sizeCZGetAttach = 11
	// 0a03: cmd only = 2; 0a04/0a06: cmd + index.W + count.W = 6 (:1754-1756).
	sizeCZCancelWrite = 2
	sizeCZMailItem    = 6
	// 0a08/0a13/0b97: cmd + receive name.24B (+ own_char.B on 0b97) (:1758, 27B per :2002).
	sizeCZOpenWriteMail = 26
	sizeCZCheckName2    = 27
	// 0a6e layout: cmd+len+recv24+sender24+zeny.Q+titleLen.W+textLen.W+charid.L
	// = 68 header before the strings (clif.cpp:16817 <char id>.L at 64,
	// strings at 68). 09ec lacks the char id slot (strings at 64).
	sizeCZWriteMail2Header = 68
	sizeCZWriteMailHeader  = 64
	// Minimum frame the send handler accepts (clif.cpp: "length < 0x3e" reject).
	minCZWriteMail = 0x3e

	sizeZCNotifyUnreadMail = 3  // cmd + result.B
	sizeZCAckWriteMail     = 3  // cmd + result.B
	sizeZCAckZenyFromMail  = 12 // cmd + mail id.Q + tab.B + result.B
	sizeZCAckItemFromMail  = 12
	sizeZCAckDeleteMail    = 11 // cmd + tab.B + mail id.Q
	sizeZCAckRemoveItem    = 9  // cmd + result.B + index.W + cnt.W + weight.W
	sizeZCAckOpenWrite     = 27 // cmd + receive name.24B + result.B
	sizeZCCheckName        = 34 // cmd + charid.L + class.W + level.W + name.24B
	// 0b3f: cmd+result.B+index.W+count.W+itemId.L+type.B+ident.B+damaged.B
	// +slot.16B+options.25B+weight.W+favorite.B+location.L+refine.B+grade.B
	// (PACKET_ZC_ACK_ADD_ITEM_RODEX ≥20200916; at 20250604 EQUIPSLOTINFO is
	// uint32 card[4] = 16B and MAX_ITEM_OPTIONS = MAX_ITEM_RDM_OPT = 5 × 5B).
	sizeZCAckAddItemRodex = 64
	// ZC_ACK_READ_RODEX fixed part: cmd+len+tab.B+id.Q+textLen.W+zeny.Q+cnt.B.
	sizeZCAckReadRodexHeader = 24
	// ZC_ACK_READ_RODEX_SUB ≥20200916: count.W+ITID.L+ident.B+damaged.B
	// +slot.16B+location.L+type.B+view.W+bind.W+options.25B+refine.B+grade.B.
	// options = MAX_ITEM_OPTIONS = MAX_ITEM_RDM_OPT = 5 × ItemOptions(5B).
	sizeZCAckReadRodexSub = 60
	// ZC_ACK_MAIL_LIST ≥20170419: cmd+len+unknown.B, then per-mail entries.
	sizeZCAckMailListHeader = 5
	// maillistinfo ≥20170419: openType.B+id.Q+read.B+type.B+sender.24B
	// +expires.L+titleLen.W before the title bytes.
	sizeZCMailListInfo = 41
)

// Public size constants for the dispatch table (mirrors the guild family's
// pattern: the dispatch table references the public names so the two cannot
// drift apart without a compile error).
const (
	// SizeCZOpenMailbox is CZ_OPEN_MAILBOX / CZ_REQ_NEXT_MAIL_LIST /
	// CZ_REQ_REFRESH_MAIL_LIST's frame size (11B).
	SizeCZOpenMailbox = sizeCZOpenMailbox
	// SizeCZOpenMailbox2 is CZ_OPEN_MAILBOX2 / CZ_REQ_REFRESH_MAIL_LIST2's
	// frame size (26B).
	SizeCZOpenMailbox2 = sizeCZOpenMailbox2
	// SizeCZReadDeleteMail is CZ_REQ_READ_MAIL / CZ_REQ_DELETE_MAIL's frame
	// size (11B).
	SizeCZReadDeleteMail = sizeCZReadDeleteMail
	// SizeCZGetAttach is CZ_REQ_ZENY_FROM_MAIL / CZ_REQ_ITEM_FROM_MAIL's
	// frame size (11B).
	SizeCZGetAttach = sizeCZGetAttach
	// SizeCZMailItem is CZ_REQ_ADD_ITEM_TO_MAIL / CZ_REQ_REMOVE_ITEM_MAIL's
	// frame size (6B).
	SizeCZMailItem = sizeCZMailItem
	// SizeCZOpenWriteMail is CZ_REQ_OPEN_WRITE_MAIL /
	// CZ_CHECK_RECEIVE_CHARACTER_NAME's frame size (26B).
	SizeCZOpenWriteMail = sizeCZOpenWriteMail
	// SizeCZCheckName2 is CZ_CHECKNAME2's frame size (27B).
	SizeCZCheckName2 = sizeCZCheckName2
)

// Mail inbox types (mmo.hpp mail_inbox_type) and mail flags
// (clif.cpp:91 mail_type enum — the MAIL_TYPE_* wire bits, distinct from the
// mmo.hpp mail_inbox_type).
const (
	MailInboxNormal   uint8 = 0
	MailInboxAccount  uint8 = 1
	MailInboxReturned uint8 = 2

	MailTypeText uint8 = 0x00
	MailTypeZeny uint8 = 0x02
	MailTypeItem uint8 = 0x04
	MailTypeNPC  uint8 = 0x08
)

// Mail status (mmo.hpp mail_status).
const (
	MailStatusNew    uint8 = 0
	MailStatusUnread uint8 = 1
	MailStatusRead   uint8 = 2
)

// ZC_ACK_WRITE_MAIL results (clif.hpp mail_send_result).
const (
	MailSendSuccess   uint8 = 0
	MailSendFailed    uint8 = 1
	MailSendFailedCnt uint8 = 2
)

// Attachment results for 09f2/09f4 (clif_mail_getattachment: 0 ok, 1 zeny
// overflow/failure, 2 inventory overflow/overweight).
const (
	MailAttachOK       uint8 = 0
	MailAttachFailed   uint8 = 1
	MailAttachOverflow uint8 = 2
)

// ZC_ACK_ADD_ITEM_RODEX / staging results (mail.hpp mail_attach_result,
// RODEX branch).
const (
	MailAddOK          uint8 = 0
	MailAddWeight      uint8 = 1
	MailAddError       uint8 = 2
	MailAddSpace       uint8 = 3
	MailAddUntradeable uint8 = 4
	MailAddEquipSwitch uint8 = 99
)

// MailItem is one attachment slot on the wire (ZC_ACK_READ_RODEX_SUB payload
// and the 0x0b3f ack share the field set). Cards are uint32 at ≥20181121
// (EQUIPSLOTINFO.card uint32[4]).
type MailItem struct {
	Index      uint16 // client inventory index (0x0b3f only)
	Count      uint16
	ITID       uint32 // client_nameid view id
	Type       uint8
	Identified bool
	Damaged    bool
	Refine     uint8
	Card       [4]uint32
	Location   uint32 // equip point bitmask
	View       uint16
	Bind       uint16
	Weight     uint16 // staged total weight (0x0b3f only)
	Favorite   bool   // 0x0b3f only
}

// CZMailReq is the decoded shared shape of the mailbox verbs that carry a
// 64-bit mail id: open/next/refresh inbox, read, delete, and the two
// collect-attachment verbs.
type CZMailReq struct {
	Op     uint16 // raw opcode (refreshinbox distinguishes next-page from open)
	MailID uint64
}

// ParseCZOpenMailbox decodes CZ_OPEN_MAILBOX / CZ_REQ_NEXT_MAIL_LIST /
// CZ_REQ_REFRESH_MAIL_LIST (11B).
func ParseCZOpenMailbox(frame []byte) (CZMailReq, error) {
	return parseCZMailID(frame, sizeCZOpenMailbox)
}

// ParseCZOpenMailbox2 decodes CZ_OPEN_MAILBOX2 / CZ_REQ_REFRESH_MAIL_LIST2
// (26B; the trailing 16 bytes are unknown to rAthena and ignored).
func ParseCZOpenMailbox2(frame []byte) (CZMailReq, error) {
	return parseCZMailID(frame, sizeCZOpenMailbox2)
}

// ParseCZReadMail decodes CZ_REQ_READ_MAIL (11B, tab.B then mail id).
func ParseCZReadMail(frame []byte) (CZMailReq, error) {
	return parseCZMailID(frame, sizeCZReadDeleteMail)
}

// ParseCZDeleteMail decodes CZ_REQ_DELETE_MAIL (11B).
func ParseCZDeleteMail(frame []byte) (CZMailReq, error) {
	return parseCZMailID(frame, sizeCZReadDeleteMail)
}

// parseCZMailID validates the header and extracts the 64-bit mail id at the
// given fixed size's offset. 11B frames keep the id at 3 (tab.B first); the
// 26B mailbox2 frames carry id.Q at 2 — clif_parse_Mail_refreshinbox
// hardcodes RFIFOQ(fd, 2) at ≥20170419 (the packetdb "2,10" trailing args are
// legacy pos[] entries that handler never reads).
func parseCZMailID(frame []byte, size int) (CZMailReq, error) {
	name := "mail request"
	off := uint64(2)
	if size == sizeCZReadDeleteMail || size == sizeCZOpenMailbox {
		off = 3
	}
	if len(frame) < size {
		return CZMailReq{}, fmt.Errorf("packet: parse %s: want at least %d bytes, got %d", name, size, len(frame))
	}
	op := binary.LittleEndian.Uint16(frame[0:2])
	return CZMailReq{Op: op, MailID: binary.LittleEndian.Uint64(frame[off : off+8])}, nil
}

// ParseCZGetAttach decodes CZ_REQ_ZENY_FROM_MAIL / CZ_REQ_ITEM_FROM_MAIL
// (11B: mail id.Q then tab.B).
func ParseCZGetAttach(frame []byte) (CZMailReq, error) {
	if len(frame) < sizeCZGetAttach {
		return CZMailReq{}, fmt.Errorf("packet: parse CZ_REQ_*_FROM_MAIL: want at least %d bytes, got %d", sizeCZGetAttach, len(frame))
	}
	return CZMailReq{Op: binary.LittleEndian.Uint16(frame[0:2]), MailID: binary.LittleEndian.Uint64(frame[2:10])}, nil
}

// Encode writes the CZ frame to w (e2e harness support).
func (m CZMailReq) Encode(w io.Writer) error {
	size := sizeCZOpenMailbox
	off := 2
	switch m.Op {
	case HeaderCZOPENMAILBOX2, HeaderCZREFRESHMAILLIST:
		size = sizeCZOpenMailbox2
	case HeaderCZREQREADMAIL, HeaderCZREQDELETEMAIL:
		size = sizeCZReadDeleteMail
		off = 3
	case HeaderCZREQZENYFROMMAIL, HeaderCZREQITEMFROMMAIL:
		size = sizeCZGetAttach
	}
	buf := make([]byte, size)
	binary.LittleEndian.PutUint16(buf[0:], m.Op)
	binary.LittleEndian.PutUint64(buf[off:], m.MailID)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write mail request: %w", err)
	}
	return nil
}

// CZOpenWriteMail is a decoded CZ_REQ_OPEN_WRITE_MAIL (0x0a08) or
// CZ_CHECK_RECEIVE_CHARACTER_NAME (0x0a13/0x0b97) — both carry the 24-byte
// receiver name; 0x0b97 adds the own-char flag byte rAthena ignores.
type CZOpenWriteMail struct {
	Op   uint16
	Name string
}

// ParseCZOpenWriteMail decodes CZ_REQ_OPEN_WRITE_MAIL (26B).
func ParseCZOpenWriteMail(frame []byte) (CZOpenWriteMail, error) {
	return parseCZMailName(frame, HeaderCZREQOPENWRITEMAI, sizeCZOpenWriteMail)
}

// ParseCZCheckReceiverName decodes CZ_CHECK_RECEIVE_CHARACTER_NAME (26B) and
// CZ_CHECKNAME2 (27B).
func ParseCZCheckReceiverName(frame []byte) (CZOpenWriteMail, error) {
	op := uint16(0)
	if len(frame) >= 2 {
		op = binary.LittleEndian.Uint16(frame[0:2])
	}
	size := sizeCZOpenWriteMail
	if op == HeaderCZCHECKNAME2 {
		size = sizeCZCheckName2
	}
	return parseCZMailName(frame, op, size)
}

func parseCZMailName(frame []byte, want uint16, size int) (CZOpenWriteMail, error) {
	if len(frame) < size {
		return CZOpenWriteMail{}, fmt.Errorf("packet: parse mail name frame 0x%04x: want at least %d bytes, got %d", want, size, len(frame))
	}
	if op := binary.LittleEndian.Uint16(frame[0:2]); op != want {
		return CZOpenWriteMail{}, fmt.Errorf("packet: parse mail name frame: unexpected cmd 0x%04x", op)
	}
	return CZOpenWriteMail{Op: want, Name: readNameField(frame, 2, 24)}, nil
}

// Encode writes the frame to w.
func (m CZOpenWriteMail) Encode(w io.Writer) error {
	size := sizeCZOpenWriteMail
	if m.Op == HeaderCZCHECKNAME2 {
		size = sizeCZCheckName2
	}
	buf := make([]byte, size)
	binary.LittleEndian.PutUint16(buf[0:], m.Op)
	writeNameField(buf, 2, m.Name)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write mail name frame: %w", err)
	}
	return nil
}

// CZMailAttachItem is a decoded CZ_REQ_ADD_ITEM_TO_MAIL /
// CZ_REQ_REMOVE_ITEM_MAIL (6B: client inventory index, count).
type CZMailAttachItem struct {
	Op    uint16
	Index uint16 // client inventory index (rAthena server_index subtracts 2)
	Count uint16
}

// ParseCZMailAddItem decodes CZ_REQ_ADD_ITEM_TO_MAIL.
func ParseCZMailAddItem(frame []byte) (CZMailAttachItem, error) {
	return parseCZMailAttachItem(frame, HeaderCZREQADDITEMMAIL)
}

// ParseCZMailRemoveItem decodes CZ_REQ_REMOVE_ITEM_MAIL.
func ParseCZMailRemoveItem(frame []byte) (CZMailAttachItem, error) {
	return parseCZMailAttachItem(frame, HeaderCZREQREMOVEITEMMA)
}

func parseCZMailAttachItem(frame []byte, want uint16) (CZMailAttachItem, error) {
	if len(frame) < sizeCZMailItem {
		return CZMailAttachItem{}, fmt.Errorf("packet: parse mail item attach 0x%04x: want at least %d bytes, got %d", want, sizeCZMailItem, len(frame))
	}
	if op := binary.LittleEndian.Uint16(frame[0:2]); op != want {
		return CZMailAttachItem{}, fmt.Errorf("packet: parse mail item attach: unexpected cmd 0x%04x", op)
	}
	return CZMailAttachItem{
		Op:    want,
		Index: binary.LittleEndian.Uint16(frame[2:4]),
		Count: binary.LittleEndian.Uint16(frame[4:6]),
	}, nil
}

// Encode writes the frame to w.
func (m CZMailAttachItem) Encode(w io.Writer) error {
	var buf [sizeCZMailItem]byte
	binary.LittleEndian.PutUint16(buf[0:], m.Op)
	binary.LittleEndian.PutUint16(buf[2:], m.Index)
	binary.LittleEndian.PutUint16(buf[4:], m.Count)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write mail item attach: %w", err)
	}
	return nil
}

// CZWriteMail is a decoded CZ_REQ_WRITE_MAIL (0x09ec) or CZ_REQ_WRITE_MAIL2
// (0x0a6e). ReceiverCharID rides 0x0a6e only; rAthena never reads it
// (clif.cpp:16817 leaves the slot unused — the name resolves the recipient).
type CZWriteMail struct {
	Op             uint16
	ReceiverName   string
	SenderName     string
	Zeny           uint64
	ReceiverCharID uint32
	TitleLength    int
	TextLength     int
	Title          string
	Body           string
}

// ParseCZWriteMail decodes either send opcode; the string layout differs by
// the 4-byte char-id slot 0x0a6e inserts before the strings.
func ParseCZWriteMail(frame []byte) (CZWriteMail, error) {
	if len(frame) < 2 {
		return CZWriteMail{}, fmt.Errorf("packet: parse CZ_REQ_WRITE_MAIL: truncated header")
	}
	op := binary.LittleEndian.Uint16(frame[0:2])
	if op != HeaderCZREQWRITEMAIL && op != HeaderCZREQWRITEMAIL2 {
		return CZWriteMail{}, fmt.Errorf("packet: parse CZ_REQ_WRITE_MAIL: unexpected cmd 0x%04x", op)
	}
	if len(frame) < minCZWriteMail {
		return CZWriteMail{}, fmt.Errorf("packet: parse CZ_REQ_WRITE_MAIL: frame too short (%d < %d)", len(frame), minCZWriteMail)
	}
	strBase := sizeCZWriteMailHeader
	if op == HeaderCZREQWRITEMAIL2 {
		strBase = sizeCZWriteMail2Header
	}
	m := CZWriteMail{
		Op:           op,
		ReceiverName: readNameField(frame, 4, 24),
		SenderName:   readNameField(frame, 28, 24),
		Zeny:         binary.LittleEndian.Uint64(frame[52:60]),
		TitleLength:  int(binary.LittleEndian.Uint16(frame[60:62])),
		TextLength:   int(binary.LittleEndian.Uint16(frame[62:64])),
	}
	if op == HeaderCZREQWRITEMAIL2 {
		m.ReceiverCharID = binary.LittleEndian.Uint32(frame[64:68])
	}
	if strBase+m.TitleLength > len(frame) {
		return CZWriteMail{}, fmt.Errorf("packet: parse CZ_REQ_WRITE_MAIL: title overruns frame (%d > %d)", strBase+m.TitleLength, len(frame))
	}
	m.Title = readNameField(frame, strBase, m.TitleLength)
	if strBase+m.TitleLength+m.TextLength > len(frame) {
		return CZWriteMail{}, fmt.Errorf("packet: parse CZ_REQ_WRITE_MAIL: body overruns frame")
	}
	m.Body = readNameField(frame, strBase+m.TitleLength, m.TextLength)
	return m, nil
}

// --- S→C encoders ---

// EncodeZCNotifyUnreadMail writes ZC_NOTIFY_UNREADMAIL (0x09e7, 3B) — the
// inbox icon; result 1 when unread (or unchecked) mail exists.
func EncodeZCNotifyUnreadMail(w io.Writer, unread bool) error {
	var buf [sizeZCNotifyUnreadMail]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYUNREADMA)
	if unread {
		buf[2] = 1
	}
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_UNREADMAIL: %w", err)
	}
	return nil
}

// MailListEntry is one inbox row (maillistinfo ≥20170419).
type MailListEntry struct {
	Type    uint8 // mail_inbox_type of the row
	MailID  uint64
	Read    bool
	Flags   uint8 // MAIL_TYPE_* bits: text/zeny/item/npc
	Sender  string
	Expires uint32 // seconds until scheduled deletion (rAthena fakes 1 year)
	Title   string
}

// EncodeZCAckMailList writes ZC_ACK_MAIL_LIST (0x0ac2, variable). At
// ≥20170419 the header carries no count — one entry per mail, IsEnd-equivalent
// unknown byte fixed at 1 (clif.cpp clif_Mail_refreshinbox WFIFOB(fd,4)=1).
func EncodeZCAckMailList(w io.Writer, entries []MailListEntry) error {
	size := sizeZCAckMailListHeader
	for _, e := range entries {
		size += sizeZCMailListInfo + len(e.Title) + 1 // title includes the NUL
	}
	buf := make([]byte, size)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKMAILLIST)
	binary.LittleEndian.PutUint16(buf[2:], uint16(size)) //nolint:gosec // G115: ≤ 30 inbox rows of bounded entries.
	buf[4] = 1
	off := sizeZCAckMailListHeader
	for _, e := range entries {
		buf[off] = e.Type
		binary.LittleEndian.PutUint64(buf[off+1:], e.MailID)
		if e.Read {
			buf[off+9] = 1
		}
		buf[off+10] = e.Flags
		copy(buf[off+11:off+11+24], e.Sender) // NUL-padded sender, 24B
		binary.LittleEndian.PutUint32(buf[off+35:], e.Expires)
		binary.LittleEndian.PutUint16(buf[off+39:], uint16(len(e.Title)+1)) //nolint:gosec // G115: titles are bounded to 40+1 upstream.
		copy(buf[off+41:], e.Title)
		buf[off+41+len(e.Title)] = 0
		off += sizeZCMailListInfo + len(e.Title) + 1
	}
	if off != size {
		return fmt.Errorf("packet: write ZC_ACK_MAIL_LIST: size mismatch %d != %d", off, size)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_MAIL_LIST: %w", err)
	}
	return nil
}

// MailReadItem is one attachment rendered in a ZC_ACK_READ_RODEX.
type MailReadItem struct {
	Count      uint16
	ITID       uint32
	Type       uint8
	Identified bool
	Damaged    bool
	Refine     uint8
	Card       [4]uint32
	Location   uint32
	View       uint16
	Bind       uint16
}

// EncodeZCAckReadRodex writes ZC_ACK_READ_RODEX (0x0b63, variable): fixed
// header, body text (NUL-terminated), then one 42-byte sub block per item.
func EncodeZCAckReadRodex(w io.Writer, openType uint8, mailID uint64, zeny uint64, body string, items []MailReadItem) error {
	textLen := len(body) + 1
	if len(body) == 0 {
		// rAthena substitutes "(no message)" for an empty body
		// (clif.cpp clif_Mail_read) so the client never renders a zero-length
		// text block.
		body = "(no message)"
		textLen = len(body) + 1
	}
	size := sizeZCAckReadRodexHeader + textLen + len(items)*sizeZCAckReadRodexSub
	buf := make([]byte, size)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKREADRODEX)
	binary.LittleEndian.PutUint16(buf[2:], uint16(size)) //nolint:gosec // G115: body ≤ 501, items ≤ 5.
	buf[4] = openType
	binary.LittleEndian.PutUint64(buf[5:], mailID)
	binary.LittleEndian.PutUint16(buf[13:], uint16(textLen)) //nolint:gosec // G115: bounded above.
	binary.LittleEndian.PutUint64(buf[15:], zeny)
	buf[23] = uint8(len(items)) //nolint:gosec // G115: MAIL_MAX_ITEM = 5.
	copy(buf[24:], body)
	buf[24+len(body)] = 0
	off := sizeZCAckReadRodexHeader + textLen
	for _, it := range items {
		binary.LittleEndian.PutUint16(buf[off:], it.Count)
		binary.LittleEndian.PutUint32(buf[off+2:], it.ITID)
		if it.Identified {
			buf[off+6] = 1
		}
		if it.Damaged {
			buf[off+7] = 1
		}
		for i, c := range it.Card {
			binary.LittleEndian.PutUint32(buf[off+8+i*4:], c)
		}
		binary.LittleEndian.PutUint32(buf[off+24:], it.Location)
		buf[off+28] = it.Type
		binary.LittleEndian.PutUint16(buf[off+29:], it.View)
		binary.LittleEndian.PutUint16(buf[off+31:], it.Bind)
		buf[off+58] = it.Refine
		// grade (off+59) stays 0 — enchantgrade is not modeled yet.
		off += sizeZCAckReadRodexSub
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_READ_RODEX: %w", err)
	}
	return nil
}

// EncodeZCAckOpenWriteMail writes ZC_ACK_OPEN_WRITE_MAIL (0x0a12, 27B).
func EncodeZCAckOpenWriteMail(w io.Writer, receiverName string, ok bool) error {
	var buf [sizeZCAckOpenWrite]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKOPENWRITE)
	writeNameField(buf[:], 2, receiverName)
	if ok {
		buf[26] = 1
	}
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_OPEN_WRITE_MAIL: %w", err)
	}
	return nil
}

// EncodeZCCheckName writes ZC_CHECKNAME (0x0a51, 34B) — the recipient
// preview row (char id, class, base level, name).
func EncodeZCCheckName(w io.Writer, charID uint32, class uint16, baseLevel uint16, name string) error {
	var buf [sizeZCCheckName]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCCHECKNAME)
	binary.LittleEndian.PutUint32(buf[2:], charID)
	binary.LittleEndian.PutUint16(buf[6:], class)
	binary.LittleEndian.PutUint16(buf[8:], baseLevel)
	writeNameField(buf[:], 10, name)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_CHECKNAME: %w", err)
	}
	return nil
}

// EncodeZCAckWriteMail writes ZC_ACK_WRITE_MAIL (0x09ed, 3B).
func EncodeZCAckWriteMail(w io.Writer, result uint8) error {
	var buf [sizeZCAckWriteMail]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKMAILWRITE)
	buf[2] = result
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_WRITE_MAIL: %w", err)
	}
	return nil
}

// EncodeZCAckAddItemRodex writes ZC_ACK_ADD_ITEM_RODEX (0x0b3f, 46B).
// result != 0 sends the zero-item failure form (rAthena memsets the struct
// and only fills result).
func EncodeZCAckAddItemRodex(w io.Writer, result uint8, it MailItem) error {
	var buf [sizeZCAckAddItemRodex]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKADDITEMRODE)
	buf[2] = result
	if result == MailAddOK {
		binary.LittleEndian.PutUint16(buf[3:], it.Index)
		binary.LittleEndian.PutUint16(buf[5:], it.Count)
		binary.LittleEndian.PutUint32(buf[7:], it.ITID)
		buf[11] = it.Type
		if it.Identified {
			buf[12] = 1
		}
		if it.Damaged {
			buf[13] = 1
		}
		for i, c := range it.Card {
			binary.LittleEndian.PutUint32(buf[14+i*4:], c)
		}
		// options 25B (off+30..54) stay zero — random options not modeled.
		binary.LittleEndian.PutUint16(buf[55:], it.Weight)
		if it.Favorite {
			buf[57] = 1
		}
		binary.LittleEndian.PutUint32(buf[58:], it.Location)
		buf[62] = it.Refine
		// grade (buf[63]) stays 0 — enchantgrade is not modeled yet.
	}
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_ADD_ITEM_RODEX: %w", err)
	}
	return nil
}

// EncodeZCAckRemoveItemMail writes ZC_ACK_REMOVE_ITEM_MAIL (0x0a07, 9B);
// weight is the staged total after the removal.
func EncodeZCAckRemoveItemMail(w io.Writer, ok bool, index, count, weight uint16) error {
	var buf [sizeZCAckRemoveItem]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKREMOVEITEMM)
	if ok {
		buf[2] = 1
	}
	binary.LittleEndian.PutUint16(buf[3:], index)
	binary.LittleEndian.PutUint16(buf[5:], count)
	binary.LittleEndian.PutUint16(buf[7:], weight)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_REMOVE_ITEM_MAIL: %w", err)
	}
	return nil
}

// encodeZCAckCollect writes the shared 09f2/09f4 collect-attachment ack.
func encodeZCAckCollect(w io.Writer, cmd uint16, mailID uint64, openType, result uint8) error {
	var buf [sizeZCAckZenyFromMail]byte
	binary.LittleEndian.PutUint16(buf[0:], cmd)
	binary.LittleEndian.PutUint64(buf[2:], mailID)
	buf[10] = openType
	buf[11] = result
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write mail collect ack 0x%04x: %w", cmd, err)
	}
	return nil
}

// EncodeZCAckZenyFromMail writes ZC_ACK_ZENY_FROM_MAIL (0x09f2, 12B).
func EncodeZCAckZenyFromMail(w io.Writer, mailID uint64, openType, result uint8) error {
	return encodeZCAckCollect(w, HeaderZCACKZENYFROMMAI, mailID, openType, result)
}

// EncodeZCAckItemFromMail writes ZC_ACK_ITEM_FROM_MAIL (0x09f4, 12B).
func EncodeZCAckItemFromMail(w io.Writer, mailID uint64, openType, result uint8) error {
	return encodeZCAckCollect(w, HeaderZCACKITEMFROMMAI, mailID, openType, result)
}

// EncodeZCAckDeleteMail writes ZC_ACK_DELETE_MAIL (0x09f6, 11B) — success
// only; rAthena stays silent on failure (clif_mail_delete).
func EncodeZCAckDeleteMail(w io.Writer, openType uint8, mailID uint64) error {
	var buf [sizeZCAckDeleteMail]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKDELETEMAIL)
	buf[2] = openType
	binary.LittleEndian.PutUint64(buf[3:], mailID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_DELETE_MAIL: %w", err)
	}
	return nil
}
