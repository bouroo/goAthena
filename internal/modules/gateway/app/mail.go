package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/panjf2000/gnet/v2"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	mailapp "github.com/bouroo/goAthena/internal/modules/social/mail/app"
	maildomain "github.com/bouroo/goAthena/internal/modules/social/mail/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// Mail (RODEX) verb handlers. The mail state machine lives in the mail
// module; this file owns the wire conversation and the compose-window
// staging.
//
// Compose staging (staged zeny + items + the mail_writing flag) lives HERE,
// not in the mail module, mirroring rAthena: sd->mail (mail.zeny /
// mail.item[]) is session state, and clif_parse_Mail_send lifts it into
// the send call. A second open replaces the first (rAthena mail_clear), a
// cancel drops it, and a disconnect prunes it via unregisterConn.
type mailStaging struct {
	writing bool
	zeny    int64
	items   []stagedMailItem
}

// stagedMailItem is one staged attachment: a bag row snapshot plus the
// amount the sender chose. rowID carries the bag row id so Send's Remove
// lifts the exact row (rAthena mail_setattachment re-reads the bag row at
// send time and fails if it moved). serverIdx is the inventory server row
// the client's remove-verb names (rAthena sd->mail.item[i].index).
type stagedMailItem struct {
	rowID     invdomain.ItemID
	serverIdx uint16
	nameID    uint32
	amount    uint32
}

// maxMailStagedWeight mirrors battle_config.mail_attachment_weight (2000):
// the staged total weight cap. rAthena's itemdb weights are /10 units; the
// itemdb Registry is optional, so a nil registry skips the weight check
// (degrade, not refuse — the same policy as the LoadEndAck sprite lookup).
const maxMailStagedWeight = 2000

// mailFakeExpiry is the ZC_ACK_MAIL_LIST "expires" field: rAthena fakes a
// 1-year countdown (clif.cpp clif_Mail_refreshinbox) because the mail
// return/delete timers are deferred.
const mailFakeExpiry = 365 * 24 * 60 * 60

// maxMailZeny caps the send frame's zeny before the uint64→int64 narrowing
// (rAthena MAX_ZENY); anything above is a forged frame and fails the send.
const maxMailZeny = 2_000_000_000

// zenyFeePercent mirrors battle_config.mail_zeny_fee (2): the staged-zeny
// fee check (rAthena mail_setitem idx==0) and the service fee share it.
const zenyFeePercent = 2

// SetMail attaches the mail service post-construction, mirroring SetGuild.
// A nil service degrades every mail dispatch entry to a log-and-skip.
func (s *MapServer) SetMail(svc *mailapp.MailService) { s.mail = svc }

// toMailID narrows the wire's 64-bit id into the domain type. Ids beyond
// int63 cannot exist (auto-increment) and simply match no row — the lookup
// fails closed on a forged id.
func toMailID(u uint64) maildomain.MailID {
	return maildomain.MailID(u) //nolint:gosec // G115: see above.
}

// SetCharRepo attaches the character repository post-construction (the
// staged-zeny balance check reads the char row the same way
// EconomyService.GetZeny does). Optional: a nil repo skips the check.
func (s *MapServer) SetCharRepo(repo chardomain.CharacterRepository) { s.charRepo = repo }

func (s *MapServer) mailSlot(charID uint32) (mailStaging, bool) {
	s.mailMu.RLock()
	defer s.mailMu.RUnlock()
	st, ok := s.mailStaging[charID]
	return st, ok
}

func (s *MapServer) setMailSlot(charID uint32, st mailStaging) {
	s.mailMu.Lock()
	defer s.mailMu.Unlock()
	s.mailStaging[charID] = st
}

func (s *MapServer) clearMailSlot(charID uint32) {
	s.mailMu.Lock()
	defer s.mailMu.Unlock()
	delete(s.mailStaging, charID)
}

