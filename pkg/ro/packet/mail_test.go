package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Mail (RODEX) wire tests: byte-exact S→C encodes and C→S round-trips against
// the rAthena struct layouts (packets_struct.hpp PACKET_*MAIL*/maillistinfo,
// builders clif.cpp:15869-16900).

func u16(b []byte, off int) uint16 { return binary.LittleEndian.Uint16(b[off : off+2]) }
func u32(b []byte, off int) uint32 { return binary.LittleEndian.Uint32(b[off : off+4]) }
func u64(b []byte, off int) uint64 { return binary.LittleEndian.Uint64(b[off : off+8]) }

func TestParseCZMailIDFrames(t *testing.T) {
	// CZ_REQ_READ_MAIL 0x09ea: tab.B then mail id.Q.
	var buf bytes.Buffer
	if err := (CZMailReq{Op: HeaderCZREQREADMAIL, MailID: 77}).Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := buf.Len(); got != 11 {
		t.Fatalf("CZ_REQ_READ_MAIL size = %d, want 11", got)
	}
	got, err := ParseCZReadMail(buf.Bytes())
	if err != nil || got.MailID != 77 || got.Op != HeaderCZREQREADMAIL {
		t.Fatalf("parsed = %+v, err %v", got, err)
	}
	// CZ_OPEN_MAILBOX2 0x0ac0: id.Q at 2, 16 unknown bytes tail (the
	// ≥20170419 handler reads RFIFOQ(fd, 2) directly).
	var b2 bytes.Buffer
	if err := (CZMailReq{Op: HeaderCZOPENMAILBOX2, MailID: 88}).Encode(&b2); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := b2.Len(); got != 26 {
		t.Fatalf("CZ_OPEN_MAILBOX2 size = %d, want 26", got)
	}
	got2, err := ParseCZOpenMailbox2(b2.Bytes())
	if err != nil || got2.MailID != 88 {
		t.Fatalf("parsed = %+v, err %v", got2, err)
	}
	// CZ_REQ_ZENY_FROM_MAIL 0x09f1: id.Q at 2, tab.B at 10.
	f1 := make([]byte, 11)
	binary.LittleEndian.PutUint16(f1[0:], HeaderCZREQZENYFROMMAIL)
	binary.LittleEndian.PutUint64(f1[2:], 99)
	f1[10] = MailInboxNormal
	got3, err := ParseCZGetAttach(f1)
	if err != nil || got3.MailID != 99 {
		t.Fatalf("parsed = %+v, err %v", got3, err)
	}
	if _, err := ParseCZReadMail([]byte{0xea, 0x09, 0}); err == nil {
		t.Fatal("short frame accepted")
	}
}

func TestParseCZMailNameFrames(t *testing.T) {
	var buf bytes.Buffer
	if err := (CZOpenWriteMail{Op: HeaderCZREQOPENWRITEMAI, Name: "Partner"}).Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := buf.Len(); got != 26 {
		t.Fatalf("CZ_REQ_OPEN_WRITE_MAIL size = %d, want 26", got)
	}
	got, err := ParseCZOpenWriteMail(buf.Bytes())
	if err != nil || got.Name != "Partner" {
		t.Fatalf("parsed = %+v, err %v", got, err)
	}
	// 0x0b97 adds one flag byte.
	f := make([]byte, 27)
	binary.LittleEndian.PutUint16(f[0:], HeaderCZCHECKNAME2)
	copy(f[2:], "Hero")
	f[26] = 1
	got2, err := ParseCZCheckReceiverName(f)
	if err != nil || got2.Name != "Hero" || got2.Op != HeaderCZCHECKNAME2 {
		t.Fatalf("parsed = %+v, err %v", got2, err)
	}
}

func TestParseCZMailAttachItem(t *testing.T) {
	var buf bytes.Buffer
	if err := (CZMailAttachItem{Op: HeaderCZREQADDITEMMAIL, Index: 5, Count: 3}).Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := ParseCZMailAddItem(buf.Bytes())
	if err != nil || got.Index != 5 || got.Count != 3 {
		t.Fatalf("parsed = %+v, err %v", got, err)
	}
	if _, err := ParseCZMailRemoveItem(buf.Bytes()); err == nil {
		t.Fatal("add frame accepted as remove")
	}
}

