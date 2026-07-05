package packet

// Login-server packet header IDs.
//
// Source: rathena/src/common/packets.hpp DEFINE_PACKET_HEADER(NAME, 0x...).
//
// The login server uses two header IDs per direction for some packets
// (a legacy pre-2017 form and a modern PACKETVER >= 20170315 form).
// Both are registered so the gateway can parse either client variant
// and encode either server variant.
const (
	// C→S — client → login server.
	HeaderCACONNECTINFOCHANGED uint16 = 0x0200
	HeaderCAEXEHASHCHECK       uint16 = 0x0204
	HeaderCAREQHASH            uint16 = 0x01db
	HeaderCALOGIN              uint16 = 0x0064
	HeaderCALOGIN2             uint16 = 0x01dd
	HeaderCALOGIN3             uint16 = 0x01fa
	HeaderCALOGIN4             uint16 = 0x027c
	HeaderCALOGINPCBANG        uint16 = 0x0277
	HeaderCALOGINCHANNEL       uint16 = 0x02b0
	HeaderCASSOLOGINREQ        uint16 = 0x0825
	HeaderCTAUTH               uint16 = 0x0acf

	// S→C — login server → client.
	HeaderSCNOTIFYBAN      uint16 = 0x0081
	HeaderACACKHASH        uint16 = 0x01dc
	HeaderACACCEPTLOGINOld uint16 = 0x0069
	HeaderACACCEPTLOGIN    uint16 = 0x0ac4
	HeaderACREFUSELOGINOld uint16 = 0x006a
	HeaderACREFUSELOGIN    uint16 = 0x083e
)

// Fixed on-wire byte lengths derived from the packed struct layouts in
// rathena/src/common/packets.hpp. Constants used:
//
//	NAME_LENGTH         = 24  (mmo.hpp:154)
//	WEB_AUTH_TOKEN_LENGTH = 17 (mmo.hpp:121)
const (
	// sizeCAConnectInfoChanged = sizeof(int16) + NAME_LENGTH = 2 + 24 = 26.
	sizeCAConnectInfoChanged = 26
	// sizeCAExeHashcheck = sizeof(int16) + hash[16] = 2 + 16 = 18.
	sizeCAExeHashcheck = 18
	// sizeCAReqHash = sizeof(int16) = 2.
	sizeCAReqHash = 2
	// sizeCALogin = sizeof(int16) + version(4) + username(24) + password(24) + clienttype(1) = 55.
	sizeCALogin = 55
	// sizeCALoginPCBang = int16 + version(4) + username(24) + password(24) + clienttype(1) + ip(16) + mac(13) = 84.
	sizeCALoginPCBang = 84
	// sizeCALoginChannel = int16 + version(4) + username(24) + password(24) + clienttype(1) + ip(16) + mac(13) + is_gravity(1) = 85.
	sizeCALoginChannel = 85
	// sizeCALogin2 = int16 + version(4) + username(24) + passwordMD5(16) + clienttype(1) = 47.
	sizeCALogin2 = 47
	// sizeCALogin3 = sizeCALogin2 + clientinfo(1) = 48.
	sizeCALogin3 = 48
	// sizeCALogin4 = int16 + version(4) + username(24) + passwordMD5(16) + clienttype(1) + mac(13) = 60.
	sizeCALogin4 = 60
	// sizeCTAuth = int16 + unknown[66] = 68.
	sizeCTAuth = 68

	// sizeSCNotifyBan = int16 + result(1) = 3.
	sizeSCNotifyBan = 3
	// sizeACRefuseLoginOld = int16 + error(1) + unblock_time[20] = 23.
	sizeACRefuseLoginOld = 23
	// sizeACRefuseLogin = int16 + error(4) + unblock_time[20] = 26.
	sizeACRefuseLogin = 26
)

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
		ID:        HeaderCACONNECTINFOCHANGED,
		Name:      "CA_CONNECT_INFO_CHANGED",
		Length:    sizeCAConnectInfoChanged,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCAEXEHASHCHECK,
		Name:      "CA_EXE_HASHCHECK",
		Length:    sizeCAExeHashcheck,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGIN,
		Name:      "CA_LOGIN",
		Length:    sizeCALogin,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGINPCBANG,
		Name:      "CA_LOGIN_PCBANG",
		Length:    sizeCALoginPCBang,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGINCHANNEL,
		Name:      "CA_LOGIN_CHANNEL",
		Length:    sizeCALoginChannel,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGIN2,
		Name:      "CA_LOGIN2",
		Length:    sizeCALogin2,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGIN3,
		Name:      "CA_LOGIN3",
		Length:    sizeCALogin3,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCALOGIN4,
		Name:      "CA_LOGIN4",
		Length:    sizeCALogin4,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCASSOLOGINREQ,
		Name:      "CA_SSO_LOGIN_REQ",
		Length:    VariableLength,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCAREQHASH,
		Name:      "CA_REQ_HASH",
		Length:    sizeCAReqHash,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCTAUTH,
		Name:      "CT_AUTH",
		Length:    sizeCTAuth,
		Direction: DirectionClientToServer,
	})

	// --- S→C: encoder-side reference; not registered in rAthena's
	// LoginPacketDatabase but constructed inline (loginclif.cpp sends these
	// with socket_send).
	db.Register(Definition{
		ID:        HeaderSCNOTIFYBAN,
		Name:      "SC_NOTIFY_BAN",
		Length:    sizeSCNotifyBan,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderACACKHASH,
		Name:      "AC_ACK_HASH",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	// Legacy pre-2017 AC_ACCEPT_LOGIN (packets.hpp:200-220): int16 +
	// int16 packetLength + login_id1..sex + PACKET_AC_ACCEPT_LOGIN_sub[]
	// trailing flexible array → variable length.
	db.Register(Definition{
		ID:        HeaderACACCEPTLOGINOld,
		Name:      "AC_ACCEPT_LOGIN_OLD",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	// Modern PACKETVER >= 20170315 AC_ACCEPT_LOGIN (packets.hpp:186-198):
	// adds token[WEB_AUTH_TOKEN_LENGTH] before the char_servers[] flexible
	// array → variable length.
	db.Register(Definition{
		ID:        HeaderACACCEPTLOGIN,
		Name:      "AC_ACCEPT_LOGIN",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderACREFUSELOGINOld,
		Name:      "AC_REFUSE_LOGIN_OLD",
		Length:    sizeACRefuseLoginOld,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderACREFUSELOGIN,
		Name:      "AC_REFUSE_LOGIN",
		Length:    sizeACRefuseLogin,
		Direction: DirectionServerToClient,
	})

	return db
}