// lockMailOps serializes one char's mail verbs. OnTraffic runs each frame on
// its own goroutine, and the mail conversation is check-then-act end to end:
// the staging read-mutate-write, the collect claim, and the send's writing
// gate all assume a char's frames arrive one at a time — rAthena parses each
// session on a single thread, and this restores that ordering. Deliberately
// not evicted: one small mutex per char that has ever used mail.
func (s *MapServer) lockMailOps(charID uint32) func() {
	l, _ := s.mailOps.LoadOrStore(charID, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// handleOpenMailbox processes CZ_OPEN_MAILBOX / CZ_REQ_NEXT_MAIL_LIST /
// CZ_REQ_REFRESH_MAIL_LIST / CZ_OPEN_MAILBOX2 / CZ_REQ_REFRESH_MAIL_LIST2 —
// all five are the same verb in rAthena (clif_parse_Mail_refreshinbox):
// load the inbox and list it.
func (s *MapServer) handleOpenMailbox(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring inbox request")
		return
	}
	res, err := s.mail.Inbox(context.Background(), auth.charID)
	if err != nil {
		s.log.Warn("map: mail inbox", "gid", auth.charID, "err", err)
		return
	}
	var burst []byte
	burst = s.appendMailList(burst, res.Mails)
	_ = c.AsyncWrite(burst, nil)
}

// handleCloseMailbox processes CZ_CLOSE_MAILBOX — rAthena routes it to
// clif_parse_dull (no-op); the client closes its window itself.
func (s *MapServer) handleCloseMailbox(_ gnet.Conn, _ *mapAuth, _ []byte) {}

// handleReadMail processes CZ_REQ_READ_MAIL (rAthena clif_parse_Mail_read):
// the full message + attachments, and the row flips NEW/UNREAD → READ on
// first open.
func (s *MapServer) handleReadMail(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_REQ_READ_MAIL")
		return
	}
	req, err := ropacket.ParseCZReadMail(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_READ_MAIL", "err", err)
		return
	}
	m, err := s.mail.Read(context.Background(), auth.charID, toMailID(req.MailID))
	if err != nil {
		s.log.Debug("map: mail read", "gid", auth.charID, "id", req.MailID, "err", err)
		return
	}
	var burst []byte
	burst = s.appendMailRead(burst, m)
	_ = c.AsyncWrite(burst, nil)
}

// handleDeleteMail processes CZ_REQ_DELETE_MAIL. rAthena refuses while any
// attachment remains (clif_parse_Mail_delete) and stays silent on failure —
// only a success sends ZC_ACK_DELETE_MAIL.
func (s *MapServer) handleDeleteMail(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_REQ_DELETE_MAIL")
		return
	}
	req, err := ropacket.ParseCZDeleteMail(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_DELETE_MAIL", "err", err)
		return
	}
	if err := s.mail.Delete(context.Background(), auth.charID, toMailID(req.MailID)); err != nil {
		return // rAthena is silent on failure
	}
	s.writeMailEncoder(c, func(w io.Writer) error {
		return ropacket.EncodeZCAckDeleteMail(w, 0, req.MailID)
	}, "ZC_ACK_DELETE_MAIL")
}

// handleCollectZeny processes CZ_REQ_ZENY_FROM_MAIL (rAthena
// clif_parse_Mail_getattach MAIL_ATT_ZENY). The 09f2 ack carries the
// result: 0 ok, 1 zeny overflow/failure, 2 inventory overflow.
func (s *MapServer) handleCollectZeny(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_REQ_ZENY_FROM_MAIL")
		return
	}
	req, err := ropacket.ParseCZGetAttach(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_ZENY_FROM_MAIL", "err", err)
		return
	}
	if err := s.mail.CollectZeny(context.Background(), auth.charID, toMailID(req.MailID)); err != nil {
		if errors.Is(err, maildomain.ErrMailNotFound) {
			return // rAthena: nothing to collect, stay silent
		}
		s.writeMailEncoder(c, func(w io.Writer) error {
			return ropacket.EncodeZCAckZenyFromMail(w, req.MailID, 0, 1)
		}, "ZC_ACK_ZENY_FROM_MAIL")
		return
	}
	s.writeMailEncoder(c, func(w io.Writer) error {
		return ropacket.EncodeZCAckZenyFromMail(w, req.MailID, 0, 0)
	}, "ZC_ACK_ZENY_FROM_MAIL")
	s.refreshMailInbox(c, auth.charID)
}

