//go:build unit

package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/bouroo/goAthena/internal/features/registry/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/registry/domain/mock"
	"github.com/bouroo/goAthena/internal/features/registry/service"
)

// fakeStore is a hand-rolled in-memory implementation of domain.Store.
// It backs the unit tests with no valkey dependency and is used to
// verify end-to-end semantics (key patterns, TTL handling, lock
// compare-and-delete) that the gomock-based tests assert call-by-call.
type fakeStore struct {
	mu sync.Mutex

	hashes    map[string]map[string]string
	sets      map[string]map[string]struct{}
	strings   map[string]string
	stringTTL map[string]time.Time

	// setNXReply, when non-nil, overrides the default SetNX behaviour
	// (return true). Tests set it to errSetNXReply{ok:false} to simulate
	// "lock held".
	setNXReply *bool
}

type errSetNXReply struct{}

func newFakeStore() *fakeStore {
	return &fakeStore{
		hashes:    map[string]map[string]string{},
		sets:      map[string]map[string]struct{}{},
		strings:   map[string]string{},
		stringTTL: map[string]time.Time{},
	}
}

func (f *fakeStore) HSet(_ context.Context, key string, fields map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hashes[key]
	if !ok {
		h = map[string]string{}
		f.hashes[key] = h
	}
	for k, v := range fields {
		h[k] = v
	}
	return nil
}

func (f *fakeStore) HGetAll(_ context.Context, key string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hashes[key]
	if !ok {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) Del(_ context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.hashes, k)
		delete(f.strings, k)
		delete(f.sets, k)
		delete(f.stringTTL, k)
	}
	return nil
}

func (f *fakeStore) SAdd(_ context.Context, key string, members ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sets[key]
	if !ok {
		s = map[string]struct{}{}
		f.sets[key] = s
	}
	for _, m := range members {
		s[m] = struct{}{}
	}
	return nil
}

func (f *fakeStore) SRem(_ context.Context, key string, members ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sets[key]
	if !ok {
		return nil
	}
	for _, m := range members {
		delete(s, m)
	}
	return nil
}

func (f *fakeStore) SMembers(_ context.Context, key string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sets[key]
	if !ok {
		return []string{}, nil
	}
	out := make([]string, 0, len(s))
	for m := range s {
		out = append(out, m)
	}
	return out, nil
}

func (f *fakeStore) SetNX(_ context.Context, key, value string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setNXReply != nil {
		return *f.setNXReply, nil
	}
	if _, exists := f.strings[key]; exists {
		return false, nil
	}
	f.strings[key] = value
	if ttl > 0 {
		f.stringTTL[key] = time.Now().Add(ttl)
	}
	return true, nil
}

func (f *fakeStore) DelIfEqual(_ context.Context, key, expected string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got, ok := f.strings[key]; ok && got == expected {
		delete(f.strings, key)
		delete(f.stringTTL, key)
	}
	return nil
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestRegisterCharacter_WritesHashAndSet(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := domainmock.NewMockStore(ctrl)

	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	loc := domain.CharacterLocation{
		CharID:    1001,
		AccountID: 42,
		ZoneID:    "zone-a",
		MapName:   "prt_fild08",
	}

	store.EXPECT().
		HSet(gomock.Any(), "char:location:1001", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, fields map[string]string) error {
			assert.Equal(t, "zone-a", fields["zone_id"])
			assert.Equal(t, "prt_fild08", fields["map_name"])
			assert.Equal(t, "42", fields["account_id"])
			assert.Equal(t, now.Format(time.RFC3339Nano), fields["last_seen"])
			return nil
		})
	store.EXPECT().
		SAdd(gomock.Any(), "zone:chars:zone-a", []string{"1001"}).
		Return(nil)

	r := service.NewRegistry(store, service.WithClock(fixedClock(now)))
	require.NoError(t, r.RegisterCharacter(context.Background(), loc))
}

func TestRegisterCharacter_RejectsEmptyZone(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	r := service.NewRegistry(store)
	err := r.RegisterCharacter(context.Background(), domain.CharacterLocation{CharID: 1})
	require.Error(t, err)
}

func TestUnregisterCharacter_RemovesHashAndSet(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	ctx := context.Background()
	r := service.NewRegistry(store, service.WithClock(fixedClock(time.Now())))

	require.NoError(t, r.RegisterCharacter(ctx, domain.CharacterLocation{
		CharID: 7, AccountID: 1, ZoneID: "zone-a", MapName: "prontera",
	}))
	require.NoError(t, r.UnregisterCharacter(ctx, 7))

	_, err := r.GetCharacterLocation(ctx, 7)
	assert.ErrorIs(t, err, domain.ErrCharacterNotFound)
}

func TestUnregisterCharacter_MissingIsNoop(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	r := service.NewRegistry(store)
	require.NoError(t, r.UnregisterCharacter(context.Background(), 9999))
}

func TestGetCharacterLocation_RoundTrip(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	r := service.NewRegistry(store, service.WithClock(fixedClock(now)))

	require.NoError(t, r.RegisterCharacter(ctx, domain.CharacterLocation{
		CharID: 1001, AccountID: 42, ZoneID: "zone-a", MapName: "prt_fild08",
	}))

	got, err := r.GetCharacterLocation(ctx, 1001)
	require.NoError(t, err)
	assert.Equal(t, uint32(1001), got.CharID)
	assert.Equal(t, uint32(42), got.AccountID)
	assert.Equal(t, "zone-a", got.ZoneID)
	assert.Equal(t, "prt_fild08", got.MapName)
	assert.Equal(t, now, got.LastSeen)
}

