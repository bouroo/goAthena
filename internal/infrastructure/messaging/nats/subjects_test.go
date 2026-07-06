//go:build unit

package nats_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

func TestSubjectConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "zone.transit", natsinfra.SubjectTransitPrefix)
	assert.Equal(t, "social.party", natsinfra.SubjectPartyPrefix)
	assert.Equal(t, "social.guild", natsinfra.SubjectGuildPrefix)
	assert.Equal(t, "global.announce", natsinfra.SubjectAnnounce)
	assert.Equal(t, "request", natsinfra.SubjectTransitRequest)
	assert.Equal(t, "inbox", natsinfra.SubjectTransitInbox)
	assert.Equal(t, "complete", natsinfra.SubjectTransitComplete)
}

func TestTransitRequestSubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		targetZone string
		want       string
	}{
		{"uuidFragment", "zone-1", "zone.transit.request.zone-1"},
		{"uuidTruncated", "abc12345", "zone.transit.request.abc12345"},
		{"empty", "", "zone.transit.request."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, natsinfra.TransitRequestSubject(tc.targetZone))
		})
	}
}

func TestTransitRequestSubject_SanitizesDots(t *testing.T) {
	t.Parallel()

	got := natsinfra.TransitRequestSubject("zone.eu.prontera")
	assert.Equal(t, "zone.transit.request.zone_eu_prontera", got,
		"dots in target zone must be replaced so subject hierarchy stays unambiguous")
}

func TestTransitInboxSubject(t *testing.T) {
	t.Parallel()

	got := natsinfra.TransitInboxSubject("zone-2")
	assert.Equal(t, "zone.transit.inbox.zone-2", got)
}

func TestTransitCompleteSubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		charID uint32
		want   string
	}{
		{"zero", 0, "zone.transit.complete.0"},
		{"small", 1001, "zone.transit.complete.1001"},
		{"maxUint32", 4294967295, "zone.transit.complete.4294967295"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, natsinfra.TransitCompleteSubject(tc.charID))
		})
	}
}

func TestPartySubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		partyID uint32
		want    string
	}{
		{"zero", 0, "social.party.0"},
		{"small", 42, "social.party.42"},
		{"maxUint32", 4294967295, "social.party.4294967295"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, natsinfra.PartySubject(tc.partyID))
		})
	}
}

func TestGuildSubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		guildID uint32
		want    string
	}{
		{"zero", 0, "social.guild.0"},
		{"small", 7, "social.guild.7"},
		{"maxUint32", 4294967295, "social.guild.4294967295"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, natsinfra.GuildSubject(tc.guildID))
		})
	}
}