// handleCollectItems processes CZ_REQ_ITEM_FROM_MAIL (rAthena
// clif_parse_Mail_getattach MAIL_ATT_ITEM).
func (s *MapServer) handleCollectItems(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_REQ_ITEM_FROM_MAIL")
		return
	}
	req, err := ropacket.ParseCZGetAttach(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_ITEM_FROM_MAIL", "err", err)
		return
	}
	if err := s.mail.CollectItems(context.Background(), auth.charID, toMailID(req.MailID)); err != nil {
		if errors.Is(err, maildomain.ErrMailNotFound) {
			return // rAthena: no items, stay silent
		}
		s.writeMailEncoder(c, func(w io.Writer) error {
			return ropacket.EncodeZCAckItemFromMail(w, req.MailID, 0, 2)
		}, "ZC_ACK_ITEM_FROM_MAIL")
		return
	}
	s.writeMailEncoder(c, func(w io.Writer) error {
		return ropacket.EncodeZCAckItemFromMail(w, req.MailID, 0, 0)
	}, "ZC_ACK_ITEM_FROM_MAIL")
	s.refreshMailInbox(c, auth.charID)
}

// handleOpenWriteMail processes CZ_REQ_OPEN_WRITE_MAIL (rAthena
// clif_parse_Mail_beginwrite): open the compose window. A compose window
// already open → refuse (rAthena's mail_writing guard).
func (s *MapServer) handleOpenWriteMail(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_REQ_OPEN_WRITE_MAIL")
		return
	}
	req, err := ropacket.ParseCZOpenWriteMail(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_OPEN_WRITE_MAIL", "err", err)
		return
	}
	if st, ok := s.mailSlot(auth.charID); ok && st.writing {
		s.writeMailEncoder(c, func(w io.Writer) error {
			return ropacket.EncodeZCAckOpenWriteMail(w, req.Name, false)
		}, "ZC_ACK_OPEN_WRITE_MAIL")
		return
	}
	s.setMailSlot(auth.charID, mailStaging{writing: true})
	s.writeMailEncoder(c, func(w io.Writer) error {
		return ropacket.EncodeZCAckOpenWriteMail(w, req.Name, true)
	}, "ZC_ACK_OPEN_WRITE_MAIL")
}

// handleCancelWriteMail processes CZ_REQ_CANCEL_WRITE_MAIL (rAthena
// clif_parse_Mail_cancelwrite): drop the staging. The zeny was never
// deducted (it only moves at send), so nothing to refund.
func (s *MapServer) handleCancelWriteMail(_ gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	s.clearMailSlot(auth.charID)
}

// handleCheckReceiverName processes CZ_CHECK_RECEIVE_CHARACTER_NAME /
// CZ_CHECKNAME2 (rAthena clif_parse_Mail_Receiver_Check): the compose
// window's recipient preview row.
func (s *MapServer) handleCheckReceiverName(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_CHECK_RECEIVE_CHARACTER_NAME")
		return
	}
	req, err := ropacket.ParseCZCheckReceiverName(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_CHECK_RECEIVE_CHARACTER_NAME", "err", err)
		return
	}
	rec, err := s.mail.CheckReceiver(context.Background(), req.Name)
	if err != nil {
		return // rAthena stays silent on an unknown name
	}
	s.writeMailEncoder(c, func(w io.Writer) error {
		return ropacket.EncodeZCCheckName(w, rec.CharID, rec.Class, rec.BaseLevel, rec.Name)
	}, "ZC_CHECKNAME")
}