func TestGetCharacterLocation_NotFound(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	r := service.NewRegistry(store)
	_, err := r.GetCharacterLocation(context.Background(), 1234)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrCharacterNotFound))
}

func TestListCharactersOnZone(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	ctx := context.Background()
	r := service.NewRegistry(store, service.WithClock(fixedClock(time.Now())))

	require.NoError(t, r.RegisterCharacter(ctx, domain.CharacterLocation{
		CharID: 1, AccountID: 10, ZoneID: "zone-a", MapName: "prontera",
	}))
	require.NoError(t, r.RegisterCharacter(ctx, domain.CharacterLocation{
		CharID: 2, AccountID: 11, ZoneID: "zone-a", MapName: "prontera",
	}))
	require.NoError(t, r.RegisterCharacter(ctx, domain.CharacterLocation{
		CharID: 3, AccountID: 12, ZoneID: "zone-b", MapName: "geffen",
	}))

	got, err := r.ListCharactersOnZone(ctx, "zone-a")
	require.NoError(t, err)
	require.Len(t, got, 2)
	ids := []uint32{got[0].CharID, got[1].CharID}
	assert.ElementsMatch(t, []uint32{1, 2}, ids)

	empty, err := r.ListCharactersOnZone(ctx, "zone-empty")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestAcquireZoneLock_FirstHolderWins(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	ctx := context.Background()
	r := service.NewRegistry(store)

	require.NoError(t, r.AcquireZoneLock(ctx, 100, "zone-a", time.Second))
	err := r.AcquireZoneLock(ctx, 100, "zone-b", time.Second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrLockHeld))
}

func TestReleaseZoneLock_OnlyOwnerCanRelease(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	ctx := context.Background()
	r := service.NewRegistry(store)

	require.NoError(t, r.AcquireZoneLock(ctx, 100, "zone-a", time.Second))
	// Wrong owner: no-op.
	require.NoError(t, r.ReleaseZoneLock(ctx, 100, "zone-b"))
	// Lock should still be held by zone-a.
	err := r.AcquireZoneLock(ctx, 100, "zone-c", time.Second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrLockHeld))

	// Real owner frees it.
	require.NoError(t, r.ReleaseZoneLock(ctx, 100, "zone-a"))
	require.NoError(t, r.AcquireZoneLock(ctx, 100, "zone-c", time.Second))
}

func TestReleaseZoneLock_RejectsEmptyZone(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	r := service.NewRegistry(store)
	require.Error(t, r.ReleaseZoneLock(context.Background(), 1, ""))
}

func TestAcquireZoneLock_RejectsEmptyZone(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	r := service.NewRegistry(store)
	require.Error(t, r.AcquireZoneLock(context.Background(), 1, "", time.Second))
}

func TestLookupLocation_PropagatesStoreErrors(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := domainmock.NewMockStore(ctrl)
	wantErr := errors.New("boom")
	store.EXPECT().HGetAll(gomock.Any(), "char:location:1").Return(nil, wantErr)
	r := service.NewRegistry(store)
	_, err := r.GetCharacterLocation(context.Background(), 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, wantErr))
}

func TestRegisterCharacter_ZoneSetErrorPropagates(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := domainmock.NewMockStore(ctrl)
	wantErr := errors.New("sadd boom")
	store.EXPECT().HSet(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	store.EXPECT().SAdd(gomock.Any(), gomock.Any(), gomock.Any()).Return(wantErr)
	r := service.NewRegistry(store)
	err := r.RegisterCharacter(context.Background(), domain.CharacterLocation{
		CharID: 1, AccountID: 1, ZoneID: "z", MapName: "m",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wantErr))
}

func TestUnregisterCharacter_LookupErrorOtherThanNotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := domainmock.NewMockStore(ctrl)
	wantErr := errors.New("hgetall boom")
	store.EXPECT().HGetAll(gomock.Any(), gomock.Any()).Return(nil, wantErr)
	r := service.NewRegistry(store)
	err := r.UnregisterCharacter(context.Background(), 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, wantErr))
}

func TestListCharactersOnZone_SMembersError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := domainmock.NewMockStore(ctrl)
	wantErr := errors.New("smembers boom")
	store.EXPECT().SMembers(gomock.Any(), gomock.Any()).Return(nil, wantErr)
	r := service.NewRegistry(store)
	_, err := r.ListCharactersOnZone(context.Background(), "z")
	require.Error(t, err)
	assert.True(t, errors.Is(err, wantErr))
}

func TestAcquireZoneLock_StoreError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := domainmock.NewMockStore(ctrl)
	wantErr := errors.New("setnx boom")
	store.EXPECT().SetNX(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, wantErr)
	r := service.NewRegistry(store)
	err := r.AcquireZoneLock(context.Background(), 1, "z", time.Second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, wantErr))
}

func TestReleaseZoneLock_StoreError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := domainmock.NewMockStore(ctrl)
	wantErr := errors.New("eval boom")
	store.EXPECT().DelIfEqual(gomock.Any(), gomock.Any(), gomock.Any()).Return(wantErr)
	r := service.NewRegistry(store)
	err := r.ReleaseZoneLock(context.Background(), 1, "z")
	require.Error(t, err)
	assert.True(t, errors.Is(err, wantErr))
}
