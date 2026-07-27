// Package app implements the character bounded context's use cases: the
// CH_ENTER (char list) and CH_SELECT_CHAR (zone redirect) packet handlers and
// the domain-to-wire mapper they share. It depends on the character domain
// ports, the gateway domain Conn, and the packet kernel — nothing transport- or
// persistence-specific.
package app

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// refuseEnterRejected is the HC_REFUSE_ENTER error code rAthena sends for every
// CH_ENTER rejection on the char-server path ("rejected from server" — every
// chclif_reject call site in char_clif.cpp passes 0).
const refuseEnterRejected uint8 = 0

// Char-list header defaults matching a default non-VIP rAthena server (mmo.hpp,
// config/core.hpp): MAX_CHARS=15, MAX_CHAR_VIP=0, MAX_CHAR_BILLING=0 ⇒
// MIN_CHARS=15, and chars_vip=0. The char-select UI uses these for the slot
// page count and premium markers; they become config-driven when the slot/VIP
// system is implemented (not on the combat-slice critical path).
const (
	defaultTotalSlots   uint8 = 15 // MAX_CHARS — absolute slot ceiling.
	defaultPremiumStart uint8 = 15 // MIN_CHARS — first premium slot index.
	defaultPremiumEnd   uint8 = 15 // MIN_CHARS + chars_vip (0 with no VIP).
)

// CharEnterHandler serves CH_ENTER (0x0065). It verifies the packet's echoed
// credentials against the connection's login-accept auth cache, then replies
// with HC_ACCEPT_ENTER (0x006b) carrying the account's character list.
//
// The connection itself is proof the login that minted the cache was accepted,
// so the cache is the trust anchor: the packet's AccountID is never used for
// the char query (the cache's is), so a tampered AccountID can at most earn a
// refuse, never an impersonation.
//
// Error policy mirrors CALoginHandler: a parse/encode or repo fault is returned
// so ProcessBytes logs it; an expected refusal (credential mismatch) is a nil
// error and the connection stays open, matching rAthena's chclif_reject.
type CharEnterHandler struct {
	chars domain.CharacterRepository
}

// NewCharEnterHandler builds a CH_ENTER handler over the character repository.
func NewCharEnterHandler(chars domain.CharacterRepository) *CharEnterHandler {
	return &CharEnterHandler{chars: chars}
}

// Handle implements gateway/domain.PacketHandler for CH_ENTER.
func (h *CharEnterHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCHEnter(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CH_ENTER: %w", err)
	}

	auth := conn.Auth()
	if !enterCredentialsMatch(req, auth) {
		if err := (packet.RefuseEnterResponse{Error: refuseEnterRejected}).Encode(connWriter{conn}); err != nil {
			return fmt.Errorf("encode HC_REFUSE_ENTER: %w", err)
		}
		return nil
	}

	chars, err := h.chars.ListByAccount(ctx, auth.AccountID)
	if err != nil {
		return fmt.Errorf("list characters for account %d: %w", auth.AccountID, err)
	}

	info := make([]packet.CharacterInfo, len(chars))
	for i := range chars {
		info[i] = MapCharacterInfo(chars[i])
	}
	resp := packet.AcceptEnterResponse{
		Total:        defaultTotalSlots,
		PremiumStart: defaultPremiumStart,
		PremiumEnd:   defaultPremiumEnd,
		Characters:   info,
	}
	if err := resp.Encode(connWriter{conn}); err != nil {
		return fmt.Errorf("encode HC_ACCEPT_ENTER: %w", err)
	}
	return nil
}

// CharSelectHandler serves CH_SELECT_CHAR (0x0066). It resolves the chosen slot
// against the cached account and redirects the client to the zone server via
// HC_NOTIFY_ZONESVR (0x0ac5). The zone endpoint is fixed at construction from
// cfg.Gateway.MapAddr; the per-character MapName comes from the selected char's
// last_map column.
type CharSelectHandler struct {
	chars domain.CharacterRepository
	zone  ZoneAddr
}

// NewCharSelectHandler builds a CH_SELECT_CHAR handler over the character
// repository, advertising the given zone endpoint in every redirect.
func NewCharSelectHandler(chars domain.CharacterRepository, zone ZoneAddr) *CharSelectHandler {
	return &CharSelectHandler{chars: chars, zone: zone}
}

