package protocol

// Opcodes holds the opcodes the server dispatches on, resolved from one
// source pack.
//
// The opcodes are protocol facts, so they live here rather than in the
// feature whose handler speaks them: the map server dispatches on the
// economy's opcode and the inventory's, and a shared fact belongs in a
// shared place.
//
// The values are resolved from the selected source's own headers (see
// cmd/protocolgen), so they hold for exactly the source and client family
// the set resolved.
type Opcodes struct {
	// set is the source pack the opcodes came from.
	set *Set
}

// Opcodes returns the opcodes the server dispatches on, resolved from the
// set.
func (s *Set) Opcodes() Opcodes { return Opcodes{set: s} }

// CHSelectChar is upstream's CH_SELECT_CHAR.
func (o Opcodes) CHSelectChar() uint16 { return o.set.MustOpcode("protocol", "CH_SELECT_CHAR") }

// CHMakeChar is upstream's CH_MAKE_CHAR.
func (o Opcodes) CHMakeChar() uint16 { return o.set.MustOpcode("protocol", "CH_MAKE_CHAR") }

// The opcodes the map server dispatches on.

// CZWantToConnection is the opcode the source registers for
// clif_parse_WantToConnection.
//
// The packet has no header constant upstream, so the parse handler is the
// only name it carries.
func (o Opcodes) CZWantToConnection() uint16 {
	return o.set.MustOpcodeByHandler("protocol", "clif_parse_WantToConnection")
}

// CZLoadEndAck is the opcode the source registers for
// clif_parse_LoadEndAck.
func (o Opcodes) CZLoadEndAck() uint16 {
	return o.set.MustOpcodeByHandler("protocol", "clif_parse_LoadEndAck")
}

// CZRequestMove is the opcode the source registers for
// clif_parse_WalkToXY.
func (o Opcodes) CZRequestMove() uint16 {
	return o.set.MustOpcodeByHandler("protocol", "clif_parse_WalkToXY")
}

// CZContactNPC is upstream's CZ_CONTACTNPC.
func (o Opcodes) CZContactNPC() uint16 { return o.set.MustOpcode("protocol", "CZ_CONTACTNPC") }

// CZInventoryTab is upstream's CZ_INVENTORY_TAB.
func (o Opcodes) CZInventoryTab() uint16 { return o.set.MustOpcode("protocol", "CZ_INVENTORY_TAB") }

// CZAuctionItemSearch is upstream's CZ_AUCTION_ITEM_SEARCH.
func (o Opcodes) CZAuctionItemSearch() uint16 {
	return o.set.MustOpcode("protocol", "CZ_AUCTION_ITEM_SEARCH")
}

// CZReqTradeBuyingStore is upstream's CZ_REQ_TRADE_BUYING_STORE.
//
// Upstream registers the packet numerically, so the value is the constant
// upstream's own table holds.
func (o Opcodes) CZReqTradeBuyingStore() uint16 { return 0x0819 }

// CZBroadcast is upstream's CZ_BROADCAST.
func (o Opcodes) CZBroadcast() uint16 { return o.set.MustOpcode("protocol", "CZ_BROADCAST") }

// CZFriendsListAdd is upstream's CZ_REQ_MAKE_FRIEND.
//
// Upstream registers the packet numerically, so the value is the constant
// upstream's own table holds.
func (o Opcodes) CZFriendsListAdd() uint16 { return 0x0202 }

// SizeCZWantToConnection is the wire length the source registers for the map
// entry packet.
func (o Opcodes) SizeCZWantToConnection() int {
	return o.set.MustPacketLength("protocol", o.CZWantToConnection())
}
