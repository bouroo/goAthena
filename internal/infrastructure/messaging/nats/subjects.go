package nats

import (
	"strconv"
	"strings"
)

// Subject constants for inter-service communication. Subjects are versioned
// (the prefix embeds the protocol version) so future migrations can ship a
// parallel set (e.g. "zone.transit.v2.<id>") without breaking live clients.
const (
	// SubjectTransitPrefix is the prefix for cross-zone character transit
	// requests. Requests use request/reply; the source zone targets
	// SubjectTransitRequest, the target zone replies on the inbox subject
	// embedded in the request message.
	SubjectTransitPrefix = "zone.transit"

	// SubjectPartyPrefix is the prefix for social party messages. Party
	// events use pub/sub fan-out within the queue group (zone_id).
	SubjectPartyPrefix = "social.party"

	// SubjectGuildPrefix is the prefix for social guild messages. Guild
	// events use pub/sub fan-out within the queue group (zone_id).
	SubjectGuildPrefix = "social.guild"

	// SubjectAnnounce is the global broadcast subject for system messages.
	// No wildcard suffix: every subscriber sees every message.
	SubjectAnnounce = "global.announce"
)

// Subject fragments appended to a prefix for request/reply shapes.
const (
	// SubjectTransitRequest is appended to SubjectTransitPrefix for source
	// → target zone handshakes.
	SubjectTransitRequest = "request"

	// SubjectTransitInbox is appended to SubjectTransitPrefix for target
	// → source replies (used to bind a reply subject without collisions).
	SubjectTransitInbox = "inbox"

	// SubjectTransitComplete is appended to SubjectTransitPrefix for the
	// "transit complete" broadcast published after a successful handshake.
	SubjectTransitComplete = "complete"
)

// TransitRequestSubject returns the NATS subject for a character transit
// handshake targeting the given target zone id. Zone ids are RFC-compliant
// tokens (UUIDs truncated to 8 chars) — dots are escaped to underscores to
// keep the subject hierarchy unambiguous.
//
//	TransitRequestSubject("zone-1") -> "zone.transit.request.zone-1"
func TransitRequestSubject(targetZoneID string) string {
	return SubjectTransitPrefix + "." + SubjectTransitRequest + "." + sanitizeToken(targetZoneID)
}

// TransitInboxSubject returns the subject the target zone publishes its
// reply on when answering a transit handshake from sourceZoneID. The source
// zone sets this as the request's Reply field.
//
//	TransitInboxSubject("zone-2") -> "zone.transit.inbox.zone-2"
func TransitInboxSubject(sourceZoneID string) string {
	return SubjectTransitPrefix + "." + SubjectTransitInbox + "." + sanitizeToken(sourceZoneID)
}

// TransitCompleteSubject returns the subject for the "transit complete"
// broadcast published after a successful cross-zone move for the given
// character id.
//
//	TransitCompleteSubject(1001) -> "zone.transit.complete.1001"
func TransitCompleteSubject(charID uint32) string {
	return SubjectTransitPrefix + "." + SubjectTransitComplete + "." + strconv.FormatUint(uint64(charID), 10)
}

// PartySubject returns the subject for party messages addressed to the
// given party id.
//
//	PartySubject(42) -> "social.party.42"
func PartySubject(partyID uint32) string {
	return SubjectPartyPrefix + "." + strconv.FormatUint(uint64(partyID), 10)
}

// GuildSubject returns the subject for guild messages addressed to the
// given guild id.
//
//	GuildSubject(7) -> "social.guild.7"
func GuildSubject(guildID uint32) string {
	return SubjectGuildPrefix + "." + strconv.FormatUint(uint64(guildID), 10)
}

// sanitizeToken replaces NATS wildcard and delimiter characters (".", ">", "*")
// with "_" so a caller-supplied id cannot create a new subject level, match
// across token boundaries, or inject a wildcard scope. All current callers
// pass either a UUID fragment (RFC 4122 hex + "-", no dots) or a uint32 (no
// dots); this is a defensive net for future id formats (e.g. FQDN-style
// zone ids).
func sanitizeToken(s string) string {
	if s == "" {
		return ""
	}
	return strings.NewReplacer(".", "_", ">", "_", "*", "_").Replace(s)
}
