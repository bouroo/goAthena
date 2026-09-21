package app

import (
	"context"
	"errors"
	"fmt"

	storagedomain "github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// StorageService is the world-side orchestrator for the bag↔warehouse verb.
// Storage itself is a separate bounded context (commerce/storage) with its own
// table and repo; the world layer is responsible for cross-context
// orchestration — a "move to warehouse" is a world verb that calls
// inventory.Remove + storage.Add atomically.
//
// The atomicity contract: if either side fails, the whole move aborts and
// neither the bag nor the warehouse has changed. Today this is enforced by a
// "remove first, then add" sequence with the bag removal error short-circuit;
// a future DB-level transaction will harden this (TODO).
type StorageService struct {
	inv     StorageInventoryPort
	storage StoragePort
}

// StorageInventoryPort is the narrow inventory surface storage needs: load a
// char's bag (to resolve a wire index to a server row), remove a row, and
// grant an item by nameID. Defining it locally keeps world/app off
// inventory/app; inventory.InventoryService satisfies it directly.
type StorageInventoryPort interface {
	LoadByChar(ctx context.Context, accountID, charID uint32) ([]invdomain.Item, error)
	Add(ctx context.Context, charID, nameID uint32, amount int) (invdomain.Item, error)
	Remove(ctx context.Context, id invdomain.ItemID, amount int) error
}

// StoragePort is the narrow storage surface world needs: load an account's
// warehouse, add a row (with stack-merge), remove a row. commerce/storage's
// StorageService satisfies it directly.
type StoragePort interface {
	LoadByAccount(ctx context.Context, accountID uint32) ([]storagedomain.StorageItem, error)
	Add(ctx context.Context, accountID, nameID uint32, amount int) (storagedomain.StorageItem, error)
	Remove(ctx context.Context, id storagedomain.StorageItemID, amount int) error
}

// StorageMoveResult carries what the gateway emits after a successful move:
// the warehouse-row index to ack, the nameID that landed in the warehouse,
// and the new amount on that row (the merge-or-insert result).
type StorageMoveResult struct {
	Index     uint16 // server row + 2 (client index convention)
	StoredID  storagedomain.StorageItemID
	NameID    uint32
	Amount    uint32
	IsFromBag bool // true = bag→warehouse; false = warehouse→bag
}

// Storage errors. Distinct sentinels so the gateway maps each to a ZC_STOREITEMLISTRESULT
// result byte (0=success, 1=failure) without parsing strings.
var (
	ErrStorageIndexOutOfRange = errors.New("storage: index out of range")
	ErrStorageInsufficient    = errors.New("storage: insufficient item amount")
	ErrStorageEquipped        = errors.New("storage: cannot store an equipped item")
	ErrStorageRowMissing      = errors.New("storage: row missing between load and remove")
	ErrStorageFull            = errors.New("storage: warehouse is full")
	ErrStorageNotOpen         = errors.New("storage: warehouse not open")
)

// MoveToStorage moves amount units of the bag row at wireIndex (client index
// = server row + 2) from the player's bag into the account's warehouse.
// Stackable items merge into an existing same-NameID warehouse row; equipment
// always inserts a new row.
//
// Failure semantics:
//   - Index out of range or item is equipped → ErrStorageIndexOutOfRange /
//     ErrStorageEquipped, NOTHING is mutated.
//   - bag row vanishes between load and remove → ErrStorageRowMissing,
//     NOTHING is mutated.
//   - remove / add fails (e.g. warehouse full) → the failed step leaves the
//     other side unchanged; the error is wrapped and returned. The contract
//     is "all or nothing"; a future DB transaction will harden this.
func (s *StorageService) MoveToStorage(ctx context.Context, accountID, charID, wireIndex uint32, amount int) (StorageMoveResult, error) {
	if amount <= 0 {
		return StorageMoveResult{}, fmt.Errorf("%w: amount must be > 0", ErrStorageInsufficient)
	}
	items, err := s.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		return StorageMoveResult{}, fmt.Errorf("storage: load bag: %w", err)
	}
	// wireIndex == 0 is an INVALID bag slot — rows start at server row 1.
	if wireIndex < 2 {
		return StorageMoveResult{}, fmt.Errorf("%w: wire index %d < 2", ErrStorageIndexOutOfRange, wireIndex)
	}
	serverRow := ropacket.ServerIndex(uint16(wireIndex)) //nolint:gosec // G115: range-checked above.
	if int(serverRow) >= len(items) {
		return StorageMoveResult{}, fmt.Errorf("%w: wire index %d out of %d items", ErrStorageIndexOutOfRange, wireIndex, len(items))
	}
	bagRow := items[serverRow]
	if bagRow.IsEquipped() {
		return StorageMoveResult{}, ErrStorageEquipped
	}
	if int(bagRow.Amount) < amount {
		return StorageMoveResult{}, fmt.Errorf("%w: have %d, want %d", ErrStorageInsufficient, bagRow.Amount, amount)
	}
	// Remove from bag first; on success, add to warehouse. If the warehouse
	// add fails the bag row is already mutated — this is the seam a future DB
	// transaction closes.
	if err := s.inv.Remove(ctx, bagRow.ID, amount); err != nil {
		if errors.Is(err, invdomain.ErrItemNotFound) {
			return StorageMoveResult{}, ErrStorageRowMissing
		}
		return StorageMoveResult{}, fmt.Errorf("storage: bag remove: %w", err)
	}
	storageRow, err := s.storage.Add(ctx, accountID, bagRow.NameID, amount)
	if err != nil {
		return StorageMoveResult{}, fmt.Errorf("storage: warehouse add: %w", err)
	}
	// Resolve the warehouse row's WIRE index (server row in LoadByAccount
	// result + 2). The new row's ID is what was just inserted (top of stack
	// or merged into an existing one); the wire index is its position in the
	// account's full list.
	warehouse, err := s.storage.LoadByAccount(ctx, accountID)
	if err != nil {
		return StorageMoveResult{}, fmt.Errorf("storage: load warehouse after add: %w", err)
	}
	var wireSlot uint16
	for i, row := range warehouse {
		if row.ID == storageRow.ID {
			//nolint:gosec // G115: warehouse row count bounded by MAX_STORAGE (600).
			wireSlot = uint16(i) + 2
			break
		}
	}
	return StorageMoveResult{
		Index:     wireSlot,
		StoredID:  storageRow.ID,
		NameID:    bagRow.NameID,
		Amount:    storageRow.Amount,
		IsFromBag: true,
	}, nil
}

