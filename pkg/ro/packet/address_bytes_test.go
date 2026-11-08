//go:build unit

package packet_test

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// The address fields of the three packets that carry a server address are
// the one place where a wrong byte order is invisible: the packet still
// parses, the client still connects, and it connects to the wrong host.
//
// rAthena writes them like this (the Thai fork, the authority):
//
//	HC_NOTIFY_ZONESVR    char_clif.cpp:909   p.ip = htonl(ip);
//	                                     :910   p.port = ntows(htons(port));
//	AC_ACCEPT_LOGIN_sub  loginclif.cpp:137   char_server.ip = htonl(ip);
//	                                     :138   char_server.port = ntows(htons(port));
//	ZC_NPCACK_SERVERMOVE clif.cpp:2178       packet.ip = htonl(ip);
//	                                     :2179       packet.port = ntows(htons(port));
//
// The structs are packed and socket_send memcpy's them, so the wire bytes
// are the host (little-endian) memory image of those field values.
//
// Compiled from the source's own structs:
//
//	HC_NOTIFY_ZONESVR    ip@22 = 01 00 00 7f   port@26 = 01 14
//	AC_ACCEPT_LOGIN_sub  ip@0  = 01 00 00 7f   port@4  = e9 17  (6121)
//	ZC_NPCACK_SERVERMOVE ip@30 = 01 00 00 7f   port@34 = 01 14
//
// The test pins those bytes. Before the fix the encoders wrote 7f 00 00 01
// for the IP — the octets reversed — and the client dialed 1.0.0.127.
func TestAddressBytesMatchTheSource(t *testing.T) {
	t.Parallel()

	// htonl of the host-order 127.0.0.1 — the value the source's struct
	// holds.
	const ipField = 0x7f000001

	var zone bytes.Buffer
	if err := (packet.NotifyZoneServerResponse{
		CID: 1, MapName: "prontera", IP: ipField, Port: 5121,
	}).Encode(&zone); err != nil {
		t.Fatalf("encode HC_NOTIFY_ZONESVR: %v", err)
	}
	got := zone.Bytes()
	if !bytes.Equal(got[22:26], []byte{0x01, 0x00, 0x00, 0x7f}) {
		t.Errorf("HC_NOTIFY_ZONESVR ip bytes = % x, want 01 00 00 7f", got[22:26])
	}
	if !bytes.Equal(got[26:28], []byte{0x01, 0x14}) {
		t.Errorf("HC_NOTIFY_ZONESVR port bytes = % x, want 01 14", got[26:28])
	}

	var move bytes.Buffer
	if err := (packet.ServerMoveResponse{
		MapName: "geffen", IP: ipField, Port: 5121,
	}).Encode(&move); err != nil {
		t.Fatalf("encode ZC_NPCACK_SERVERMOVE: %v", err)
	}
	m := move.Bytes()
	if !bytes.Equal(m[30:34], []byte{0x01, 0x00, 0x00, 0x7f}) {
		t.Errorf("ZC_NPCACK_SERVERMOVE ip bytes = % x, want 01 00 00 7f", m[30:34])
	}
	if !bytes.Equal(m[34:36], []byte{0x01, 0x14}) {
		t.Errorf("ZC_NPCACK_SERVERMOVE port bytes = % x, want 01 14", m[34:36])
	}

	var accept bytes.Buffer
	if err := (packet.AcceptLoginResponse{
		LoginID1: 1, AID: 2, LoginID2: 3,
		CharServers: []packet.CharServer{{IP: ipField, Port: 6121, Name: "c"}},
	}).Encode(&accept); err != nil {
		t.Fatalf("encode AC_ACCEPT_LOGIN: %v", err)
	}
	a := accept.Bytes()
	if !bytes.Equal(a[64:68], []byte{0x01, 0x00, 0x00, 0x7f}) {
		t.Errorf("AC_ACCEPT_LOGIN char server ip bytes = % x, want 01 00 00 7f", a[64:68])
	}
	if !bytes.Equal(a[68:70], []byte{0xe9, 0x17}) {
		t.Errorf("AC_ACCEPT_LOGIN char server port bytes = % x, want e9 17", a[68:70])
	}
}

// TestAddressBytesDecodeToTheSameAddress proves the wire bytes decode
// back to the address the operator configured.
//
// The client reads the 4 address bytes as a little-endian uint32 and then
// extracts the octets most-significant-first — the counterpart of the
// source's own CONVIP macro (socket.hpp:176). The bytes the fixed encoder
// writes therefore have to decode to 127.0.0.1, and the bytes the old
// one wrote decoded to 1.0.0.127: a routable address that is not this
// host, which is why the bug survived a health check and a login.
func TestAddressBytesDecodeToTheSameAddress(t *testing.T) {
	t.Parallel()

	const configured = "127.0.0.1"
	ipField := binary.BigEndian.Uint32(net.ParseIP(configured).To4())

	var accept bytes.Buffer
	if err := (packet.AcceptLoginResponse{
		LoginID1: 1, AID: 2, LoginID2: 3,
		CharServers: []packet.CharServer{{IP: ipField, Port: 6121, Name: "c"}},
	}).Encode(&accept); err != nil {
		t.Fatalf("encode AC_ACCEPT_LOGIN: %v", err)
	}
	a := accept.Bytes()

	// The client's read: little-endian uint32, then CONVIP's octets.
	field := binary.LittleEndian.Uint32(a[64:68])
	decoded := net.IPv4(byte(field>>24), byte(field>>16), byte(field>>8), byte(field)).String()
	if decoded != configured {
		t.Errorf("decoded char server address = %s, want %s", decoded, configured)
	}

	// The port is the plain number in the source's field value, so the
	// little-endian image decodes back to it.
	if port := binary.LittleEndian.Uint16(a[68:70]); port != 6121 {
		t.Errorf("decoded char server port = %d, want 6121", port)
	}
}