// handleAddItemToMail processes CZ_REQ_ADD_ITEM_TO_MAIL (rAthena
// clif_parse_Mail_setattach): stage zeny (index 0) or an item (index ≥ 2,
// server row = index-2) into the compose window.
func (s *MapServer) handleAddItemToMail(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_REQ_ADD_ITEM_TO_MAIL")
		return
	}
	req, err := ropacket.ParseCZMailAddItem(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_ADD_ITEM_TO_MAIL", "err", err)
		return
	}
	st, ok := s.mailSlot(auth.charID)
	if !ok || !st.writing {
		return // rAthena mail_invalid_operation: no compose window
	}
	if req.Index == 0 {
		s.addZenyToMail(c, auth, st, req.Count)
		return
	}
	st, ack, failCode := s.stageMailItem(auth, st, req)
	if failCode != 0 {
		s.ackMailAddItem(c, failCode, ropacket.MailItem{})
		return
	}
	s.ackMailAddItem(c, 0, ack)
}

// addZenyToMail stages the compose window's zeny amount (rAthena mail_setitem
// idx==0): the amount plus the send fee must fit the sender's balance.
func (s *MapServer) addZenyToMail(c gnet.Conn, auth *mapAuth, st mailStaging, count uint16) {
	amount := int64(count) //nolint:gosec // G115: uint16→int64 is lossless.
	fee := amount * zenyFeePercent / 100
	bal, err := s.mailZenyBalance(auth.charID)
	if err != nil {
		s.log.Warn("map: mail staged-zeny balance", "gid", auth.charID, "err", err)
		return // balance-read failure: logged + skipped, the handler's degrade
	}
	if amount+fee > bal {
		s.ackMailAddItem(c, 2, ropacket.MailItem{})
		return
	}
	st.zeny = amount
	s.setMailSlot(auth.charID, st)
	s.ackMailAddItem(c, 0, ropacket.MailItem{Index: 0, Count: count})
}

// ackMailAddItem writes ZC_ACK_ADD_ITEM_RODEX; result != 0 sends the
// zero-item failure form (rAthena memsets the struct and fills only result).
func (s *MapServer) ackMailAddItem(c gnet.Conn, result uint8, it ropacket.MailItem) {
	s.writeMailEncoder(c, func(w io.Writer) error {
		return ropacket.EncodeZCAckAddItemRodex(w, result, it)
	}, "ZC_ACK_ADD_ITEM_RODEX")
}

// stageMailItem resolves the bag row behind the client's add-item verb,
// merges the count into the staged row with the same nameid (or appends a
// new slot), and enforces the staged weight cap. Returns the updated staging,
// the ack item, and a non-zero ZC_ACK_ADD_ITEM_RODEX failure code on refusal
// (2 = bad row/amount, 3 = no free slot, 1 = too heavy).
func (s *MapServer) stageMailItem(auth *mapAuth, st mailStaging, req ropacket.CZMailAttachItem) (mailStaging, ropacket.MailItem, uint8) {
	row := int(ropacket.ServerIndex(req.Index))
	items, err := s.inv.LoadByChar(context.Background(), auth.accountID, auth.charID)
	if err != nil {
		s.log.Warn("map: mail add-item load inventory", "gid", auth.charID, "err", err)
		return st, ropacket.MailItem{}, 2
	}
	if row < 0 || row >= len(items) || items[row].Amount < uint32(req.Count) { //nolint:gosec // G115: uint16→uint32 is lossless.
		return st, ropacket.MailItem{}, 2
	}
	if items[row].IsEquipped() {
		// A worn row cannot be mailed (rAthena mail_setitem returns
		// MAIL_ATTACH_EQUIPSWITCH for the swap slot).
		return st, ropacket.MailItem{}, ropacket.MailAddEquipSwitch
	}
	// Merge only into the staged entry backed by the SAME bag row — the send
	// lifts by rowID, so a same-nameid different-row merge would validate
	// against one row's amount and lift from another's.
	merged := false
	for i := range st.items {
		if st.items[i].rowID == items[row].ID {
			if st.items[i].amount+uint32(req.Count) > items[row].Amount { //nolint:gosec // G115
				return st, ropacket.MailItem{}, 2
			}
			st.items[i].amount += uint32(req.Count) //nolint:gosec // G115
			merged = true
			break
		}
	}
	if !merged {
		if len(st.items) >= maildomain.MaxItems {
			return st, ropacket.MailItem{}, 3
		}
		st.items = append(st.items, stagedMailItem{
			rowID:     items[row].ID,
			serverIdx: uint16(row), //nolint:gosec // G115: bounded by MAX_INVENTORY.
			nameID:    items[row].NameID,
			amount:    uint32(req.Count), //nolint:gosec // G115: uint16→uint32 is lossless.
		})
	}
	if s.itemDB != nil && st.stagedWeight(s.itemDB) > maxMailStagedWeight {
		return st, ropacket.MailItem{}, 1
	}
	s.setMailSlot(auth.charID, st)
	return st, ropacket.MailItem{Index: req.Index, Count: req.Count, ITID: items[row].NameID}, 0
}

