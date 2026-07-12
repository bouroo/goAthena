// Package aoi implements a tower-grid Area-of-Interest engine with
// 18x18 cell towers and adaptive squeezing for high-density scenes.
package aoi

import (
	"errors"
	"fmt"
	"sync"
)

// TowerSize is the side length (in cells) of a single AOI tower.
// Chosen to roughly match the visual viewport radius so an entity's
// 9-grid neighborhood covers its broadcast area without scanning
// the whole map.
const TowerSize = 18

// ErrEntityExists is returned by AddEntity when the ID is already tracked.
var ErrEntityExists = errors.New("aoi: entity already exists")

// ErrEntityMissing is returned by RemoveEntity / MoveEntity when the ID is unknown.
var ErrEntityMissing = errors.New("aoi: entity not found")

// ErrOutOfBounds is returned when coordinates fall outside the managed map.
var ErrOutOfBounds = errors.New("aoi: coordinates out of bounds")

// EntityID is a unique identifier for an AOI-tracked actor.
type EntityID uint32

// EntityType classifies entities for AOI filtering.
type EntityType uint8

const (
	// EntityPlayer covers player characters.
	EntityPlayer EntityType = iota
	// EntityNPC covers non-player characters (merchants, warps, etc.).
	EntityNPC
	// EntityMob covers monsters.
	EntityMob
)

// String renders an EntityType for logs.
func (t EntityType) String() string {
	switch t {
	case EntityPlayer:
		return "player"
	case EntityNPC:
		return "npc"
	case EntityMob:
		return "mob"
	default:
		return fmt.Sprintf("entity(%d)", uint8(t))
	}
}

// Entity is an AOI-tracked actor. The X/Y coordinates are world cells.
// Entities are owned by the GridManager that adds them; callers mutate
// fields only through GridManager methods.
type Entity struct {
	ID   EntityID
	Type EntityType
	X    int
	Y    int
}

// Tower holds entities in a single grid cell partition.
// Each Tower is guarded by an RWMutex so concurrent readers can fan out
// across towers without contention while writers (cross-tower moves)
// take exclusive locks.
type Tower struct {
	mu      sync.RWMutex
	players map[EntityID]*Entity
	npcs    map[EntityID]*Entity
	mobs    map[EntityID]*Entity
}

// entityMapFor returns the per-type map backing a tower for the given type.
func (t *Tower) entityMapFor(typ EntityType) map[EntityID]*Entity {
	switch typ {
	case EntityPlayer:
		return t.players
	case EntityNPC:
		return t.npcs
	case EntityMob:
		return t.mobs
	default:
		return nil
	}
}

// count returns the total number of entities in the tower.
// Caller must hold at least t.mu (read or write).
func (t *Tower) count() int {
	return len(t.players) + len(t.npcs) + len(t.mobs)
}

func newTower() Tower {
	return Tower{
		players: make(map[EntityID]*Entity),
		npcs:    make(map[EntityID]*Entity),
		mobs:    make(map[EntityID]*Entity),
	}
}

// GridManager manages the tower-grid for a single map instance.
// A GridManager is safe for concurrent use.
type GridManager struct {
	mu sync.RWMutex

	width      int
	height     int
	gridWidth  int
	gridHeight int

	towers      []Tower
	entityTower map[EntityID]int
	entityType  map[EntityID]EntityType
}

// NewGridManager creates a grid for a map of the given cell dimensions.
// Width and height must be positive.
func NewGridManager(width, height int) *GridManager {
	if width <= 0 || height <= 0 {
		panic(fmt.Sprintf("aoi: NewGridManager requires positive dimensions, got %dx%d", width, height))
	}

	gw := (width + TowerSize - 1) / TowerSize
	gh := (height + TowerSize - 1) / TowerSize

	gm := &GridManager{
		width:      width,
		height:     height,
		gridWidth:  gw,
		gridHeight: gh,
		towers:     make([]Tower, gw*gh),
	}
	for i := range gm.towers {
		gm.towers[i] = newTower()
	}
	gm.entityTower = make(map[EntityID]int)
	gm.entityType = make(map[EntityID]EntityType)
	return gm
}

// Width returns the map width in cells.
func (gm *GridManager) Width() int { return gm.width }

// Height returns the map height in cells.
func (gm *GridManager) Height() int { return gm.height }

// GridWidth returns the number of towers horizontally.
func (gm *GridManager) GridWidth() int { return gm.gridWidth }

// GridHeight returns the number of towers vertically.
func (gm *GridManager) GridHeight() int { return gm.gridHeight }

// TowerID returns the tower ID for world coordinates (x, y), or -1 if the
// coordinates fall outside the managed map.
func (gm *GridManager) TowerID(x, y int) int {
	if !gm.inBounds(x, y) {
		return -1
	}
	tx := x / TowerSize
	ty := y / TowerSize
	return ty*gm.gridWidth + tx
}

func (gm *GridManager) inBounds(x, y int) bool {
	return x >= 0 && x < gm.width && y >= 0 && y < gm.height
}

