package domain

import (
	"time"
)

// StorageItem represents an item in character storage.
// One row of the rAthena `storage` table with fields matching the SQL schema.
type StorageItem struct {
	ID        uint64    // `id` — primary key
	AccountID uint32    // `account_id` — owning account (storage is account-scoped per rAthena)
	NameID    uint32    // `nameid` — item_db.yml id
	Amount    int32     // `amount` — stack count (must be positive)
	Identify  byte      // `identify` — 1 = identified, 0 = unidentified
	Refine    byte      // `refine` — refine level 0-10
	Attribute byte      // `attribute` — elemental attribute
	Card0     uint16    // `card0` — card slot 0
	Card1     uint16    // `card1` — card slot 1
	Card2     uint16    // `card2` — card slot 2
	Card3     uint16    // `card3` — card slot 3
	CreatedAt time.Time // `created_at`
	UpdatedAt time.Time // `updated_at`
}
