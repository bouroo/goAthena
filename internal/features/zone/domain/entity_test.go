//go:build unit

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
)

func TestEntity_MobFields(t *testing.T) {
	e := domain.Entity{
		ID: 1, Type: domain.EntityMob, X: 10, Y: 20,
		MobID: 1002, HP: 50, MaxHP: 50, AI: 2,
		SpawnOriginX: 10, SpawnOriginY: 20,
		Name: "Poring",
	}
	assert.Equal(t, int32(1002), e.MobID)
	assert.Equal(t, int32(50), e.HP)
	assert.Equal(t, int32(50), e.MaxHP)
	assert.Equal(t, uint8(2), e.AI)
	assert.Equal(t, 10, e.SpawnOriginX)
	assert.Equal(t, 20, e.SpawnOriginY)
	assert.Equal(t, "Poring", e.Name)
}

func TestEntity_PlayerFieldsZeroForMobOnlySiblings(t *testing.T) {
	var player domain.Entity
	player.ID = 7
	player.Type = domain.EntityPlayer
	assert.Equal(t, domain.EntityID(7), player.ID)
	assert.Equal(t, domain.EntityType(domain.EntityPlayer), player.Type)
	assert.Equal(t, int32(0), player.MobID)
	assert.Equal(t, uint8(0), player.AI)
	assert.Equal(t, "", player.Name)
}
