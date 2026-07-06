// Package service is the Valkey-backed implementation of the registry
// port (D22). It stores per-character location records and arbitrates
// the dual-zone write lock used during transit handshakes.
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/features/registry/domain"
)

// Valkey key patterns. Keep them in one place so the cluster layout is
// auditable in a single read.
const (
	// hashCharLocationPrefix is the prefix for per-character location
	// hash maps. Full key: "char:location:<charID>". Fields: zone_id,
	// map_name, account_id, last_seen (RFC3339Nano).
	hashCharLocationPrefix = "char:location:"

	// setZoneCharsPrefix is the prefix for per-zone membership sets.
	// Full key: "zone:chars:<zoneID>". Members: charID strings.
	setZoneCharsPrefix = "zone:chars:"

	// stringCharZoneLockPrefix is the prefix for the per-character
	// write-lock string. Full key: "lock:char:<charID>". Value: the
	// holding zoneID.
	stringCharZoneLockPrefix = "lock:char:"
)

// charLocationKey returns the Valkey key for a character's location
// hash. The key embeds only the character id so cross-zone lookups do
// not require knowing the source zone.
func charLocationKey(charID uint32) string {
	return hashCharLocationPrefix + strconv.FormatUint(uint64(charID), 10)
}

// zoneCharsKey returns the Valkey key for a zone's membership set.
func zoneCharsKey(zoneID string) string {
	return setZoneCharsPrefix + zoneID
}

// charZoneLockKey returns the Valkey key for a character's write lock.
func charZoneLockKey(charID uint32) string {
	return stringCharZoneLockPrefix + strconv.FormatUint(uint64(charID), 10)
}

// locationToFields serialises a CharacterLocation into the hash field
// map. The last_seen field uses RFC3339Nano so callers in different
// timezones round-trip without precision loss.
func locationToFields(loc domain.CharacterLocation) map[string]string {
	return map[string]string{
		"zone_id":    loc.ZoneID,
		"map_name":   loc.MapName,
		"account_id": strconv.FormatUint(uint64(loc.AccountID), 10),
		"last_seen":  loc.LastSeen.UTC().Format(time.RFC3339Nano),
	}
}

// fieldsToLocation parses a hash field map back into a
// CharacterLocation. Returns an error if account_id or last_seen are
// malformed — those are infrastructure bugs, not user-visible states.
func fieldsToLocation(charID uint32, fields map[string]string) (domain.CharacterLocation, error) {
	accountID, err := strconv.ParseUint(fields["account_id"], 10, 32)
	if err != nil {
		return domain.CharacterLocation{}, fmt.Errorf("parse account_id %q: %w", fields["account_id"], err)
	}
	lastSeen, err := time.Parse(time.RFC3339Nano, fields["last_seen"])
	if err != nil {
		return domain.CharacterLocation{}, fmt.Errorf("parse last_seen %q: %w", fields["last_seen"], err)
	}
	return domain.CharacterLocation{
		CharID:    charID,
		AccountID: uint32(accountID),
		ZoneID:    fields["zone_id"],
		MapName:   fields["map_name"],
		LastSeen:  lastSeen,
	}, nil
}

// valkeyStore adapts a valkey-go client to the narrow domain.Store
// port. It exists in the service package (not infrastructure) because
// it is part of the registry feature's outbound adapter — the same
// composition root that wires the registry also wires this adapter.
type valkeyStore struct {
	client valkeygo.Client
}

// NewValkeyStore returns a domain.Store backed by the given valkey
// client. The caller owns the client and is responsible for closing it.
func NewValkeyStore(client valkeygo.Client) domain.Store {
	return &valkeyStore{client: client}
}

func (s *valkeyStore) HSet(ctx context.Context, key string, fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	cmd := s.client.B().Hset().Key(key).FieldValue()
	for k, v := range fields {
		cmd = cmd.FieldValue(k, v)
	}
	if err := s.client.Do(ctx, cmd.Build()).Error(); err != nil {
		return fmt.Errorf("valkey hset %s: %w", key, err)
	}
	return nil
}

