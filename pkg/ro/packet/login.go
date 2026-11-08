package packet

// Login-server packet header IDs.
//
// Source: rathena/src/common/packets.hpp DEFINE_PACKET_HEADER(NAME, 0x...).
//
// The login server uses two header IDs per direction for some packets
// (a legacy pre-2017 form and a modern PACKETVER >= 20170315 form).
// Both are registered so the gateway can parse either client variant
// and encode either server variant.

// C→S — client → login server.
// HeaderCACONNECTINFOCHANGED is the source pack's own opcode for this packet.
func HeaderCACONNECTINFOCHANGED() uint16 { return opcode("CA_CONNECT_INFO_CHANGED") }

// HeaderCAEXEHASHCHECK is the source pack's own opcode for this packet.
func HeaderCAEXEHASHCHECK() uint16 { return opcode("CA_EXE_HASHCHECK") }

// HeaderCAREQHASH is the source pack's own opcode for this packet.
func HeaderCAREQHASH() uint16 { return opcode("CA_REQ_HASH") }

// HeaderCALOGIN is the source pack's own opcode for this packet.
func HeaderCALOGIN() uint16 { return opcode("CA_LOGIN") }

// HeaderCALOGIN2 is the source pack's own opcode for this packet.
func HeaderCALOGIN2() uint16 { return opcode("CA_LOGIN2") }

// HeaderCALOGIN3 is the source pack's own opcode for this packet.
func HeaderCALOGIN3() uint16 { return opcode("CA_LOGIN3") }

// HeaderCALOGIN4 is the source pack's own opcode for this packet.
func HeaderCALOGIN4() uint16 { return opcode("CA_LOGIN4") }

// HeaderCALOGINPCBANG is the source pack's own opcode for this packet.
func HeaderCALOGINPCBANG() uint16 { return opcode("CA_LOGIN_PCBANG") }

// HeaderCALOGINCHANNEL is the source pack's own opcode for this packet.
func HeaderCALOGINCHANNEL() uint16 { return opcode("CA_LOGIN_CHANNEL") }

// HeaderCASSOLOGINREQ is the source pack's own opcode for this packet.
func HeaderCASSOLOGINREQ() uint16 { return opcode("CA_SSO_LOGIN_REQ") }

// HeaderCTAUTH is the source pack's own opcode for this packet.
func HeaderCTAUTH() uint16 { return opcode("CT_AUTH") }

// S→C — login server → client.
// HeaderSCNOTIFYBAN is the source pack's own opcode for this packet.
func HeaderSCNOTIFYBAN() uint16 { return opcode("SC_NOTIFY_BAN") }

// HeaderACACKHASH is the source pack's own opcode for this packet.
func HeaderACACKHASH() uint16 { return opcode("AC_ACK_HASH") }

// HeaderACACCEPTLOGINOld is the source pack's own opcode for this packet.
func HeaderACACCEPTLOGINOld() uint16 { return opcode("owner_dead") }

// HeaderACACCEPTLOGIN is the source pack's own opcode for this packet.
func HeaderACACCEPTLOGIN() uint16 { return opcode("AC_ACCEPT_LOGIN") }

// HeaderACREFUSELOGINOld is the source pack's own opcode for this packet.
func HeaderACREFUSELOGINOld() uint16 { return opcode("CZ_ENTER2") }

// HeaderACREFUSELOGIN is the source pack's own opcode for this packet.
func HeaderACREFUSELOGIN() uint16 { return opcode("AC_REFUSE_LOGIN") }

// Fixed on-wire byte lengths derived from the packed struct layouts in
// rathena/src/common/packets.hpp. Constants used:
//
//	NAME_LENGTH         = 24  (mmo.hpp:154)
//	WEB_AUTH_TOKEN_LENGTH = 17 (mmo.hpp:121)

// sizeCAConnectInfoChanged() = sizeof(int16) + NAME_LENGTH = 2 + 24 = 26.
// sizeCAConnectInfoChanged is the source pack's own length for this packet.
func sizeCAConnectInfoChanged() int { return packetLengthOf(512) }

// sizeCAExeHashcheck() = sizeof(int16) + hash[16] = 2 + 16 = 18.
// sizeCAExeHashcheck is the source pack's own length for this packet.
func sizeCAExeHashcheck() int { return packetLengthOf(516) }

// sizeCAReqHash() = sizeof(int16) = 2.
// sizeCAReqHash is the source pack's own length for this packet.
func sizeCAReqHash() int { return packetLengthOf(475) }

// sizeCALogin() = sizeof(int16) + version(4) + username(24) + password(24) + clienttype(1) = 55.
// sizeCALogin is the source pack's own length for this packet.
func sizeCALogin() int { return packetLengthOf(100) }

// sizeCALoginPCBang() = int16 + version(4) + username(24) + password(24) + clienttype(1) + ip(16) + mac(13) = 84.
// sizeCALoginPCBang is the source pack's own length for this packet.
func sizeCALoginPCBang() int { return packetLengthOf(631) }

// sizeCALoginChannel() = int16 + version(4) + username(24) + password(24) + clienttype(1) + ip(16) + mac(13) + is_gravity(1) = 85.
// sizeCALoginChannel is the source pack's own length for this packet.
func sizeCALoginChannel() int { return packetLengthOf(688) }

// sizeCALogin2() = int16 + version(4) + username(24) + passwordMD5(16) + clienttype(1) = 47.
// sizeCALogin2 is the source pack's own length for this packet.
func sizeCALogin2() int { return packetLengthOf(477) }