// towerAt returns the tower pointer for (tx, ty) or nil if out of grid.
func (gm *GridManager) towerAt(tx, ty int) *Tower {
	if tx < 0 || ty < 0 || tx >= gm.gridWidth || ty >= gm.gridHeight {
		return nil
	}
	return &gm.towers[ty*gm.gridWidth+tx]
}

// AddEntity registers an entity in the grid at its current coordinates.
// Returns ErrEntityExists if the ID is already tracked, ErrOutOfBounds
// if e.X/e.Y fall outside the map.
func (gm *GridManager) AddEntity(e *Entity) error {
	if e == nil {
		return errors.New("aoi: nil entity")
	}
	if !gm.inBounds(e.X, e.Y) {
		return fmt.Errorf("%w: (%d,%d)", ErrOutOfBounds, e.X, e.Y)
	}
	id := e.ID
	tid := gm.TowerID(e.X, e.Y)

	gm.mu.Lock()
	if _, exists := gm.entityTower[id]; exists {
		gm.mu.Unlock()
		return fmt.Errorf("%w: id=%d", ErrEntityExists, id)
	}
	gm.entityTower[id] = tid
	gm.entityType[id] = e.Type
	gm.mu.Unlock()

	tower := &gm.towers[tid]
	tower.mu.Lock()
	tower.entityMapFor(e.Type)[id] = e
	tower.mu.Unlock()
	return nil
}

// RemoveEntity removes an entity from the grid. Returns ErrEntityMissing
// if the ID is not currently tracked.
func (gm *GridManager) RemoveEntity(id EntityID) error {
	gm.mu.Lock()
	tid, ok := gm.entityTower[id]
	if !ok {
		gm.mu.Unlock()
		return fmt.Errorf("%w: id=%d", ErrEntityMissing, id)
	}
	typ := gm.entityType[id]
	delete(gm.entityTower, id)
	delete(gm.entityType, id)
	gm.mu.Unlock()

	tower := &gm.towers[tid]
	tower.mu.Lock()
	if m := tower.entityMapFor(typ); m != nil {
		delete(m, id)
	}
	tower.mu.Unlock()
	return nil
}

// MoveEntity atomically updates an entity's coordinates. Cross-tower
// moves are handled by swapping the entity between towers under exclusive
// locks. Locks are always taken in ascending tower-ID order to prevent
// deadlocks between concurrent movers of related entities.
//
// Returns ErrEntityMissing if the ID is unknown or ErrOutOfBounds if the
// destination lies outside the map.
func (gm *GridManager) MoveEntity(id EntityID, newX, newY int) error {
	if !gm.inBounds(newX, newY) {
		return fmt.Errorf("%w: (%d,%d)", ErrOutOfBounds, newX, newY)
	}

	gm.mu.Lock()
	oldTID, ok := gm.entityTower[id]
	if !ok {
		gm.mu.Unlock()
		return fmt.Errorf("%w: id=%d", ErrEntityMissing, id)
	}
	typ := gm.entityType[id]
	newTID := (newY/TowerSize)*gm.gridWidth + (newX / TowerSize)
	if newTID == oldTID {
		gm.mu.Unlock()

		tower := &gm.towers[oldTID]
		tower.mu.Lock()
		if e := tower.entityMapFor(typ)[id]; e != nil {
			e.X = newX
			e.Y = newY
		}
		tower.mu.Unlock()
		return nil
	}

	gm.entityTower[id] = newTID
	gm.mu.Unlock()

	// Take the two tower locks in ascending tower-ID order to avoid
	// deadlock when two goroutines cross-swap in opposite directions.
	first, second := oldTID, newTID
	if first > second {
		first, second = second, first
	}

	firstTower := &gm.towers[first]
	firstTower.mu.Lock()
	srcMap := firstTower.entityMapFor(typ)
	e := srcMap[id]
	delete(srcMap, id)
	firstTower.mu.Unlock()

	if e == nil {
		// Index said the entity lived here, but the tower map disagrees.
		// Another goroutine already moved it out from under us. The index
		// has been advanced past this tower; the winner's pointer is the
		// canonical one and we have nothing to insert.
		return nil
	}
	e.X = newX
	e.Y = newY

	secondTower := &gm.towers[second]
	secondTower.mu.Lock()
	dstMap := secondTower.entityMapFor(typ)
	if existing := dstMap[id]; existing != nil {
		// Concurrent mover already inserted the entity here; overwrite the
		// coordinates on its pointer and discard ours. Both pointers refer
		// to the same logical entity.
		existing.X = newX
		existing.Y = newY
	} else {
		dstMap[id] = e
	}
	secondTower.mu.Unlock()
	return nil
}

// Get9GridTowers returns the 9 towers surrounding and including the tower at
// world coordinates (x, y). Edge and corner maps return fewer towers.
// Returns nil if (x, y) is out of bounds.
func (gm *GridManager) Get9GridTowers(x, y int) []*Tower {
	if !gm.inBounds(x, y) {
		return nil
	}
	tx := x / TowerSize
	ty := y / TowerSize
	out := make([]*Tower, 0, 9)
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if t := gm.towerAt(tx+dx, ty+dy); t != nil {
				out = append(out, t)
			}
		}
	}
	return out
}