func TestParseCZWriteMail2(t *testing.T) {
	// 0x0a6e: strings start after the 4-byte receiver char id.
	title, body := "Hello there", "Body text\nline two"
	frame := make([]byte, sizeCZWriteMail2Header+len(title)+1+len(body))
	binary.LittleEndian.PutUint16(frame[0:], HeaderCZREQWRITEMAIL2)
	binary.LittleEndian.PutUint16(frame[2:], uint16(len(frame))) //nolint:gosec // test frame bounded.
	copy(frame[4:], "Partner\x00")
	copy(frame[28:], "Hero\x00")
	binary.LittleEndian.PutUint64(frame[52:], 12345)
	binary.LittleEndian.PutUint16(frame[60:], uint16(len(title)+1)) //nolint:gosec
	binary.LittleEndian.PutUint16(frame[62:], uint16(len(body)))    //nolint:gosec
	binary.LittleEndian.PutUint32(frame[64:], 150002)
	copy(frame[68:], title)
	frame[68+len(title)] = 0
	copy(frame[68+len(title)+1:], body)

	got, err := ParseCZWriteMail(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.ReceiverName != "Partner" || got.SenderName != "Hero" || got.Zeny != 12345 ||
		got.ReceiverCharID != 150002 || got.Title != title || got.Body != body {
		t.Fatalf("parsed = %+v", got)
	}
	// Too-short frames (rAthena's length < 0x3e guard) are rejected.
	if _, err := ParseCZWriteMail(frame[:40]); err == nil {
		t.Fatal("short frame accepted")
	}
	// 0x09ec has no char-id slot.
	f9 := make([]byte, sizeCZWriteMailHeader+len(title)+1)
	binary.LittleEndian.PutUint16(f9[0:], HeaderCZREQWRITEMAIL)
	binary.LittleEndian.PutUint16(f9[60:], uint16(len(title)+1)) //nolint:gosec
	copy(f9[64:], title)
	got9, err := ParseCZWriteMail(f9)
	if err != nil || got9.Title != title || got9.Body != "" {
		t.Fatalf("parsed = %+v, err %v", got9, err)
	}
}

func TestZCNotifyUnreadMail(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeZCNotifyUnreadMail(&buf, true); err != nil {
		t.Fatalf("encode: %v", err)
	}
	b := buf.Bytes()
	if len(b) != 3 || u16(b, 0) != HeaderZCNOTIFYUNREADMA || b[2] != 1 {
		t.Fatalf("bytes = %x", b)
	}
}

func TestZCAckMailList(t *testing.T) {
	entries := []MailListEntry{
		{Type: MailInboxNormal, MailID: 7, Flags: MailTypeZeny | MailTypeItem, Sender: "Alice", Expires: 365 * 24 * 60 * 60, Title: "Hi"},
		{Type: MailInboxNormal, MailID: 8, Read: true, Sender: "Bob", Expires: 60, Title: "RE:Hi"},
	}
	var buf bytes.Buffer
	if err := EncodeZCAckMailList(&buf, entries); err != nil {
		t.Fatalf("encode: %v", err)
	}
	b := buf.Bytes()
	want := 5 + (41 + 3) + (41 + 6) // header + "Hi" entry + "RE:Hi" entry
	if len(b) != want {
		t.Fatalf("size = %d, want %d", len(b), want)
	}
	if u16(b, 0) != HeaderZCACKMAILLIST || u16(b, 2) != uint16(want) || b[4] != 1 {
		t.Fatalf("header = %x", b[:5])
	}
	e := b[5:]
	if e[0] != MailInboxNormal || u64(e, 1) != 7 || e[9] != 0 || e[10] != MailTypeZeny|MailTypeItem {
		t.Fatalf("entry 1 head = %x", e[:11])
	}
	if readNameField(e, 11, 24) != "Alice" || u32(e, 35) != 365*24*60*60 || u16(e, 39) != 3 || readNameField(e, 41, 3) != "Hi" {
		t.Fatalf("entry 1 tail mismatch")
	}
	e2 := b[5+44:]
	if u64(e2, 1) != 8 || e2[9] != 1 || readNameField(e2, 11, 24) != "Bob" {
		t.Fatalf("entry 2 mismatch")
	}
}

func TestZCAckReadRodex(t *testing.T) {
	items := []MailReadItem{{
		Count: 3, ITID: 501, Type: 0, Identified: true,
		Card: [4]uint32{4001, 0, 0, 0}, Location: 136, View: 0, Bind: 0, Refine: 7,
	}}
	var buf bytes.Buffer
	if err := EncodeZCAckReadRodex(&buf, MailInboxNormal, 42, 500, "yo", items); err != nil {
		t.Fatalf("encode: %v", err)
	}
	b := buf.Bytes()
	want := 24 + 3 + 60
	if len(b) != want {
		t.Fatalf("size = %d, want %d", len(b), want)
	}
	if u16(b, 0) != HeaderZCACKREADRODEX || u16(b, 2) != uint16(want) || b[4] != MailInboxNormal ||
		u64(b, 5) != 42 || u16(b, 13) != 3 || u64(b, 15) != 500 || b[23] != 1 {
		t.Fatalf("header = %x", b[:24])
	}
	if string(b[24:26]) != "yo" || b[26] != 0 {
		t.Fatalf("body = %x", b[24:27])
	}
	s := b[27:]
	if u16(s, 0) != 3 || u32(s, 2) != 501 || s[6] != 1 || u32(s, 8) != 4001 ||
		u32(s, 24) != 136 || u16(s, 29) != 0 || s[58] != 7 || s[59] != 0 {
		t.Fatalf("sub = %x", s)
	}
	// Empty body gets the rAthena "(no message)" substitute.
	var empty bytes.Buffer
	if err := EncodeZCAckReadRodex(&empty, 0, 1, 0, "", nil); err != nil {
		t.Fatalf("encode empty: %v", err)
	}
	eb := empty.Bytes()
	if u16(eb, 13) != uint16(len("(no message)")+1) || string(eb[24:24+12]) != "(no message)" {
		t.Fatalf("empty body frame = %x", eb)
	}
}

func TestZCAckAddItemRodex(t *testing.T) {
	var ok bytes.Buffer
	if err := EncodeZCAckAddItemRodex(&ok, MailAddOK, MailItem{
		Index: 5, Count: 2, ITID: 1201, Type: 9, Identified: true, Card: [4]uint32{0, 0, 0, 0},
		Location: 2, Weight: 100, Refine: 0, Favorite: false,
	}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	b := ok.Bytes()
	if len(b) != 64 || b[2] != 0 || u16(b, 3) != 5 || u16(b, 5) != 2 || u32(b, 7) != 1201 ||
		b[11] != 9 || b[12] != 1 || u16(b, 55) != 100 || u32(b, 58) != 2 {
		t.Fatalf("bytes = %x", b)
	}
	var fail bytes.Buffer
	if err := EncodeZCAckAddItemRodex(&fail, MailAddWeight, MailItem{}); err != nil {
		t.Fatalf("encode fail: %v", err)
	}
	fb := fail.Bytes()
	if len(fb) != 64 || fb[2] != 1 || u32(fb, 7) != 0 {
		t.Fatalf("failure bytes = %x", fb)
	}
}

func TestZCSmallAcks(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeZCAckWriteMail(&buf, MailSendSuccess); err != nil {
		t.Fatalf("encode write ack: %v", err)
	}
	if b := buf.Bytes(); len(b) != 3 || u16(b, 0) != HeaderZCACKMAILWRITE || b[2] != 0 {
		t.Fatalf("write ack = %x", b)
	}

	buf.Reset()
	if err := EncodeZCAckOpenWriteMail(&buf, "Partner", true); err != nil {
		t.Fatalf("encode open write: %v", err)
	}
	if b := buf.Bytes(); len(b) != 27 || readNameField(b, 2, 24) != "Partner" || b[26] != 1 {
		t.Fatalf("open write = %x", b)
	}

	buf.Reset()
	if err := EncodeZCCheckName(&buf, 150002, 6, 99, "Partner"); err != nil {
		t.Fatalf("encode checkname: %v", err)
	}
	if b := buf.Bytes(); len(b) != 34 || u32(b, 2) != 150002 || u16(b, 6) != 6 || u16(b, 8) != 99 || readNameField(b, 10, 24) != "Partner" {
		t.Fatalf("checkname = %x", b)
	}

	buf.Reset()
	if err := EncodeZCAckRemoveItemMail(&buf, true, 5, 2, 90); err != nil {
		t.Fatalf("encode remove: %v", err)
	}
	if b := buf.Bytes(); len(b) != 9 || b[2] != 1 || u16(b, 3) != 5 || u16(b, 5) != 2 || u16(b, 7) != 90 {
		t.Fatalf("remove = %x", b)
	}

	buf.Reset()
	if err := EncodeZCAckZenyFromMail(&buf, 42, MailInboxNormal, MailAttachOK); err != nil {
		t.Fatalf("encode zeny ack: %v", err)
	}
	if b := buf.Bytes(); len(b) != 12 || u64(b, 2) != 42 || b[10] != 0 || b[11] != 0 {
		t.Fatalf("zeny ack = %x", b)
	}

	buf.Reset()
	if err := EncodeZCAckItemFromMail(&buf, 43, MailInboxNormal, MailAttachOK); err != nil {
		t.Fatalf("encode item ack: %v", err)
	}
	if b := buf.Bytes(); len(b) != 12 || u16(b, 0) != HeaderZCACKITEMFROMMAI || u64(b, 2) != 43 {
		t.Fatalf("item ack = %x", b)
	}

	buf.Reset()
	if err := EncodeZCAckDeleteMail(&buf, MailInboxNormal, 44); err != nil {
		t.Fatalf("encode delete: %v", err)
	}
	if b := buf.Bytes(); len(b) != 11 || u16(b, 0) != HeaderZCACKDELETEMAIL || b[2] != 0 || u64(b, 3) != 44 {
		t.Fatalf("delete = %x", b)
	}
}