// sizeCALogin3() = sizeCALogin2() + clientinfo(1) = 48.
// sizeCALogin3 is the source pack's own length for this packet.
func sizeCALogin3() int { return packetLengthOf(506) }

// sizeCALogin4() = int16 + version(4) + username(24) + passwordMD5(16) + clienttype(1) + mac(13) = 60.
// sizeCALogin4 is the source pack's own length for this packet.
func sizeCALogin4() int { return packetLengthOf(636) }

// sizeCTAuth() = int16 + unknown[66] = 68.
// sizeCTAuth is the source pack's own length for this packet.
func sizeCTAuth() int { return layout("PACKET_CT_AUTH") }

// sizeSCNotifyBan() = int16 + result(1) = 3.
// sizeSCNotifyBan is the source pack's own length for this packet.
func sizeSCNotifyBan() int { return layout("PACKET_SC_NOTIFY_BAN") }

// sizeACRefuseLoginOld() = int16 + error(1) + unblock_time[20] = 23.
// sizeACRefuseLoginOld is the source pack's own length for this packet.
func sizeACRefuseLoginOld() int { return packetLengthOf(106) }

// sizeACRefuseLogin() = int16 + error(4) + unblock_time[20] = 26.
// sizeACRefuseLogin is the source pack's own length for this packet.
func sizeACRefuseLogin() int { return layout("PACKET_AC_REFUSE_LOGIN") }

// NewLoginServerDB returns a packet database pre-populated with all known
// login-server packet definitions (both directions).
//
// Inbound (C→S) entries mirror rathena's LoginPacketDatabase at
// rathena/src/login/loginclif.cpp:483-498 exactly. Outbound (S→C) entries
// are added for the gateway encoder side, even though rAthena does not
// register them in its own database (loginclif constructs these packets
// inline and sends them with socket_send).
//
// All sizes are sourced from rathena/src/common/packets.hpp packed struct
// layouts (verified against rathena/src/login/loginclif.cpp sizeof calls).
//
// Variable-length packets return VariableLength (-1) from Length; the
// gateway must read the uint16 length at byte offset 2 of the packet header
// to determine the on-wire size, per
// rathena/src/common/packets.hpp:653-739 (PacketDatabase::handle dynamic
// branch) and rathena/src/map/clif.cpp:25749 (RFIFOW(fd,2)).
func NewLoginServerDB() *DB {
	db := NewDB()

	// --- C→S: mirrors rathena/src/login/loginclif.cpp:483-498 verbatim.
	db.Register(Definition{
		ID:        HeaderCACONNECTINFOCHANGED(),
		Name:      "CA_CONNECT_INFO_CHANGED",
		Length:    sizeCAConnectInfoChanged(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCAEXEHASHCHECK(),
		Name:      "CA_EXE_HASHCHECK",
		Length:    sizeCAExeHashcheck(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGIN(),
		Name:      "CA_LOGIN",
		Length:    sizeCALogin(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGINPCBANG(),
		Name:      "CA_LOGIN_PCBANG",
		Length:    sizeCALoginPCBang(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGINCHANNEL(),
		Name:      "CA_LOGIN_CHANNEL",
		Length:    sizeCALoginChannel(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGIN2(),
		Name:      "CA_LOGIN2",
		Length:    sizeCALogin2(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGIN3(),
		Name:      "CA_LOGIN3",
		Length:    sizeCALogin3(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGIN4(),
		Name:      "CA_LOGIN4",
		Length:    sizeCALogin4(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCASSOLOGINREQ(),
		Name:      "CA_SSO_LOGIN_REQ",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCAREQHASH(),
		Name:      "CA_REQ_HASH",
		Length:    sizeCAReqHash(),
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCTAUTH(),
		Name:      "CT_AUTH",
		Length:    sizeCTAuth(),
		Direction: DirectionClientToServer,
	})

	// --- S→C: encoder-side reference; not registered in rAthena's
	// LoginPacketDatabase but constructed inline (loginclif.cpp sends these
	// with socket_send).
	db.Register(Definition{
		ID:        HeaderSCNOTIFYBAN(),
		Name:      "SC_NOTIFY_BAN",
		Length:    sizeSCNotifyBan(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderACACKHASH(),
		Name:      "AC_ACK_HASH",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	// Legacy pre-2017 AC_ACCEPT_LOGIN (packets.hpp:200-220): int16 +
	// int16 packetLength + login_id1..sex + PACKET_AC_ACCEPT_LOGIN_sub[]
	// trailing flexible array → variable length.
	db.Register(Definition{
		ID:        HeaderACACCEPTLOGINOld(),
		Name:      "AC_ACCEPT_LOGIN_OLD",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	// Modern PACKETVER >= 20170315 AC_ACCEPT_LOGIN (packets.hpp:186-198):
	// adds token[WEB_AUTH_TOKEN_LENGTH] before the char_servers[] flexible
	// array → variable length.
	db.Register(Definition{
		ID:        HeaderACACCEPTLOGIN(),
		Name:      "AC_ACCEPT_LOGIN",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderACREFUSELOGINOld(),
		Name:      "AC_REFUSE_LOGIN_OLD",
		Length:    sizeACRefuseLoginOld(),
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderACREFUSELOGIN(),
		Name:      "AC_REFUSE_LOGIN",
		Length:    sizeACRefuseLogin(),
		Direction: DirectionServerToClient,
	})

	return db
}