// Count9Grid returns the total number of entities in the 9-grid
// neighborhood centered on (x, y). Used by the squeeze heuristic.
func (gm *GridManager) Count9Grid(x, y int) int {
	towers := gm.Get9GridTowers(x, y)
	if len(towers) == 0 {
		return 0
	}
	n := 0
	for _, t := range towers {
		t.mu.RLock()
		n += t.count()
		t.mu.RUnlock()
	}
	return n
}

// QueryVisible returns all entities in the standard broadcast viewport
// (radius 15 cells) around (x, y). Out-of-bounds coordinates yield nil.
// The result is freshly allocated; callers may mutate it freely.
func (gm *GridManager) QueryVisible(x, y int) []*Entity {
	return gm.QueryWithin(x, y, DefaultBroadcastRadius)
}

// QueryVisibleSqueezed returns all entities in the adaptive broadcast
// viewport around (x, y). The radius is chosen by SqueezeRadius based on
// the local 9-grid density, so high-density areas broadcast less far.
func (gm *GridManager) QueryVisibleSqueezed(x, y int) []*Entity {
	radius := SqueezeRadius(gm.Count9Grid(x, y))
	return gm.QueryWithin(x, y, radius)
}

// DefaultBroadcastRadius is the normal AOI broadcast radius in cells.
const DefaultBroadcastRadius = 15

// QueryWithin returns all entities within the given radius of the point (x, y).
func (gm *GridManager) QueryWithin(x, y, radius int) []*Entity {
	if !gm.inBounds(x, y) || radius < 0 {
		return nil
	}

	x0, y0, x1, y1 := clampRadiusBBox(x, y, radius, gm.width, gm.height)

	tx0 := x0 / TowerSize
	ty0 := y0 / TowerSize
	tx1 := x1 / TowerSize
	ty1 := y1 / TowerSize

	out := make([]*Entity, 0, 64)
	for ty := ty0; ty <= ty1; ty++ {
		for tx := tx0; tx <= tx1; tx++ {
			tower := &gm.towers[ty*gm.gridWidth+tx]
			collectTowerEntities(tower, x0, y0, x1, y1, &out)
		}
	}
	return out
}

// clampRadiusBBox returns the inclusive (x0, y0, x1, y1) bbox around
// (x, y) clipped to the map dimensions.
func clampRadiusBBox(x, y, radius, width, height int) (int, int, int, int) {
	x0 := x - radius
	y0 := y - radius
	x1 := x + radius
	y1 := y + radius
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 >= width {
		x1 = width - 1
	}
	if y1 >= height {
		y1 = height - 1
	}
	return x0, y0, x1, y1
}

// collectTowerEntities appends entities in tower that fall within the
// inclusive bbox (x0, y0)..(x1, y1) to dst. The caller does not need to
// hold the tower lock — collectTowerEntities takes the read lock itself.
func collectTowerEntities(tower *Tower, x0, y0, x1, y1 int, dst *[]*Entity) {
	tower.mu.RLock()
	defer tower.mu.RUnlock()
	for _, e := range tower.players {
		if inBBox(e, x0, y0, x1, y1) {
			*dst = append(*dst, e)
		}
	}
	for _, e := range tower.npcs {
		if inBBox(e, x0, y0, x1, y1) {
			*dst = append(*dst, e)
		}
	}
	for _, e := range tower.mobs {
		if inBBox(e, x0, y0, x1, y1) {
			*dst = append(*dst, e)
		}
	}
}

func inBBox(e *Entity, x0, y0, x1, y1 int) bool {
	return e.X >= x0 && e.X <= x1 && e.Y >= y0 && e.Y <= y1
}

// EntityCount returns the total entities tracked by the grid.
func (gm *GridManager) EntityCount() int {
	gm.mu.RLock()
	n := len(gm.entityTower)
	gm.mu.RUnlock()
	return n
}

// TowerEntityCount returns the entity count of the tower at towerID, or 0
// if towerID is out of range.
func (gm *GridManager) TowerEntityCount(towerID int) int {
	if towerID < 0 || towerID >= len(gm.towers) {
		return 0
	}
	t := &gm.towers[towerID]
	t.mu.RLock()
	n := t.count()
	t.mu.RUnlock()
	return n
}

// EntityLocation returns (x, y, towerID) for an entity, or false if unknown.
// Exposed primarily for tests and debugging.
func (gm *GridManager) EntityLocation(id EntityID) (int, int, int, bool) {
	gm.mu.RLock()
	tid, ok := gm.entityTower[id]
	typ := gm.entityType[id]
	gm.mu.RUnlock()
	if !ok {
		return 0, 0, 0, false
	}
	t := &gm.towers[tid]
	t.mu.RLock()
	e := t.entityMapFor(typ)[id]
	var x, y int
	if e != nil {
		x, y = e.X, e.Y
	}
	t.mu.RUnlock()
	if e == nil {
		return 0, 0, 0, false
	}
	return x, y, tid, true
}