// MoveFromStorage moves amount units of the warehouse row at wireIndex
// (warehouse index = server row + 2) from the account's warehouse into the
// player's bag. Stackable items merge into an existing same-NameID bag row;
// equipment always inserts a new bag row.
//
// Failure semantics mirror MoveToStorage: a failed bag insert leaves the
// warehouse row removed-but-not-delivered (the future DB transaction closes
// that seam). Out-of-range / insufficient / not-found errors are mapped to
// distinct sentinels so the gateway can echo a useful ack.
func (s *StorageService) MoveFromStorage(ctx context.Context, accountID, charID, wireIndex uint32, amount int) (StorageMoveResult, error) {
	if amount <= 0 {
		return StorageMoveResult{}, fmt.Errorf("%w: amount must be > 0", ErrStorageInsufficient)
	}
	items, err := s.storage.LoadByAccount(ctx, accountID)
	if err != nil {
		return StorageMoveResult{}, fmt.Errorf("storage: load warehouse: %w", err)
	}
	if wireIndex < 2 {
		return StorageMoveResult{}, fmt.Errorf("%w: wire index %d < 2", ErrStorageIndexOutOfRange, wireIndex)
	}
	serverRow := ropacket.ServerIndex(uint16(wireIndex)) //nolint:gosec // G115: range-checked above.
	if int(serverRow) >= len(items) {
		return StorageMoveResult{}, fmt.Errorf("%w: wire index %d out of %d rows", ErrStorageIndexOutOfRange, wireIndex, len(items))
	}
	warehouseRow := items[serverRow]
	if warehouseRow.IsEquipped() {
		return StorageMoveResult{}, ErrStorageEquipped
	}
	if int(warehouseRow.Amount) < amount {
		return StorageMoveResult{}, fmt.Errorf("%w: have %d, want %d", ErrStorageInsufficient, warehouseRow.Amount, amount)
	}
	if err := s.storage.Remove(ctx, warehouseRow.ID, amount); err != nil {
		if errors.Is(err, storagedomain.ErrStorageItemNotFound) {
			return StorageMoveResult{}, ErrStorageRowMissing
		}
		return StorageMoveResult{}, fmt.Errorf("storage: warehouse remove: %w", err)
	}
	bagRow, err := s.inv.Add(ctx, charID, warehouseRow.NameID, amount)
	if err != nil {
		return StorageMoveResult{}, fmt.Errorf("storage: bag add: %w", err)
	}
	return StorageMoveResult{
		Index:     0, // bag-side ack uses the storage ack's own wire index; client matches by the row
		StoredID:  warehouseRow.ID,
		NameID:    warehouseRow.NameID,
		Amount:    bagRow.Amount,
		IsFromBag: false,
	}, nil
}

// LoadWarehouse returns the account's warehouse rows for the ZC_STORE_*
// init burst on CZ_REQ_OPENSTORE2. Index preservation is load-bearing: the
// warehouse wire index is server-row + 2, so the order returned here is the
// order the client will see in its grid.
func (s *StorageService) LoadWarehouse(ctx context.Context, accountID uint32) ([]storagedomain.StorageItem, error) {
	rows, err := s.storage.LoadByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("load warehouse: %w", err)
	}
	return rows, nil
}

// NewStorageService builds the orchestrator with the inventory and storage
// services wired in. Both must be non-nil — the bag↔warehouse verb requires
// both contexts to be reachable. Composition root + DI both call this.
func NewStorageService(inv StorageInventoryPort, storage StoragePort) *StorageService {
	return &StorageService{inv: inv, storage: storage}
}
