package service

// shopItem mirrors the on-wire shape of one entry in
// ZC_PC_PURCHASE_ITEMLIST (rathena/src/map/packets_struct.hpp ITEM_INFO,
// PACKETVER >= 20210203). 19 bytes on the wire:
//
//	uint32 itemId
//	uint32 price
//	uint32 discountPrice
//	uint8  itemType    // rAthena IT_* type
//	uint16 viewSprite  // sprite for equipment
//	uint32 location    // EQP_* bitmask for equipment
type shopItem struct {
	ItemID        uint32
	Price         uint32
	DiscountPrice uint32
	ItemType      uint8
	ViewSprite    uint16
	Location      uint32
}

// npcSpawn defines a static NPC entity to spawn on the map.
// GIDs start at 110000000 (rAthena START_NPC_NUM).
type npcSpawn struct {
	GID      uint32
	Name     string
	SpriteID int16
	X, Y     int16
	Dir      uint8

	// ShopType is the shop role: 0=dialog NPC, 1=shop NPC. Dialog
	// NPCs follow the M15 CZ_CONTACTNPC → ZC_SAY_DIALOG2 flow; shop
	// NPCs follow the M16 CZ_CONTACTNPC → ZC_SELECT_DEALTYPE flow and
	// carry a non-empty ShopItems list.
	ShopType uint8
	// ShopItems is the stock list for shop-type NPCs. Ignored when
	// ShopType is 0. The list is sent verbatim in
	// ZC_PC_PURCHASE_ITEMLIST when the player picks "Buy" in the
	// deal-type window.
	ShopItems []shopItem
}

// npcSpawns is the hardcoded NPC spawn list for the default map.
// These are sent during CZ_NOTIFY_ACTORINIT after the status burst
// and empty list packets, using ZC_SET_UNIT_IDLE (0x09ff).
//
// Sprite IDs are rAthena NPC class IDs (nd->class_):
//
//	114 = Kafra Employee (4_KAFRA)
//	104 = Weapon Shop (4_M_03)
//	 45 = Warp Portal (4_F_01)
//	111 = Guide (4_M_02)
//
// Shop item database IDs are rAthena item_db IDs (item_info):
//
//	501  = Red Potion   (healing, IT_HEALING=0)
//	502  = Orange Potion (healing, IT_HEALING=0)
//	1201 = Knife        (1-handed dagger, IT_WEAPON=3, EQP_HAND_R=2)
//	1101 = Short Sword  (1-handed sword, IT_WEAPON=3, EQP_HAND_R=2)
var npcSpawns = []npcSpawn{
	{
		GID:      110000001,
		Name:     "Kafra Employee",
		SpriteID: 114,
		X:        150,
		Y:        180,
		Dir:      0,
		// ShopType=0 (dialog NPC) — uses the M15 dialog flow.
	},
	{
		GID:      110000002,
		Name:     "Weapon Shop",
		SpriteID: 104,
		X:        160,
		Y:        180,
		Dir:      0,
		ShopType: 1,
		ShopItems: []shopItem{
			{ItemID: 501, Price: 50, DiscountPrice: 50, ItemType: 0, ViewSprite: 0, Location: 0},
			{ItemID: 502, Price: 200, DiscountPrice: 200, ItemType: 0, ViewSprite: 0, Location: 0},
			{ItemID: 1201, Price: 500, DiscountPrice: 500, ItemType: 3, ViewSprite: 1, Location: 2},
			{ItemID: 1101, Price: 1500, DiscountPrice: 1500, ItemType: 3, ViewSprite: 2, Location: 2},
		},
	},
	{
		GID:      110000003,
		Name:     "Warp",
		SpriteID: 45,
		X:        150,
		Y:        190,
		Dir:      0,
	},
	{
		GID:      110000004,
		Name:     "Guide",
		SpriteID: 111,
		X:        160,
		Y:        190,
		Dir:      0,
	},
}
