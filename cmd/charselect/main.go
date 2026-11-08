// Command charselect drives the full client handshake against a running
// goathena node: login, char-list, char-select — and reports the map
// address the server hands the client.
//
// It exists because the per-map address handoff only happens at
// char-select: the login port proves the node is up, but the address the
// client is given for its character's map is decided by the zone
// directory, and that is what a deployment's route table has to get
// right.
//
// Usage:
//
//	charselect -host 127.0.0.1 -port 6900 -user loadgen -pass loadgen
//
// The map server's address is printed as "map=<ipv4>:<port>" so a shell
// can grep for it.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"time"

	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/protocol"
)

func main() {
	host := flag.String("host", "127.0.0.1", "login host")
	port := flag.Int("port", 6900, "login port")
	user := flag.String("user", "loadgen", "account name")
	pass := flag.String("pass", "loadgen", "account password")
	slot := flag.Uint("slot", 0, "character slot to select")
	charAddr := flag.String("char-addr", "", "dial this char-server address instead of the advertised one (the advertised address is the deployment's, and may not be routable from here)")
	flag.Parse()

	// The opcode and wire-length tables come from the installed source
	// pack and client family, exactly as the server resolves them.
	protocol.Install(protocol.SourceThailand, protocol.VariantRE)
	if err := run(*host, *port, *user, *pass, uint8(*slot), *charAddr); err != nil { //nolint:gosec // G115: a slot index is 0..N.
		fmt.Fprintf(os.Stderr, "charselect: %v\n", err)
		os.Exit(1)
	}
}

func run(host string, port int, user, pass string, slot byte, charAddr string) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial login server: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	// 1. CA_LOGIN (0x0064): version, user[24], pass[24], client type.
	login := make([]byte, 55) // cmd(2) + version(4) + user(24) + pass(24) + type(1)
	binary.LittleEndian.PutUint16(login[0:], ropacket.HeaderCALOGIN())
	binary.LittleEndian.PutUint32(login[2:], 0x00000001) // version
	copy(login[6:30], user)
	copy(login[30:54], pass)
	login[54] = 0 // client type
	if _, err := conn.Write(login); err != nil {
		return fmt.Errorf("write CA_LOGIN: %w", err)
	}

	// 2. AC_ACCEPT_LOGIN (0x0ac4) carries the char-server address.
	accept, err := readFrame(conn)
	if err != nil {
		return fmt.Errorf("read accept-login: %w", err)
	}
	if cmd := binary.LittleEndian.Uint16(accept); cmd != ropacket.HeaderACACCEPTLOGIN() {
		return fmt.Errorf("server answered 0x%04x, want AC_ACCEPT_LOGIN", cmd)
	}
	charHost, charPort, err := charServerAddress(accept)
	if err != nil {
		return err
	}
	fmt.Printf("char=%s:%d\n", charHost, charPort)

	// 3. CH_ENTER + CH_SELECT_CHAR on the char server.
	host4, port4, err := charSelect(accept, charHost, charPort, charAddr, slot)
	if err != nil {
		return err
	}
	fmt.Printf("map=%s:%d\n", host4, port4)
	return nil
}

// charSelect runs the char-server half of the handshake: CH_ENTER, then
// CH_SELECT_CHAR, and returns the map address the server hands the client.
//
// The advertised char-server address is the deployment's — the address
// the real client dials — and it may not be routable from the machine
// running this harness. The override dials a different one while the
// advertised one is still what gets reported.
func charSelect(accept []byte, charHost string, charPort int, charAddr string, slot byte) (string, int, error) {
	dialAddr := net.JoinHostPort(charHost, strconv.Itoa(charPort))
	if charAddr != "" {
		dialAddr = charAddr
	}
	ch, err := net.DialTimeout("tcp", dialAddr, 5*time.Second)
	if err != nil {
		return "", 0, fmt.Errorf("dial char server: %w", err)
	}
	defer ch.Close()
	_ = ch.SetDeadline(time.Now().Add(15 * time.Second))

	loginID1, loginID2, sex, err := sessionTokens(accept)
	if err != nil {
		return "", 0, err
	}
	enter := make([]byte, 17)
	binary.LittleEndian.PutUint16(enter[0:], ropacket.HeaderCHENTER())
	binary.LittleEndian.PutUint32(enter[2:], accountID(accept))
	binary.LittleEndian.PutUint32(enter[6:], loginID1)
	binary.LittleEndian.PutUint32(enter[10:], loginID2)
	enter[14] = sex
	if _, err := ch.Write(enter); err != nil {
		return "", 0, fmt.Errorf("write CH_ENTER: %w", err)
	}
	enterReply, err := readFrame(ch)
	if err != nil {
		return "", 0, fmt.Errorf("read char accept-enter: %w", err)
	}
	if cmd := binary.LittleEndian.Uint16(enterReply); cmd != ropacket.HeaderHCACCEPTENTER() {
		return "", 0, fmt.Errorf("char server answered 0x%04x, want HC_ACCEPT_ENTER", cmd)
	}

	// CH_SELECT_CHAR (0x0066): the server answers with the map address
	// for the character's map.
	selectFrame := make([]byte, 3)
	binary.LittleEndian.PutUint16(selectFrame[0:], ropacket.HeaderCHSELECTCHAR())
	selectFrame[2] = slot
	if _, err := ch.Write(selectFrame); err != nil {
		return "", 0, fmt.Errorf("write CH_SELECT_CHAR: %w", err)
	}
	zone, err := readFrame(ch)
	if err != nil {
		return "", 0, fmt.Errorf("read notify-zone: %w", err)
	}
	if cmd := binary.LittleEndian.Uint16(zone); cmd != ropacket.HeaderHCNOTIFYZONESVR() {
		return "", 0, fmt.Errorf("char server answered 0x%04x, want HC_NOTIFY_ZONESVR", cmd)
	}
	return zoneAddress(zone)
}

