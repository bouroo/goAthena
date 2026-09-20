//go:build integration

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	economyapp "github.com/bouroo/goAthena/internal/modules/economy/app"
	economyinfra "github.com/bouroo/goAthena/internal/modules/economy/infra"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	mailapp "github.com/bouroo/goAthena/internal/modules/social/mail/app"
	maildomain "github.com/bouroo/goAthena/internal/modules/social/mail/domain"
	mailinfra "github.com/bouroo/goAthena/internal/modules/social/mail/infra"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// The M11 mail (RODEX) L3 proofs. The unit suites cover the service state
// machine and the codecs; only an end-to-end run proves the wire behaviour:
// the inbox request lists mails, the read verb delivers the full message,
// the collect verbs move zeny/items, and the send verb charges the sender
// and delivers to the receiver.

// mailTestEnv is buildTestMapDeps' env plus the mail repository the test
// seeds and the economy service the mail service moves zeny through.
type mailTestEnv struct {
	mapTestEnv
	mailRepo *mailinfra.MemoryMailRepository
	econ     *economyapp.EconomyService
}

// buildMailTestEnv mirrors buildTestMapDeps but wires a MailService over an
// in-memory mail repo with both chars registered as recipients, so the
// gateway's mail verbs have a live backend. The economy service runs over
// the memory character repository (the same source EconomyService.GetZeny
// reads), so zeny movement is real.
func buildMailTestEnv(t *testing.T, sessions *charinfra.MemorySessionStore, port int) (net.Conn, mailTestEnv) {
	t.Helper()
	ms, env := buildTestMapDeps(t, sessions)
	// Seed the login session CZ_ENTER resolves against (LoginID1 must match
	// sendCZEnter's auth code below).
	if err := sessions.PutSession(context.Background(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	repo := mailinfra.NewMemoryMailRepository()
	repo.RegisterLookup(maildomain.Recipient{AccountID: 2000001, CharID: 150001, Name: "Hero", Class: 0, BaseLevel: 1})
	repo.RegisterLookup(maildomain.Recipient{AccountID: 2000002, CharID: 150002, Name: "Partner", Class: 0, BaseLevel: 1})
	econ := economyapp.NewEconomyService(env.charRepo, economyinfra.NewMemoryLedger())
	// Seed the sender with zeny (the char row must exist before the send;
	// the economy reads the char row).
	if _, err := env.charRepo.CreateWithID(context.Background(), chardomain.Character{
		ID: 150001, AccountID: 2000001, Name: "Hero", Class: 0, BaseLevel: 1, Zeny: 10000,
	}); err != nil {
		t.Fatalf("seed sender char: %v", err)
	}
	// A bag row for the staged attachment (server row 0 → client index 2).
	if _, err := env.itemRepo.Add(context.Background(), 150001, 501, 5); err != nil {
		t.Fatalf("seed sender bag: %v", err)
	}
	inv := invapp.NewInventoryService(env.itemRepo)
	ms.SetMail(mailapp.NewMailService(repo, testEconPort{svc: econ}, inv))
	ms.SetCharRepo(env.charRepo)

	conn := startAndDial(t, ms, port)
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	awaitAcceptEnter(t, conn)
	return conn, mailTestEnv{mapTestEnv: env, mailRepo: repo, econ: econ}
}

// mailInvPort adapts the inventory ItemRepository to the mail app's
// InventoryPort (the same shape the mail module's DI uses).
type mailInvPort struct {
	repo invdomain.ItemRepository
}

func (p mailInvPort) Add(ctx context.Context, charID, nameID uint32, amount int) (invdomain.Item, error) {
	return p.repo.Add(ctx, charID, nameID, amount)
}

func (p mailInvPort) Remove(ctx context.Context, id invdomain.ItemID, amount int) error {
	return p.repo.Remove(ctx, id, amount)
}

// TestMailSendDeliversEndToEnd proves the full RODEX wire loop: the sender
// opens the compose window, stages zeny + an item, sends to "Partner", and
// the send charges the sender (zeny + 2% fee + 2500 attachment price).
func TestMailSendDeliversEndToEnd(t *testing.T) {
	sessions := charinfra.NewMemorySessionStore()
	conn, env := buildMailTestEnv(t, sessions, 17901)
	defer conn.Close()

	// Each verb waits for its ack: handlers run on per-frame goroutines, so
	// the conversation is only ordered when the client round-trips (a real
	// RODEX client does exactly this).
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	// Open the compose window: 27B ack.
	sendCZOpenWriteMail(t, conn, "Partner")
	expectFrame(t, conn, ropacket.HeaderZCACKOPENWRITE, 27)

	// Stage zeny (index 0) + an item (index 2, server row 0): 64B acks.
	sendCZMailAddItem(t, conn, 0, 1000)
	expectFrame(t, conn, ropacket.HeaderZCACKADDITEMRODE, 64)
	sendCZMailAddItem(t, conn, 2, 1)
	expectFrame(t, conn, ropacket.HeaderZCACKADDITEMRODE, 64)

	// Send to "Partner": 1000 zeny + 20 fee + 2500 attachment = 3520.
	// The 3B ack's result byte must be 0.
	sendCZWriteMail(t, conn, "Partner", "hi", "body", 1000)
	body := expectFrame(t, conn, ropacket.HeaderZCACKMAILWRITE, 3)
	if body[0] != 0 {
		t.Fatalf("send ack result = %d, want 0", body[0])
	}

	// The sender's zeny drops by 3520 (10000 → 6480).
	if bal, err := env.econ.GetZeny(context.Background(), 150001); err != nil || bal != 6480 {
		t.Fatalf("sender zeny = %d (err %v), want 6480", bal, err)
	}
}

// TestMailReadDelivers proves the read verb delivers the full message and
// flips the row to READ.
func TestMailReadDelivers(t *testing.T) {
	sessions := charinfra.NewMemorySessionStore()
	conn, env := buildMailTestEnv(t, sessions, 17902)
	defer conn.Close()

	// Seed a mail directly (bypass the compose window — the send path is
	// proven by TestMailSendDeliversEndToEnd).
	mail := &maildomain.Mail{
		SenderID: 150002, SenderNam: "Partner", DestID: 150001, DestName: "Hero",
		Title: "hi", Body: "body", Time: time.Now().Unix(), Status: maildomain.StatusNew,
	}
	if err := env.mailRepo.Create(context.Background(), mail); err != nil {
		t.Fatalf("seed mail: %v", err)
	}

	// Read it.
	sendCZReadMail(t, conn, uint64(mail.ID))
	cmd, _ := readMailFrame(t, conn)
	if cmd != ropacket.HeaderZCACKREADRODEX {
		t.Fatalf("read cmd = 0x%04x, want ZC_ACK_READ_RODEX", cmd)
	}
	// The row is now READ.
	m, err := env.mailRepo.Get(context.Background(), 150001, mail.ID)
	if err != nil || m.Status != maildomain.StatusRead {
		t.Fatalf("mail status = %d (err %v), want READ", m.Status, err)
	}
}

// TestMailCollectZenyMoves proves the collect-zeny verb moves the mail's
// zeny into the receiver's balance and zeroes the column.
func TestMailCollectZenyMoves(t *testing.T) {
	sessions := charinfra.NewMemorySessionStore()
	conn, env := buildMailTestEnv(t, sessions, 17903)
	defer conn.Close()

	// Seed a mail with zeny.
	mail := &maildomain.Mail{
		SenderID: 150002, SenderNam: "Partner", DestID: 150001, DestName: "Hero",
		Title: "hi", Body: "", Time: time.Now().Unix(), Status: maildomain.StatusNew,
		Zeny: 500,
	}
	if err := env.mailRepo.Create(context.Background(), mail); err != nil {
		t.Fatalf("seed mail: %v", err)
	}

	// Collect the zeny.
	sendCZCollectZeny(t, conn, uint64(mail.ID))
	cmd, _ := readMailFrame(t, conn)
	if cmd != ropacket.HeaderZCACKZENYFROMMAI {
		t.Fatalf("collect cmd = 0x%04x, want ZC_ACK_ZENY_FROM_MAIL", cmd)
	}
	// The mail's zeny is zeroed.
	m, err := env.mailRepo.Get(context.Background(), 150001, mail.ID)
	if err != nil || m.Zeny != 0 {
		t.Fatalf("mail zeny = %d (err %v), want 0", m.Zeny, err)
	}
}

// TestMailCollectItemsMoves proves the collect-item verb moves the mail's
// attachment into the receiver's bag and clears the attachment rows.
func TestMailCollectItemsMoves(t *testing.T) {
	sessions := charinfra.NewMemorySessionStore()
	conn, env := buildMailTestEnv(t, sessions, 17904)
	defer conn.Close()

	mail := &maildomain.Mail{
		SenderID: 150002, SenderNam: "Partner", DestID: 150001, DestName: "Hero",
		Title: "hi", Body: "", Time: time.Now().Unix(), Status: maildomain.StatusNew,
		Items: []maildomain.Attachment{{MailID: 0, Index: 0, NameID: 501, Amount: 3, Identify: 1}},
	}
	if err := env.mailRepo.Create(context.Background(), mail); err != nil {
		t.Fatalf("seed mail: %v", err)
	}

	sendCZCollectItems(t, conn, uint64(mail.ID))
	cmd, _ := readMailFrame(t, conn)
	if cmd != ropacket.HeaderZCACKITEMFROMMAI {
		t.Fatalf("collect cmd = 0x%04x, want ZC_ACK_ITEM_FROM_MAIL", cmd)
	}
	// The attachment rows are gone and the item landed in the bag.
	m, err := env.mailRepo.Get(context.Background(), 150001, mail.ID)
	if err != nil || len(m.Items) != 0 {
		t.Fatalf("mail attachments after collect = %d (err %v), want 0", len(m.Items), err)
	}
	rows, err := env.itemRepo.LoadByChar(context.Background(), 2000001, 150001)
	if err != nil {
		t.Fatalf("load receiver bag: %v", err)
	}
	var landed bool
	for _, r := range rows {
		if r.NameID == 501 && r.Amount == 3 {
			landed = true
		}
	}
	if !landed {
		t.Fatalf("attachment item never landed in the bag: %+v", rows)
	}
}

// TestMailListAndDelete proves the inbox request lists the seeded mail and
// the delete verb removes an empty mail with its ack.
func TestMailListAndDelete(t *testing.T) {
	sessions := charinfra.NewMemorySessionStore()
	conn, env := buildMailTestEnv(t, sessions, 17905)
	defer conn.Close()

	mail := &maildomain.Mail{
		SenderID: 150002, SenderNam: "Partner", DestID: 150001, DestName: "Hero",
		Title: "delete me", Body: "", Time: time.Now().Unix(), Status: maildomain.StatusNew,
	}
	if err := env.mailRepo.Create(context.Background(), mail); err != nil {
		t.Fatalf("seed mail: %v", err)
	}

	// Open the inbox: the list carries the seeded row.
	sendCZOpenMailbox(t, conn)
	cmd, body := readMailFrame(t, conn)
	if cmd != ropacket.HeaderZCACKMAILLIST {
		t.Fatalf("list cmd = 0x%04x, want ZC_ACK_MAIL_LIST", cmd)
	}
	if len(body) < 41+4 || !bytes.Contains(body, []byte("delete me")) {
		t.Fatalf("list body missing the seeded mail title: %x", body)
	}

	// Delete it (empty mail — no attachments).
	sendCZDeleteMail(t, conn, uint64(mail.ID))
	cmd, _ = readMailFrame(t, conn)
	if cmd != ropacket.HeaderZCACKDELETEMAIL {
		t.Fatalf("delete cmd = 0x%04x, want ZC_ACK_DELETE_MAIL", cmd)
	}
	if _, err := env.mailRepo.Get(context.Background(), 150001, mail.ID); err == nil {
		t.Fatal("mail survived delete")
	}
}

// expectFrame reads one fixed-size frame and asserts its opcode, returning
// the bytes after the 2-byte cmd.
func expectFrame(t *testing.T, c net.Conn, cmd uint16, size int) []byte {
	t.Helper()
	frame := make([]byte, size)
	if _, err := io.ReadFull(c, frame); err != nil {
		t.Fatalf("read frame 0x%04x: %v", cmd, err)
	}
	if got := binary.LittleEndian.Uint16(frame[:2]); got != cmd {
		t.Fatalf("frame cmd = 0x%04x, want 0x%04x", got, cmd)
	}
	return frame[2:]
}

// --- wire helpers ---

// sendCZOpenWriteMail sends CZ_REQ_OPEN_WRITE_MAIL (0x0a08, 26B).
func sendCZOpenWriteMail(t *testing.T, c net.Conn, name string) {
	t.Helper()
	var buf [26]byte
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZREQOPENWRITEMAI)
	copy(buf[2:], name)
	if _, err := c.Write(buf[:]); err != nil {
		t.Fatalf("write CZ_REQ_OPEN_WRITE_MAIL: %v", err)
	}
}

// sendCZMailAddItem sends CZ_REQ_ADD_ITEM_TO_MAIL (0x0a04, 6B).
func sendCZMailAddItem(t *testing.T, c net.Conn, index, count uint16) {
	t.Helper()
	var buf [6]byte
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZREQADDITEMMAIL)
	binary.LittleEndian.PutUint16(buf[2:], index)
	binary.LittleEndian.PutUint16(buf[4:], count)
	if _, err := c.Write(buf[:]); err != nil {
		t.Fatalf("write CZ_REQ_ADD_ITEM_TO_MAIL: %v", err)
	}
}

