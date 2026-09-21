package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Guild wire tests: round-trip C→S parses and byte-exact S→C encodes against
// the rAthena struct layouts the codecs mirror.

func TestParseCZCreateGuild(t *testing.T) {
	var buf bytes.Buffer
	if err := (CZCreateGuild{CharID: 150001, Name: "Emperium Crew"}).Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := buf.Len(); got != 30 {
		t.Fatalf("CZ_REQ_MAKE_GUILD size = %d, want 30", got)
	}
	got, err := ParseCZCreateGuild(buf.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.CharID != 150001 || got.Name != "Emperium Crew" {
		t.Fatalf("parsed = %+v", got)
	}
	if _, err := ParseCZCreateGuild([]byte{0x65, 0x01}); err == nil {
		t.Fatal("short frame accepted")
	}
}

func TestParseCZGuildLeaveRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCZGuildLeave(&buf, 42, 2000001, 150001, "goodbye"); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := buf.Len(); got != 52 {
		t.Fatalf("CZ_REQ_LEAVE_GUILD size = %d, want 52", got)
	}
	got, err := ParseCZGuildLeave(buf.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.GuildID != 42 || got.AID != 2000001 || got.CID != 150001 || got.Message != "goodbye" {
		t.Fatalf("parsed = %+v", got)
	}
	// Ban shares the shape, different cmd.
	var ban bytes.Buffer
	if err := EncodeCZGuildBan(&ban, 42, 2000001, 150001, "inactive"); err != nil {
		t.Fatalf("encode ban: %v", err)
	}
	parsed, err := ParseCZGuildBan(ban.Bytes())
	if err != nil {
		t.Fatalf("parse ban: %v", err)
	}
	if parsed.Message != "inactive" {
		t.Fatalf("ban parsed = %+v", parsed)
	}
	if _, err := ParseCZGuildBan(buf.Bytes()); err == nil {
		t.Fatal("leave frame accepted as ban")
	}
}

func TestParseCZGuildBreakRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCZGuildBreak(&buf, "Emperium Crew"); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := buf.Len(); got != 44 {
		t.Fatalf("CZ_REQ_DISORGANIZE_GUILD size = %d, want 44", got)
	}
	got, err := ParseCZGuildBreak(buf.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Key != "Emperium Crew" {
		t.Fatalf("parsed = %+v", got)
	}
}

func TestParseCZJoinGuildRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCZJoinGuild(&buf, 7, GuildJoinAccept); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := buf.Len(); got != 10 {
		t.Fatalf("CZ_JOIN_GUILD size = %d, want 10", got)
	}
	got, err := ParseCZJoinGuild(buf.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.GuildID != 7 || got.Answer != GuildJoinAccept {
		t.Fatalf("parsed = %+v", got)
	}
}

func TestParseCZGuildChat(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCZGuildChat(&buf, "hi guild"); err != nil {
		t.Fatalf("encode: %v", err)
	}
	msg, err := ParseCZGuildChat(buf.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg != "hi guild" {
		t.Fatalf("msg = %q", msg)
	}
	if _, err := ParseCZGuildChat([]byte{0x7e}); err == nil {
		t.Fatal("short frame accepted")
	}
}

func TestGuildResponseEncoders(t *testing.T) {
	t.Run("ResultMakeGuild", func(t *testing.T) {
		var buf bytes.Buffer
		if err := (ResultMakeGuildResponse{Result: GuildCreateOK}).Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 3 || binary.LittleEndian.Uint16(b) != HeaderZCREQUESTMAKEGILD || b[2] != 0 {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("ReqJoinGuild", func(t *testing.T) {
		resp := ReqJoinGuildResponse{GuildID: 9, GuildName: "Crew"}
		var buf bytes.Buffer
		if err := resp.Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 30 || binary.LittleEndian.Uint32(b[2:6]) != 9 || readNameField(b, 6, 24) != "Crew" {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("AckReqJoinGuild", func(t *testing.T) {
		var buf bytes.Buffer
		if err := (AckReqJoinGuildResponse{Result: GuildInviteAccepted}).Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 3 || b[2] != 2 {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("UpdateGDID", func(t *testing.T) {
		resp := UpdateGDIDResponse{GuildID: 5, Mode: 0x11, IsMaster: true, GuildName: "Crew", MasterGID: 150001}
		var buf bytes.Buffer
		if err := resp.Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 47 || binary.LittleEndian.Uint32(b[2:6]) != 5 || b[14] != 1 || readNameField(b, 19, 24) != "Crew" || binary.LittleEndian.Uint32(b[43:47]) != 150001 {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("UpdateCharStat", func(t *testing.T) {
		var buf bytes.Buffer
		if err := (UpdateCharStatResponse{AID: 1, CID: 2, Status: 1}).Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 14 || binary.LittleEndian.Uint32(b[10:14]) != 1 {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("GuildInfo", func(t *testing.T) {
		var buf bytes.Buffer
		resp := GuildInfoResponse{GuildID: 7, Level: 3, UserNum: 2, MaxUserNum: 16, GuildName: "Athena", Zeny: 9, MasterGID: 150001, MasterName: "Hero"}
		if err := resp.Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 118 || binary.LittleEndian.Uint32(b[10:14]) != 2 || readNameField(b, 46, 24) != "Athena" || binary.LittleEndian.Uint32(b[86:90]) != 9 || binary.LittleEndian.Uint32(b[90:94]) != 150001 || readNameField(b, 94, 24) != "Hero" {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("MemberMgrInfo", func(t *testing.T) {
		var buf bytes.Buffer
		resp := MemberMgrInfoResponse{Members: []GuildMemberInfo{
			{AID: 2000001, GID: 150001, Job: 6, Level: 99, State: 1, Position: 0, CharName: "Hero"},
			{AID: 2000002, GID: 150002, Position: 1, CharName: "Partner"},
		}}
		if err := resp.Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 4+58*2 || int(binary.LittleEndian.Uint16(b[2:4])) != len(b) {
			t.Fatalf("bytes = %x", b)
		}
		m1, m2 := b[4:], b[4+58:]
		if binary.LittleEndian.Uint32(m1[0:4]) != 2000001 || binary.LittleEndian.Uint32(m1[4:8]) != 150001 || readNameField(m1[34:58], 0, 24) != "Hero" {
			t.Fatalf("member 1 = %x", m1)
		}
		if binary.LittleEndian.Uint32(m2[4:8]) != 150002 || readNameField(m2[34:58], 0, 24) != "Partner" || binary.LittleEndian.Uint32(m2[22:26]) != 0 {
			t.Fatalf("member 2 = %x", m2)
		}
	})
	t.Run("AckLeaveGuild", func(t *testing.T) {
		var buf bytes.Buffer
		if err := (AckLeaveGuildResponse{GID: 3, Reason: "bye"}).Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 46 || binary.LittleEndian.Uint32(b[2:6]) != 3 || readNameField(b, 6, 40) != "bye" {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("AckBanGuild", func(t *testing.T) {
		var buf bytes.Buffer
		if err := (AckBanGuildResponse{GID: 4, Reason: "afk"}).Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 46 || readNameField(b, 2, 40) != "afk" || binary.LittleEndian.Uint32(b[42:46]) != 4 {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("AckDisorganizeGuild", func(t *testing.T) {
		var buf bytes.Buffer
		if err := (AckDisorganizeGuildResponse{Result: GuildBrokenOK}).Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		if len(b) != 6 || binary.LittleEndian.Uint32(b[2:6]) != 0 {
			t.Fatalf("bytes = %x", b)
		}
	})
	t.Run("GuildChat", func(t *testing.T) {
		resp := GuildChatResponse{Message: "Hello"}
		var buf bytes.Buffer
		if err := resp.Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b := buf.Bytes()
		// 4-byte header + message + NUL terminator (rAthena len+1).
		if len(b) != 10 || binary.LittleEndian.Uint16(b[2:4]) != 10 || string(b[4:9]) != "Hello" || b[9] != 0 {
			t.Fatalf("bytes = %x", b)
		}
	})
}