// readFrame reads one frame: two bytes of command, then the frame's
// length. AC_ACCEPT_LOGIN carries its total length at offset 2; the
// fixed-size replies are read by their known size.
func readFrame(conn net.Conn) ([]byte, error) {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return nil, fmt.Errorf("read frame header: %w", err)
	}
	cmd := binary.LittleEndian.Uint16(hdr)
	switch cmd {
	case ropacket.HeaderACACCEPTLOGIN():
		lenBuf := make([]byte, 2)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return nil, fmt.Errorf("read frame length: %w", err)
		}
		total := int(binary.LittleEndian.Uint16(lenBuf))
		if total < 4 {
			return nil, fmt.Errorf("accept-login length %d is too small", total)
		}
		rest := make([]byte, total-4)
		if _, err := io.ReadFull(conn, rest); err != nil {
			return nil, fmt.Errorf("read frame body: %w", err)
		}
		frame := make([]byte, total)
		copy(frame[0:], hdr)
		copy(frame[2:], lenBuf)
		copy(frame[4:], rest)
		return frame, nil
	case ropacket.HeaderHCACCEPTENTER():
		// The frame carries its total length at offset 2.
		lenBuf := make([]byte, 2)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return nil, fmt.Errorf("read frame length: %w", err)
		}
		total := int(binary.LittleEndian.Uint16(lenBuf))
		if total < 4 {
			return nil, fmt.Errorf("accept-enter length %d is too small", total)
		}
		rest := make([]byte, total-4)
		if _, err := io.ReadFull(conn, rest); err != nil {
			return nil, fmt.Errorf("read frame body: %w", err)
		}
		frame := make([]byte, total)
		copy(frame[0:], hdr)
		copy(frame[2:], lenBuf)
		copy(frame[4:], rest)
		return frame, nil
	case ropacket.HeaderHCNOTIFYZONESVR():
		rest := make([]byte, 156-2)
		if _, err := io.ReadFull(conn, rest); err != nil {
			return nil, fmt.Errorf("read frame body: %w", err)
		}
		frame := make([]byte, 156)
		copy(frame, hdr)
		copy(frame[2:], rest)
		return frame, nil
	default:
		return nil, fmt.Errorf("unexpected command 0x%04x", cmd)
	}
}

// charServerAddress reads the char-server address from the accept-login
// frame: the first char_servers[] entry, at offset 64.
func charServerAddress(accept []byte) (string, int, error) {
	if len(accept) < 70 {
		return "", 0, fmt.Errorf("accept-login frame is %d bytes, too short for a char server", len(accept))
	}
	host := decodeAddress(accept[64:68])
	port := int(binary.LittleEndian.Uint16(accept[68:70]))
	return host, port, nil
}

// zoneAddress reads the map-server address from the notify-zone frame:
// the IP at offset 22 and the port at offset 26.
func zoneAddress(zone []byte) (string, int, error) {
	if len(zone) < 28 {
		return "", 0, fmt.Errorf("notify-zone frame is %d bytes, too short", len(zone))
	}
	host := decodeAddress(zone[22:26])
	port := int(binary.LittleEndian.Uint16(zone[26:28]))
	return host, port, nil
}

// decodeAddress reads the 4 address bytes the way the client does: a
// little-endian uint32 whose octets are then taken most-significant
// first (the counterpart of the source's CONVIP macro).
func decodeAddress(b []byte) string {
	field := binary.LittleEndian.Uint32(b)
	addr := netip.AddrFrom4([4]byte{
		byte(field >> 24), //nolint:gosec // G115: a shift of a uint32 into a byte is a truncation by design.
		byte(field >> 16), //nolint:gosec // G115: ditto.
		byte(field >> 8),  //nolint:gosec // G115: ditto.
		byte(field),       //nolint:gosec // G115: ditto.
	})
	return addr.String()
}

// sessionTokens reads the session's login ids and the account's sex from
// the accept-login frame.
func sessionTokens(accept []byte) (uint32, uint32, byte, error) {
	if len(accept) < 56 {
		return 0, 0, 0, fmt.Errorf("accept-login frame is %d bytes, too short", len(accept))
	}
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	loginID2 := binary.LittleEndian.Uint32(accept[12:16])
	sex := accept[55]
	return loginID1, loginID2, sex, nil
}

// accountID reads the account id from the accept-login frame.
func accountID(accept []byte) uint32 {
	return binary.LittleEndian.Uint32(accept[8:12])
}
