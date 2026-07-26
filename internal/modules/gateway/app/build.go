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
}

// BuildDispatcher assembles the three role-keyed dispatch tables from the
// contributed handlers and returns the immutable Dispatcher the TCP and
// WebSocket transports share. The char table (CH_ENTER, CH_SELECT_CHAR) fills
// in at M2; the map table lands at M3.
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
	return domain.NewDispatcher(login, char, nil)
}