// Handle implements gateway/domain.PacketHandler for CH_SELECT_CHAR.
func (h *CharSelectHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCHSelectChar(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CH_SELECT_CHAR: %w", err)
	}

	auth := conn.Auth()
	char, err := h.chars.GetBySlot(ctx, auth.AccountID, req.Slot)
	if err != nil {
		if errors.Is(err, domain.ErrCharacterNotFound) {
			// No character at the slot: refuse and keep the connection so the
			// client can pick again, mirroring a rejected CH_ENTER.
			if encErr := (packet.RefuseEnterResponse{Error: refuseEnterRejected}).Encode(connWriter{conn}); encErr != nil {
				return fmt.Errorf("encode HC_REFUSE_ENTER: %w", encErr)
			}
			return nil
		}
		return fmt.Errorf("select char account %d slot %d: %w", auth.AccountID, req.Slot, err)
	}

	redirect := packet.NotifyZoneServerResponse{
		CID:     char.CharID,
		MapName: char.LastMap,
		IP:      h.zone.IP,
		Port:    h.zone.Port,
	}
	if err := redirect.Encode(connWriter{conn}); err != nil {
		return fmt.Errorf("encode HC_NOTIFY_ZONESVR: %w", err)
	}
	return nil
}

// ZoneAddr is the parsed map-server endpoint advertised in HC_NOTIFY_ZONESVR.
// IP is the host's IPv4 in rAthena's inet_addr/htonl convention (the value
// binary.LittleEndian.PutUint32 writes back to network order on the wire); Port
// is the TCP port.
type ZoneAddr struct {
	IP   uint32
	Port uint16
}

// ParseZoneAddr splits a "host:port" map address into the IP/Port pair the
// HC_NOTIFY_ZONESVR wire format expects. The host must resolve to an IPv4
// address. The composition root calls this once with cfg.Gateway.MapAddr.
func ParseZoneAddr(addr string) (ZoneAddr, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return ZoneAddr{}, fmt.Errorf("parse zone addr %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ips, lerr := net.LookupIP(host)
		if lerr != nil {
			return ZoneAddr{}, fmt.Errorf("resolve zone host %q: %w", host, lerr)
		}
		for _, resolved := range ips {
			if v4 := resolved.To4(); v4 != nil {
				ip = v4
				break
			}
		}
	}
	v4 := ip.To4()
	if v4 == nil {
		return ZoneAddr{}, fmt.Errorf("zone host %q has no IPv4 address", host)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return ZoneAddr{}, fmt.Errorf("parse zone port %q: %w", portStr, err)
	}
	// inet_addr yields the network-order octets [a,b,c,d]; rAthena stores them
	// as the host-endian uint32 a little-endian machine reads them as, which the
	// encoder writes back to network order via LittleEndian.PutUint32. Reading
	// the octets little-endian reproduces that value on any host.
	return ZoneAddr{IP: binary.LittleEndian.Uint32(v4), Port: uint16(port)}, nil
}

// enterCredentialsMatch reports whether a CH_ENTER packet's echoed credentials
// match the connection's cached login-accept auth. LoginID2 is the session
// secret and is compared constant-time; AccountID, LoginID1, and Sex are echoed
// plainly (they are not secret). A zero AccountID cache means the connection
// never completed login-accept, so no CH_ENTER can match.
func enterCredentialsMatch(req packet.CHEnterRequest, auth gwdomain.ConnAuth) bool {
	if auth.AccountID == 0 {
		return false
	}
	if req.AccountID != auth.AccountID || req.LoginID1 != auth.LoginID1 || req.Sex != auth.Sex {
		return false
	}
	return constantTimeEq32(req.LoginID2, auth.LoginID2)
}

// constantTimeEq32 compares two uint32 values in constant time, mirroring the
// crypto/subtle compare account/app/service.go uses for passwords.
func constantTimeEq32(a, b uint32) bool {
	var ab, bb [4]byte
	binary.LittleEndian.PutUint32(ab[:], a)
	binary.LittleEndian.PutUint32(bb[:], b)
	return subtle.ConstantTimeCompare(ab[:], bb[:]) == 1
}

// connWriter adapts a gateway domain.Conn (Write returns only error) to the
// io.Writer the kernel packet encoders target. Duplicated from account/app —
// the account module kept its own copy to make the gateway domain the only
// cross-module dependency; the character module does the same for the same
// reason. A shared helper would require either module to import the other or a
// third package, neither of which the layering warrants.
type connWriter struct{ conn gwdomain.Conn }

func (w connWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(p); err != nil {
		return 0, fmt.Errorf("write to conn: %w", err)
	}
	return len(p), nil
}
