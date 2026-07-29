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
	// OnCZUseSkill serves CZ_USE_SKILL2 (0x0438), the skill-cast request. The M14b
	// slice resolves SM_BASH (an offensive weapon skill) on an adjacent mob;
	// other skills are dropped pending later units. Provided by the world module.
	OnCZUseSkill domain.PacketHandler
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
	// OnCZItemPickup serves CZ_ITEM_PICKUP (0x009f), the floor-item pickup
	// request. Provided by the world module (M10a).
	OnCZItemPickup domain.PacketHandler
	// OnCZUseItem serves CZ_USE_ITEM2 (0x0439), consuming and applying a Healing
	// inventory item. Provided by the world module (M12).
	OnCZUseItem domain.PacketHandler
	// Provided by the world module (M10b).
	OnCZReqWearEquip domain.PacketHandler
	// OnCZReqTakeoffEquip serves CZ_REQ_TAKEOFF_EQUIP (0x00ab), the unequip
	// request. Provided by the world module (M10b).
	OnCZReqTakeoffEquip domain.PacketHandler
	// OnCZContactNPC serves CZ_CONTACTNPC (0x0090), initiating an NPC dialog.
	// Provided by the content module.
	OnCZContactNPC domain.PacketHandler
	// OnCZReqNextScript serves CZ_REQ_NEXT_SCRIPT (0x00b9), advancing the dialog.
	// Provided by the content module.
	OnCZReqNextScript domain.PacketHandler
	// OnCZChooseMenu serves CZ_CHOOSE_MENU (0x00b8), responding to a dialog choice.
	// Provided by the content module.
	OnCZChooseMenu domain.PacketHandler
	// OnCZCloseDialog serves CZ_CLOSE_DIALOG (0x0146), ending the dialog.
	// Provided by the content module.
	OnCZCloseDialog domain.PacketHandler
	// OnCZAckSelectDealtype serves CZ_ACK_SELECT_DEALTYPE (0x00c5), the
	// Buy/Sell/Cancel deal-type selection after contacting a shop NPC.
	// Provided by the shop module (M13).
	OnCZAckSelectDealtype domain.PacketHandler
	// OnCZPCPurchaseItemList serves CZ_PC_PURCHASE_ITEMLIST (0x00c8), the
	// shop buy request. Provided by the shop module (U4).
	OnCZPCPurchaseItemList domain.PacketHandler
	// OnCZPCSellItemList serves CZ_PC_SELL_ITEMLIST (0x00c9), the shop sell
	// request. Provided by the shop module.
	OnCZPCSellItemList domain.PacketHandler
}

// BuildDispatcher assembles the three role-keyed dispatch tables from the
// contributed handlers and returns the immutable Dispatcher every transport
// shares — including the dedicated map listener, whose fresh connections start
// at the map role and so dispatch against the map table built here.
func BuildDispatcher(h Handlers) *domain.Dispatcher {
	return domain.NewDispatcher(buildLoginTable(h), buildCharTable(h), buildMapTable(h))
}

// addHandler installs h into table under header if h is non-nil. Returning
// the table (rather than mutating in place) keeps the call-site one line
// without forcing callers to declare a key variable for every entry.
func addHandler(table domain.PacketHandlerTable, header uint16, h domain.PacketHandler) domain.PacketHandlerTable {
	if h != nil {
		table[header] = h
	}
	return table
}

// loginHandlers groups the single CA_LOGIN (0x0064) entry the login role owns;
// the login role is the server's first entry point.
func buildLoginTable(h Handlers) domain.PacketHandlerTable {
	return addHandler(domain.PacketHandlerTable{}, packet.HeaderCALOGIN, h.OnCALogin)
}

// charHandlers groups the three char-server entry points: CH_ENTER returns
// the roster, CH_SELECT_CHAR redirects to the zone, CH_MAKE_CHAR creates a
// new character. All three share the same conn role so a partial milestone
// (e.g. make missing) is still routable.
func buildCharTable(h Handlers) domain.PacketHandlerTable {
	return addHandler(
		addHandler(
			addHandler(domain.PacketHandlerTable{}, packet.HeaderCHMAKECHAR, h.OnCHMakeChar),
			packet.HeaderCHSELECTCHAR, h.OnCHSelectChar),
		packet.HeaderCHENTER, h.OnCHEnter)
}

// mapHandlers groups every map-role opcode the gateway ever dispatches:
// the CZ_ENTER trust gate, every combat/movement/interaction packet the
// world module ships, and the four content-dialog frames the content module
// ships. Bundling them in one helper keeps BuildDispatcher readable and
// makes the role-vs-module split obvious at a glance.
func buildMapTable(h Handlers) domain.PacketHandlerTable {
	table := domain.PacketHandlerTable{}
	table = addHandler(table, packet.HeaderCZENTER, h.OnCZEnter)
	table = addHandler(table, packet.HeaderCZREQUESTMOVE, h.OnCZRequestMove)
	table = addHandler(table, packet.HeaderCZACTIONREQUEST, h.OnCZActionRequest)
	table = addHandler(table, packet.HeaderCZUSESKILL, h.OnCZUseSkill)
	table = addHandler(table, packet.HeaderCZREQUESTTIME, h.OnCZRequestTime)
	table = addHandler(table, packet.HeaderCZCHANGEDIR, h.OnCZChangeDir)
	table = addHandler(table, packet.HeaderCZREQEMOTION, h.OnCZReqEmotion)
	table = addHandler(table, packet.HeaderCZRESTART, h.OnCZRestart)
	table = addHandler(table, packet.HeaderCZITEMPICKUP, h.OnCZItemPickup)
	table = addHandler(table, packet.HeaderCZUSEITEM2, h.OnCZUseItem)
	table = addHandler(table, packet.HeaderCZREQWEAREQUIPV5, h.OnCZReqWearEquip)
	table = addHandler(table, packet.HeaderCZREQTAKEOFFEQUIP, h.OnCZReqTakeoffEquip)
	table = addHandler(table, packet.HeaderCZCONTACTNPC, h.OnCZContactNPC)
	table = addHandler(table, packet.HeaderCZREQNEXTSCRIPT, h.OnCZReqNextScript)
	table = addHandler(table, packet.HeaderCZCHOOSEMENU, h.OnCZChooseMenu)
	table = addHandler(table, packet.HeaderCZCLOSEDIALOG, h.OnCZCloseDialog)
	table = addHandler(table, packet.HeaderCZACKSELECTDEALTYPE, h.OnCZAckSelectDealtype)
	table = addHandler(table, packet.HeaderCZPCPURCHASEITEMLIST, h.OnCZPCPurchaseItemList)
	return addHandler(table, packet.HeaderCZPCSELLITEMLIST, h.OnCZPCSellItemList)
}