// handleRemoveItemFromMail processes CZ_REQ_REMOVE_ITEM_MAIL (rAthena
// clif_parse_Mail_winopen, RODEX branch: index.W + count.W): unstage one
// attachment. The ack carries the staged total weight after the removal.
func (s *MapServer) handleRemoveItemFromMail(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_REQ_REMOVE_ITEM_MAIL")
		return
	}
	req, err := ropacket.ParseCZMailRemoveItem(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_REMOVE_ITEM_MAIL", "err", err)
		return
	}
	st, ok := s.mailSlot(auth.charID)
	if !ok || !st.writing {
		return
	}
	removed := false
	if req.Index == 0 {
		st.zeny = 0
		removed = true
	} else {
		serverIdx := ropacket.ServerIndex(req.Index)
		for i := range st.items {
			if st.items[i].serverIdx == serverIdx {
				st.items = append(st.items[:i], st.items[i+1:]...)
				removed = true
				break
			}
		}
	}
	if removed {
		s.setMailSlot(auth.charID, st)
	}
	weight := uint16(0)
	if s.itemDB != nil {
		weight = uint16(st.stagedWeight(s.itemDB)) //nolint:gosec // G115: capped above.
	}
	s.writeMailEncoder(c, func(w io.Writer) error {
		return ropacket.EncodeZCAckRemoveItemMail(w, removed, req.Index, req.Count, weight)
	}, "ZC_ACK_REMOVE_ITEM_MAIL")
}