// sendCZWriteMail sends CZ_REQ_WRITE_MAIL (0x09ec, variable): cmd + len.W +
// receiver.24B + sender.24B + zeny.Q + titleLen.W + bodyLen.W, then the title
// and body bytes. The length at offset 2 is what the gateway's variable-frame
// reader uses to bound the frame.
func sendCZWriteMail(t *testing.T, c net.Conn, receiver, title, body string, zeny uint64) {
	t.Helper()
	total := 64 + len(title) + len(body)
	var buf []byte
	buf = append(buf, 0xec, 0x09) // 0x09ec
	buf = append(buf, byte(total), byte(total>>8))
	buf = appendNameField(buf, receiver, 24)
	buf = appendNameField(buf, "", 24) // sender name (the server fills it)
	buf = append(buf, make([]byte, 8)...)
	binary.LittleEndian.PutUint64(buf[len(buf)-8:], zeny)
	buf = append(buf, make([]byte, 4)...)
	binary.LittleEndian.PutUint16(buf[len(buf)-4:], uint16(len(title)))
	binary.LittleEndian.PutUint16(buf[len(buf)-2:], uint16(len(body)))
	buf = append(buf, []byte(title)...)
	buf = append(buf, []byte(body)...)
	if _, err := c.Write(buf); err != nil {
		t.Fatalf("write CZ_REQ_WRITE_MAIL: %v", err)
	}
}

