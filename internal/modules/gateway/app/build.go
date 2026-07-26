package app

import (
	"github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Handlers holds the feature-module packet-handler contributions the gateway
// dispatches. Every field is a gateway/domain.PacketHandler function value, so
// the gateway never imports a feature module (the architecture guard forbids
// cross-module impl imports); the composition root — the one place allowed to
// see both the gateway and each feature module — fills these from the modules'
// concrete handlers (e.g. account's CALoginHandler.Handle).
//
// A nil field means that opcode has no handler yet: Dispatch returns
// ErrNoHandler for it and the session continues, so partially-built milestones
// do not disconnect clients for unimplemented packets.
type Handlers struct {
	// OnCALogin serves CA_LOGIN (0x0064), the login-server entry point.
	// Provided by the account module.
	OnCALogin domain.PacketHandler
	// OnCHEnter serves CH_ENTER (0x0065), the char-server entry point that
	// returns the account's character list. Provided by the character module.
	OnCHEnter domain.PacketHandler
	// OnCHSelectChar serves CH_SELECT_CHAR (0x0066), which redirects the
	// client to the zone server. Provided by the character module.
	OnCHSelectChar domain.PacketHandler
	// OnCHMakeChar serves CH_MAKE_CHAR (0x0a39), the character-creation
	// request. Provided by the character module.
	OnCHMakeChar domain.PacketHandler
	// OnCZEnter serves CZ_ENTER (0x0072), the map-server entry point and trust
	// gate. Provided by the world module.
	OnCZEnter domain.PacketHandler
	// OnCZRequestMove serves CZ_REQUEST_MOVE (0x0085), the click-to-walk
	// request. Provided by the world module (M4c).
	OnCZRequestMove domain.PacketHandler
	// OnCZActionRequest serves CZ_ACTION_REQUEST (0x0089), the attack/motion
	// request. Provided by the world module (M6).
	OnCZActionRequest domain.PacketHandler
	// OnCZRequestTime serves CZ_REQUEST_TIME (0x007e), the clock-skew probe.
	// Provided by the world module (M7).
	OnCZRequestTime domain.PacketHandler
	// OnCZChangeDir serves CZ_CHANGE_DIR (0x009b), the facing-change request.
	// Provided by the world module (M7).
	OnCZChangeDir domain.PacketHandler
	// OnCZReqEmotion serves CZ_REQ_EMOTION (0x00bf), the emotion-icon request.
	// Provided by the world module (M7).
	OnCZReqEmotion domain.PacketHandler
	// OnCZRestart serves CZ_RESTART (0x00b2), the leave-world request (respawn
	// or return to char select). Provided by the world module (M7).
	OnCZRestart domain.PacketHandler
}

// BuildDispatcher assembles the three role-keyed dispatch tables from the
// contributed handlers and returns the immutable Dispatcher every transport
// shares — including the dedicated map listener, whose fresh connections start
// at the map role and so dispatch against the map table built here.
func BuildDispatcher(h Handlers) *domain.Dispatcher {
	login := domain.PacketHandlerTable{}
	if h.OnCALogin != nil {
		login[packet.HeaderCALOGIN] = h.OnCALogin
	}
	char := domain.PacketHandlerTable{}
	if h.OnCHEnter != nil {
		char[packet.HeaderCHENTER] = h.OnCHEnter
	}
	if h.OnCHSelectChar != nil {
		char[packet.HeaderCHSELECTCHAR] = h.OnCHSelectChar
	}
	if h.OnCHMakeChar != nil {
		char[packet.HeaderCHMAKECHAR] = h.OnCHMakeChar
	}
	mapT := domain.PacketHandlerTable{}
	if h.OnCZEnter != nil {
		mapT[packet.HeaderCZENTER] = h.OnCZEnter
	}
	if h.OnCZRequestMove != nil {
		mapT[packet.HeaderCZREQUESTMOVE] = h.OnCZRequestMove
	}
	if h.OnCZActionRequest != nil {
		mapT[packet.HeaderCZACTIONREQUEST] = h.OnCZActionRequest
	}
	if h.OnCZRequestTime != nil {
		mapT[packet.HeaderCZREQUESTTIME] = h.OnCZRequestTime
	}
	if h.OnCZChangeDir != nil {
		mapT[packet.HeaderCZCHANGEDIR] = h.OnCZChangeDir
	}
	if h.OnCZReqEmotion != nil {
		mapT[packet.HeaderCZREQEMOTION] = h.OnCZReqEmotion
	}
	if h.OnCZRestart != nil {
		mapT[packet.HeaderCZRESTART] = h.OnCZRestart
	}
	return domain.NewDispatcher(login, char, mapT)
}