func (s *valkeyStore) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	resp := s.client.Do(ctx, s.client.B().Hgetall().Key(key).Build())
	if err := resp.Error(); err != nil {
		if valkeygo.IsValkeyNil(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("valkey hgetall %s: %w", key, err)
	}
	pairs, err := resp.AsStrMap()
	if err != nil {
		if valkeygo.IsValkeyNil(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("valkey hgetall %s decode: %w", key, err)
	}
	return pairs, nil
}

func (s *valkeyStore) HGetAllMulti(ctx context.Context, keys []string) (map[string]map[string]string, error) {
	if len(keys) == 0 {
		return map[string]map[string]string{}, nil
	}
	commands := make([]valkeygo.Completed, len(keys))
	for i, key := range keys {
		commands[i] = s.client.B().Hgetall().Key(key).Build()
	}
	results := s.client.DoMulti(ctx, commands...)

	out := make(map[string]map[string]string, len(keys))
	for i, resp := range results {
		key := keys[i]
		if err := resp.Error(); err != nil {
			if valkeygo.IsValkeyNil(err) {
				out[key] = map[string]string{}
				continue
			}
			return nil, fmt.Errorf("valkey hgetallmulti %s: %w", key, err)
		}
		pairs, err := resp.AsStrMap()
		if err != nil {
			if valkeygo.IsValkeyNil(err) {
				out[key] = map[string]string{}
				continue
			}
			return nil, fmt.Errorf("valkey hgetallmulti %s decode: %w", key, err)
		}
		if pairs == nil {
			pairs = map[string]string{}
		}
		out[key] = pairs
	}
	return out, nil
}

func (s *valkeyStore) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	cmd := s.client.B().Del().Key(keys...)
	if err := s.client.Do(ctx, cmd.Build()).Error(); err != nil {
		return fmt.Errorf("valkey del %v: %w", keys, err)
	}
	return nil
}

func (s *valkeyStore) SAdd(ctx context.Context, key string, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	if err := s.client.Do(ctx, s.client.B().Sadd().Key(key).Member(members...).Build()).Error(); err != nil {
		return fmt.Errorf("valkey sadd %s: %w", key, err)
	}
	return nil
}

func (s *valkeyStore) SRem(ctx context.Context, key string, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	if err := s.client.Do(ctx, s.client.B().Srem().Key(key).Member(members...).Build()).Error(); err != nil {
		return fmt.Errorf("valkey srem %s: %w", key, err)
	}
	return nil
}

func (s *valkeyStore) SMembers(ctx context.Context, key string) ([]string, error) {
	resp := s.client.Do(ctx, s.client.B().Smembers().Key(key).Build())
	if err := resp.Error(); err != nil {
		if valkeygo.IsValkeyNil(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("valkey smembers %s: %w", key, err)
	}
	members, err := resp.AsStrSlice()
	if err != nil {
		if valkeygo.IsValkeyNil(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("valkey smembers %s decode: %w", key, err)
	}
	if members == nil {
		members = []string{}
	}
	return members, nil
}

func (s *valkeyStore) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	var cmd valkeygo.Completed
	if ttl > 0 {
		cmd = s.client.B().Set().Key(key).Value(value).Nx().Px(ttl).Build()
	} else {
		cmd = s.client.B().Set().Key(key).Value(value).Nx().Build()
	}
	resp := s.client.Do(ctx, cmd)
	if err := resp.Error(); err != nil {
		if valkeygo.IsValkeyNil(err) {
			return false, nil
		}
		return false, fmt.Errorf("valkey setnx %s: %w", key, err)
	}
	return true, nil
}

// zoneLockReleaseScript implements compare-and-delete for the per-char
// zone lock. Mirrors the warehouse lock pattern (warehouse.go:25).
var zoneLockReleaseScript = "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end"

func (s *valkeyStore) DelIfEqual(ctx context.Context, key, expected string) error {
	resp := s.client.Do(ctx, s.client.B().
		Eval().
		Script(zoneLockReleaseScript).
		Numkeys(1).
		Key(key).
		Arg(expected).
		Build())
	if err := resp.Error(); err != nil {
		if valkeygo.IsValkeyNil(err) {
			return nil
		}
		return fmt.Errorf("valkey delifequal %s: %w", key, err)
	}
	return nil
}

// registry is the service-layer Registry implementation. It is
// goroutine-safe: every operation is independent and delegates its
// concurrency story to the underlying Store (valkey-go client is
// itself goroutine-safe).
type registry struct {
	store domain.Store
	now   func() time.Time
}

// Option mutates a registry during construction.
type Option func(*registry)

// WithClock overrides the time source used for LastSeen stamps.
// Intended for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(r *registry) { r.now = now }
}