// sendCZReadMail sends CZ_REQ_READ_MAIL (0x09ea, 11B): cmd + tab.B + id.Q.
func sendCZReadMail(t *testing.T, c net.Conn, mailID uint64) {
	t.Helper()
	var buf [11]byte
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZREQREADMAIL)
	buf[2] = 0
	binary.LittleEndian.PutUint64(buf[3:], mailID)
	if _, err := c.Write(buf[:]); err != nil {
		t.Fatalf("write CZ_REQ_READ_MAIL: %v", err)
	}
}

// sendCZCollectZeny sends CZ_REQ_ZENY_FROM_MAIL (0x09f1, 11B): cmd + id.Q +
// tab.B.
func sendCZCollectZeny(t *testing.T, c net.Conn, mailID uint64) {
	t.Helper()
	var buf [11]byte
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZREQZENYFROMMAIL)
	binary.LittleEndian.PutUint64(buf[2:], mailID)
	buf[10] = 0
	if _, err := c.Write(buf[:]); err != nil {
		t.Fatalf("write CZ_REQ_ZENY_FROM_MAIL: %v", err)
	}
}

// sendCZCollectItems sends CZ_REQ_ITEM_FROM_MAIL (0x09f3, 11B).
func sendCZCollectItems(t *testing.T, c net.Conn, mailID uint64) {
	t.Helper()
	var buf [11]byte
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZREQITEMFROMMAIL)
	binary.LittleEndian.PutUint64(buf[2:], mailID)
	buf[10] = 0
	if _, err := c.Write(buf[:]); err != nil {
		t.Fatalf("write CZ_REQ_ITEM_FROM_MAIL: %v", err)
	}
}

