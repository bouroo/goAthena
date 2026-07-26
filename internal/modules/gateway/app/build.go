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
	return domain.NewDispatcher(login, char, mapT)
}