// NewRegistry returns a Registry backed by the given Store. Pass the
// valkey-backed adapter (NewValkeyStore) in production; pass a mock
// Store in unit tests.
func NewRegistry(store domain.Store, opts ...Option) domain.Registry {
	r := &registry{
		store: store,
		now:   time.Now,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *registry) RegisterCharacter(ctx context.Context, loc domain.CharacterLocation) error {
	if loc.ZoneID == "" {
		return fmt.Errorf("registry: register character %d: zone_id is empty", loc.CharID)
	}
	loc.LastSeen = r.now()
	charIDStr := strconv.FormatUint(uint64(loc.CharID), 10)
	if err := r.store.HSet(ctx, charLocationKey(loc.CharID), locationToFields(loc)); err != nil {
		return fmt.Errorf("registry: register character %d: %w", loc.CharID, err)
	}
	if err := r.store.SAdd(ctx, zoneCharsKey(loc.ZoneID), charIDStr); err != nil {
		return fmt.Errorf("registry: register character %d on zone %s: %w", loc.CharID, loc.ZoneID, err)
	}
	return nil
}

func (r *registry) UnregisterCharacter(ctx context.Context, charID uint32) error {
	loc, err := r.lookupLocation(ctx, charID)
	if err != nil {
		if errors.Is(err, domain.ErrCharacterNotFound) {
			return nil
		}
		return err
	}
	charIDStr := strconv.FormatUint(uint64(charID), 10)
	if err := r.store.Del(ctx, charLocationKey(charID)); err != nil {
		return fmt.Errorf("registry: unregister character %d: %w", charID, err)
	}
	if err := r.store.SRem(ctx, zoneCharsKey(loc.ZoneID), charIDStr); err != nil {
		return fmt.Errorf("registry: unregister character %d from zone %s: %w", charID, loc.ZoneID, err)
	}
	return nil
}

func (r *registry) GetCharacterLocation(ctx context.Context, charID uint32) (*domain.CharacterLocation, error) {
	loc, err := r.lookupLocation(ctx, charID)
	if err != nil {
		return nil, err
	}
	return &loc, nil
}

func (r *registry) lookupLocation(ctx context.Context, charID uint32) (domain.CharacterLocation, error) {
	fields, err := r.store.HGetAll(ctx, charLocationKey(charID))
	if err != nil {
		return domain.CharacterLocation{}, fmt.Errorf("registry: lookup character %d: %w", charID, err)
	}
	if len(fields) == 0 {
		return domain.CharacterLocation{}, fmt.Errorf("registry: lookup character %d: %w", charID, domain.ErrCharacterNotFound)
	}
	loc, err := fieldsToLocation(charID, fields)
	if err != nil {
		return domain.CharacterLocation{}, fmt.Errorf("registry: decode character %d: %w", charID, err)
	}
	return loc, nil
}

func (r *registry) ListCharactersOnZone(ctx context.Context, zoneID string) ([]domain.CharacterLocation, error) {
	members, err := r.store.SMembers(ctx, zoneCharsKey(zoneID))
	if err != nil {
		return nil, fmt.Errorf("registry: list zone %s: %w", zoneID, err)
	}
	if len(members) == 0 {
		return []domain.CharacterLocation{}, nil
	}

	charIDs := make([]uint32, 0, len(members))
	keys := make([]string, 0, len(members))
	for _, m := range members {
		charID, err := strconv.ParseUint(m, 10, 32)
		if err != nil {
			continue
		}
		charIDs = append(charIDs, uint32(charID))
		keys = append(keys, charLocationKey(uint32(charID)))
	}

	batch, err := r.store.HGetAllMulti(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("registry: batch lookup zone %s: %w", zoneID, err)
	}

	out := make([]domain.CharacterLocation, 0, len(charIDs))
	for i, charID := range charIDs {
		fields := batch[keys[i]]
		if len(fields) == 0 {
			continue
		}
		loc, err := fieldsToLocation(charID, fields)
		if err != nil {
			return nil, fmt.Errorf("registry: decode character %d: %w", charID, err)
		}
		out = append(out, loc)
	}
	return out, nil
}

func (r *registry) AcquireZoneLock(ctx context.Context, charID uint32, zoneID string, ttl time.Duration) error {
	if zoneID == "" {
		return fmt.Errorf("registry: acquire zone lock for character %d: zone_id is empty", charID)
	}
	ok, err := r.store.SetNX(ctx, charZoneLockKey(charID), zoneID, ttl)
	if err != nil {
		return fmt.Errorf("registry: acquire zone lock for character %d: %w", charID, err)
	}
	if !ok {
		return fmt.Errorf("registry: acquire zone lock for character %d: %w", charID, domain.ErrLockHeld)
	}
	return nil
}

func (r *registry) ReleaseZoneLock(ctx context.Context, charID uint32, zoneID string) error {
	if zoneID == "" {
		return fmt.Errorf("registry: release zone lock for character %d: zone_id is empty", charID)
	}
	if err := r.store.DelIfEqual(ctx, charZoneLockKey(charID), zoneID); err != nil {
		return fmt.Errorf("registry: release zone lock for character %d: %w", charID, err)
	}
	return nil
}