// sendCZOpenMailbox sends CZ_OPEN_MAILBOX (0x09e8, 11B): cmd + tab.B + id.Q.
func sendCZOpenMailbox(t *testing.T, c net.Conn) {
	t.Helper()
	var buf [11]byte
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZOPENMAILBOX)
	if _, err := c.Write(buf[:]); err != nil {
		t.Fatalf("write CZ_OPEN_MAILBOX: %v", err)
	}
}

// sendCZDeleteMail sends CZ_REQ_DELETE_MAIL (0x09f5, 11B): cmd + tab.B + id.Q.
func sendCZDeleteMail(t *testing.T, c net.Conn, mailID uint64) {
	t.Helper()
	var buf [11]byte
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZREQDELETEMAIL)
	buf[2] = 0
	binary.LittleEndian.PutUint64(buf[3:], mailID)
	if _, err := c.Write(buf[:]); err != nil {
		t.Fatalf("write CZ_REQ_DELETE_MAIL: %v", err)
	}
}

// readMailFrame reads one mail reply's cmd and payload, honouring each
// frame's length discipline: the list declares its total at offset 2; the
// read ack is a fixed 24B header + body + 60B per item (the ≥20200916 sub
// struct at this packetver); the collect acks are 12B.
func readMailFrame(t *testing.T, r io.Reader) (uint16, []byte) {
	t.Helper()
	var cmd [2]byte
	if _, err := io.ReadFull(r, cmd[:]); err != nil {
		t.Fatalf("read frame cmd: %v", err)
	}
	c := binary.LittleEndian.Uint16(cmd[:])
	switch c {
	case ropacket.HeaderZCACKMAILLIST:
		var lenBuf [2]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			t.Fatalf("read ZC_ACK_MAIL_LIST len: %v", err)
		}
		body := make([]byte, int(binary.LittleEndian.Uint16(lenBuf[:]))-4)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read ZC_ACK_MAIL_LIST body: %v", err)
		}
		return c, body
	case ropacket.HeaderZCACKREADRODEX:
		// Fixed 24B header (the body length is at offset 13, the item count
		// at offset 23) — read the header, then the body, then the items.
		header := make([]byte, 22)
		if _, err := io.ReadFull(r, header); err != nil {
			t.Fatalf("read ZC_ACK_READ_RODEX header: %v", err)
		}
		bodyLen := int(binary.LittleEndian.Uint16(header[11:13]))
		itemCount := int(header[21])
		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read ZC_ACK_READ_RODEX body: %v", err)
		}
		items := make([]byte, itemCount*60)
		if _, err := io.ReadFull(r, items); err != nil {
			t.Fatalf("read ZC_ACK_READ_RODEX items: %v", err)
		}
		return c, append(header, append(body, items...)...)
	case ropacket.HeaderZCACKZENYFROMMAI, ropacket.HeaderZCACKITEMFROMMAI:
		body := make([]byte, 10)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read mail collect ack body: %v", err)
		}
		return c, body
	case ropacket.HeaderZCACKDELETEMAIL:
		body := make([]byte, 9)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read ZC_ACK_DELETE_MAIL body: %v", err)
		}
		return c, body
	default:
		t.Fatalf("unexpected frame cmd 0x%04x", c)
		return 0, nil
	}
}

// appendNameField appends a fixed-width name field.
func appendNameField(buf []byte, name string, width int) []byte {
	field := make([]byte, width)
	copy(field, name)
	return append(buf, field...)
}