// handleWriteMail processes CZ_REQ_WRITE_MAIL / CZ_REQ_WRITE_MAIL2 (rAthena
// clif_parse_Mail_send): lift the staged attachments, charge zeny + fees,
// deliver. On success the sender gets ZC_ACK_WRITE_MAIL 0 and the online
// receiver gets the unread-mail icon.
func (s *MapServer) handleWriteMail(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	unlock := s.lockMailOps(auth.charID)
	defer unlock()
	if s.mail == nil {
		s.log.Debug("map: mail not wired, ignoring CZ_REQ_WRITE_MAIL")
		return
	}
	req, err := ropacket.ParseCZWriteMail(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_WRITE_MAIL", "err", err)
		return
	}
	st, ok := s.mailSlot(auth.charID)
	if !ok || !st.writing {
		s.writeMailEncoder(c, func(w io.Writer) error {
			return ropacket.EncodeZCAckWriteMail(w, 1)
		}, "ZC_ACK_WRITE_MAIL")
		return
	}
	// The send frame's zeny is authoritative (the client resends the full
	// staged zeny); fall back to the staging when the frame carries 0. A
	// forged frame value above the cap (or one that would wrap int64
	// negative, disabling the charge) fails the send without dropping the
	// compose window — rAthena's mail_setitem failure keeps sd->mail.
	if req.Zeny > maxMailZeny {
		s.writeMailEncoder(c, func(w io.Writer) error {
			return ropacket.EncodeZCAckWriteMail(w, 1)
		}, "ZC_ACK_WRITE_MAIL")
		return
	}
	zeny := int64(req.Zeny) //nolint:gosec // G115: capped to MAX_ZENY above.
	if zeny == 0 && st.zeny > 0 {
		zeny = st.zeny
	}
	items := make([]maildomain.Attachment, 0, len(st.items))
	for _, it := range st.items {
		items = append(items, maildomain.Attachment{
			UniqueID: uint64(it.rowID), //nolint:gosec // G115: row id is a uint32 value domain.
			NameID:   it.nameID,
			Amount:   it.amount,
		})
	}
	senderName := playerName(s.world, auth.charID)
	dest, err := s.mail.Send(context.Background(), auth.charID, senderName, req.ReceiverName, req.Title, req.Body, zeny, items)
	if err != nil {
		// rAthena mail_deliveryfail: the staging is dropped (the charge and
		// lifted items are compensated by the service).
		s.clearMailSlot(auth.charID)
		s.writeMailEncoder(c, func(w io.Writer) error {
			return ropacket.EncodeZCAckWriteMail(w, 1)
		}, "ZC_ACK_WRITE_MAIL")
		return
	}
	s.clearMailSlot(auth.charID)
	s.writeMailEncoder(c, func(w io.Writer) error {
		return ropacket.EncodeZCAckWriteMail(w, 0)
	}, "ZC_ACK_WRITE_MAIL")
	// The online receiver gets the unread-mail icon (rAthena
	// intif_parse_Mail_new → clif_Mail_new).
	s.notifyMailNew(dest)
}

// --- delivery helpers ---

// appendMailList encodes ZC_ACK_MAIL_LIST for the given inbox rows into buf.
// The wire order is newest-first: rAthena's mail_fromsql loads ascending by
// id and clif_Mail_refreshinbox walks the array backwards onto the wire.
func (s *MapServer) appendMailList(buf []byte, mails []maildomain.Mail) []byte {
	entries := make([]ropacket.MailListEntry, 0, len(mails))
	for i := len(mails) - 1; i >= 0; i-- {
		m := mails[i]
		flags := uint8(0)
		if m.Zeny > 0 {
			flags |= ropacket.MailTypeZeny
		}
		if len(m.Items) > 0 {
			flags |= ropacket.MailTypeItem
		}
		entries = append(entries, ropacket.MailListEntry{
			Type:    uint8(m.Type), //nolint:gosec // G115: bounded by the mail type enum.
			MailID:  uint64(m.ID),  //nolint:gosec // G115: mail id is a uint64 value domain.
			Read:    m.Status != maildomain.StatusUnread,
			Flags:   flags,
			Sender:  m.SenderNam,
			Expires: mailFakeExpiry,
			Title:   m.Title,
		})
	}
	var tmp bytes.Buffer
	if err := ropacket.EncodeZCAckMailList(&tmp, entries); err != nil {
		s.log.Error("map: encode ZC_ACK_MAIL_LIST", "err", err)
		return buf
	}
	return append(buf, tmp.Bytes()...)
}

