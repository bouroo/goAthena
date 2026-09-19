package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// HeaderZCDeleteItemFromBody is ZC_DELETE_ITEM_FROM_BODY (0x07fa): the
// server→client frame that tells the client an inventory row was deleted or
// decremented, so the bag grid re-syncs without waiting for the next list verb.
// Source: third_party/rathenaThailand src/map/packets.hpp:831
// (DEFINE_PACKET_HEADER(ZC_DELETE_ITEM_FROM_BODY, 0x7fa)).
const HeaderZCDeleteItemFromBody uint16 = 0x07fa

// sizeZCDeleteItemFromBody is the fixed on-wire length:
// int16 packetType + int16 deleteType + uint16 index + int16 count
// (packets.hpp:825-830, `struct PACKET_ZC_DELETE_ITEM_FROM_BODY`). The struct is
// not PACKETVER-guarded in this fork and clif_delitem sends sizeof(packet)
// (clif.cpp:2917-2924), so 8 is the length at every PACKETVER the fork compiles.
const sizeZCDeleteItemFromBody = 8

// DeleteTypeItemSold is the delete reason for a completed NPC sale — "Item
// sold", value 6 in the enumeration clif.cpp:2905-2914 documents next to
// clif_delitem. goAthena models no other reason yet: the values exist for other
// verbs (use / refine / material change / storage / cart / trade), and each gets
// a constant here only when a verb actually needs it.
const DeleteTypeItemSold int16 = 6

// DeleteItemFromBodyResponse encodes ZC_DELETE_ITEM_FROM_BODY (0x07fa, 8B).
//
// clif_delitem (clif.cpp:2915-2928) fills it as: packetType, deleteType = the
// caller's reason, index = client_index(index) — the 1-based-on-the-wire slot
// convention ropacket.ClientIndex encodes — and count = the amount removed.
// The frame is SELF-directed (clif_send(..., &sd, SELF)).
//
//	int16 packetType  (2) offset 0
//	int16 deleteType  (2) offset 2
//	uint16 index      (2) offset 4  client index of the row
//	int16 count       (2) offset 6  amount removed
//	                     ----
//	                     8
//
// count is int16 on the wire (not uint16, unlike ZC_ITEM_PICKUP_ACK's count),
// so the field is modelled signed to keep the byte layout a direct copy.
type DeleteItemFromBodyResponse struct {
	DeleteType int16
	Index      uint16
	Count      int16
}

// Size returns the fixed on-wire byte length of this packet.
func (r *DeleteItemFromBodyResponse) Size() int { return sizeZCDeleteItemFromBody }

// Encode writes ZC_DELETE_ITEM_FROM_BODY to w.
func (r *DeleteItemFromBodyResponse) Encode(w io.Writer) error {
	var buf [sizeZCDeleteItemFromBody]byte
	binary.LittleEndian.PutUint16(buf[0:2], HeaderZCDeleteItemFromBody)
	binary.LittleEndian.PutUint16(buf[2:4], uint16(r.DeleteType)) //nolint:gosec // bit-cast of a raw int16 wire field
	binary.LittleEndian.PutUint16(buf[4:6], r.Index)
	binary.LittleEndian.PutUint16(buf[6:8], uint16(r.Count)) //nolint:gosec // bit-cast of a raw int16 wire field
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_DELETE_ITEM_FROM_BODY: %w", err)
	}
	return nil
}