// appendMailRead encodes ZC_ACK_READ_RODEX for one mail into buf. The
// attachment row carries the send-time snapshot (cards, refine, bound), the
// itemdb entry contributes the client-side rendering fields (type, view
// sprite, equip-point bitmask) — the same split clif_Mail_read uses
// (item->* vs item_data->*).
func (s *MapServer) appendMailRead(buf []byte, m maildomain.Mail) []byte {
	items := make([]ropacket.MailReadItem, 0, len(m.Items))
	for _, it := range m.Items {
		wireType := uint8(itemdb.WireType("") & 0xff) //nolint:gosec // G115: IT_* values are small.
		var location uint32
		var view uint16
		if entry := s.itemEntry(it.NameID); entry != nil {
			wireType = uint8(itemdb.WireType(entry.Type) & 0xff) //nolint:gosec // G115: IT_* values are small.
			location = entry.EquipLocations
			if entry.View > 0 && entry.View <= 0xffff {
				view = uint16(entry.View) //nolint:gosec // G115: range-checked above.
			}
		}
		var bind uint16
		if it.Bound != 0 {
			bind = 2 // rAthena: item->bound ? 2 : flag.bindOnEquip ? 1 : 0
		}
		items = append(items, ropacket.MailReadItem{
			Count:      uint16(it.Amount), //nolint:gosec // G115: bounded by MAX_INVENTORY.
			ITID:       it.NameID,
			Type:       wireType,
			Identified: it.Identify != 0,
			Damaged:    it.Attribute != 0,
			Refine:     it.Refine,
			Card:       [4]uint32{it.Card0, it.Card1, it.Card2, it.Card3},
			Location:   location,
			View:       view,
			Bind:       bind,
		})
	}
	var tmp bytes.Buffer
	if err := ropacket.EncodeZCAckReadRodex(&tmp, 0, uint64(m.ID), uint64(m.Zeny), m.Body, items); err != nil { //nolint:gosec // G115: bounded.
		s.log.Error("map: encode ZC_ACK_READ_RODEX", "err", err)
		return buf
	}
	return append(buf, tmp.Bytes()...)
}

// refreshMailInbox re-sends the inbox list to a char (rAthena
// clif_Mail_refreshinbox after a collect/delete).
func (s *MapServer) refreshMailInbox(c gnet.Conn, charID uint32) {
	if s.mail == nil {
		return
	}
	res, err := s.mail.Inbox(context.Background(), charID)
	if err != nil {
		return
	}
	var burst []byte
	burst = s.appendMailList(burst, res.Mails)
	_ = c.AsyncWrite(burst, nil)
}

// notifyMailNew pushes the unread-mail icon to an online receiver (rAthena
// intif_parse_Mail_new → clif_Mail_new).
func (s *MapServer) notifyMailNew(charID uint32) {
	pc, ok := s.connFor(charID)
	if !ok {
		return
	}
	var buf bytes.Buffer
	if err := ropacket.EncodeZCNotifyUnreadMail(&buf, true); err != nil {
		s.log.Error("map: encode ZC_NOTIFY_UNREADMAIL", "err", err)
		return
	}
	_ = pc.AsyncWrite(buf.Bytes(), nil)
}

// mailZenyBalance reads the sender's zeny via the character repository (the
// same source EconomyService.GetZeny reads), keeping the gateway off the
// economy module.
func (s *MapServer) mailZenyBalance(charID uint32) (int64, error) {
	if s.charRepo == nil {
		return 0, errors.New("mail: character repository not wired")
	}
	c, err := s.charRepo.FindByID(context.Background(), chardomain.CharID(charID)) //nolint:gosec // G115: charID is a uint32 value domain.
	if err != nil {
		return 0, fmt.Errorf("mail: staged-zeny balance: %w", err)
	}
	return int64(c.Zeny), nil //nolint:gosec // G115: zeny is bounded.
}

// writeMailEncoder writes one S→C mail frame built by a packet codec
// (the mail codecs are free functions, not Size/Encode structs like the
// guild family, so the gateway adapts them to the AsyncWrite path here).
func (s *MapServer) writeMailEncoder(c gnet.Conn, enc func(io.Writer) error, name string) {
	var buf bytes.Buffer
	if err := enc(&buf); err != nil {
		s.log.Error("map: encode "+name, "err", err)
		return
	}
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// stagedWeight sums the staged items' weight (rAthena itemdb units /10).
func (st mailStaging) stagedWeight(items *itemdb.Registry) uint32 {
	var total uint32
	for _, it := range st.items {
		total += it.amount * items.Weight(it.nameID) / 10
	}
	return total
}
